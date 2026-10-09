package livesuite

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

type sshScenario struct {
	prefix, afterPhase string
	transition         func(context.Context, string, process.Runner, liveExecutable) error
	beforeRemove       func(context.Context, string, process.Runner, liveExecutable) error
}

func runSSH(ctx context.Context, config Config, run process.Runner, executable, group string, check func(string, func() error) error) error {
	return runSSHScenario(ctx, config, run, executable, group, check, sshScenario{prefix: "ssh", afterPhase: "started-again"})
}

func runSSHScenario(ctx context.Context, config Config, run process.Runner, executable, group string, recordCheck func(string, func() error) error, scenario sshScenario) (result error) {
	check := func(name string, action func() error) error {
		return recordCheck(scenario.prefix+strings.TrimPrefix(name, "ssh"), action)
	}

	if config.Host.OS == "windows" {
		if err := check("ssh/standard-account", func() error {
			var output bytes.Buffer
			// Unlike WindowsIdentity.Groups, whoami includes deny-only groups
			// from an administrator's filtered UAC token. Compare the SID,
			// not localized group names or attributes.
			status, err := run(ctx, process.Request{Name: "whoami.exe", Args: []string{"/groups", "/fo", "csv", "/nh"}, Streams: process.Streams{Stdout: &output, Stderr: config.Stderr}})
			if err != nil || status != 0 {
				return errors.New(sshStandardAccountMessage)
			}
			return verifySSHStandardAccount(output.String())
		}); err != nil {
			return err
		}
	}
	if err := check("ssh/client", func() error {
		var version bytes.Buffer
		status, err := run(ctx, process.Request{Name: "ssh", Args: []string{"-V"}, Streams: process.Streams{Stdout: &version, Stderr: &version}})
		prefix := "OpenSSH_"
		if config.Host.OS == "windows" {
			prefix = "OpenSSH_for_Windows_"
		}
		if err != nil || status != 0 || !strings.HasPrefix(strings.TrimSpace(version.String()), prefix) {
			return errors.New(sshClientMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	var podman process.Runner
	var connection string
	if err := check("ssh/target", func() error {
		var err error
		podman, connection, err = newLivePodman(ctx, config, run)
		if err == nil && strings.HasPrefix(scenario.prefix, "updates/") {
			bound := podman
			podman = func(ctx context.Context, request process.Request) (int, error) {
				if request.Name == "podman" && (len(request.Args) == 0 || request.Args[0] != "machine") {
					if err := requireSelectedConnection(ctx, bound, connection); err != nil {
						return 0, err
					}
				}
				return bound(ctx, request)
			}
		}
		return err
	}); err != nil {
		return err
	}
	if err := check("ssh/group-empty", func() error { return requireEmptyGroup(ctx, podman, group) }); err != nil {
		return err
	}
	var root, userConfig string
	var originalConfig []byte
	if err := check("ssh/host-clean", func() error {
		// Observe the documented host-state layout independently of the
		// installer: these are the files host OpenSSH must actually consume.
		var err error
		root, err = controllergroup.StateDirectory(config.Host.OS, group)
		if err != nil {
			return err
		}
		root = filepath.Join(root, "ssh")
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if config.Host.OS == "windows" && os.Getenv("USERPROFILE") != "" {
			home = os.Getenv("USERPROFILE")
		}
		userConfig = filepath.Join(home, ".ssh", "config")
		entries, err := os.ReadDir(root)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(entries) != 0 {
			return errors.New(sshStateOccupiedMessage)
		}
		originalConfig, err = readOptionalSSHFile(userConfig)
		if err != nil {
			return err
		}
		managedPath := filepath.ToSlash(filepath.Join(root, "config"))
		for _, line := range strings.Split(string(originalConfig), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || !strings.EqualFold(fields[0], "Include") {
				continue
			}
			if config.Host.OS == "windows" {
				line = strings.ToLower(strings.ReplaceAll(line, `\`, "/"))
				managedPath = strings.ToLower(managedPath)
			}
			if strings.Contains(line, managedPath) {
				return errors.New(sshStateOccupiedMessage)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	name := strings.ReplaceAll(scenario.prefix, "/", "-") + "-" + strings.ToLower(rand.Text())
	keyDirectory := filepath.Join(root, "sandbox-"+hex.EncodeToString([]byte(name)))
	command := liveExecutable{run: run, podman: podman, connection: connection, path: executable}
	invoke := func(ctx context.Context, args ...string) error {
		return command.invoke(ctx, process.Request{Args: args, Env: liveCommandEnvironment(config, group), Streams: process.Streams{Stdout: config.Stdout, Stderr: config.Stderr}})
	}
	hostClean := func() error {
		entries, err := os.ReadDir(root)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		current, err := readOptionalSSHFile(userConfig)
		if err != nil {
			return err
		}
		if len(entries) != 0 || !bytes.Equal(current, originalConfig) {
			return errors.New(sshCleanupMessage)
		}
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		result = errors.Join(result, check("ssh/cleanup", func() error {
			if requireEmptyGroup(cleanup, podman, group) != nil || hostClean() != nil {
				if scenario.beforeRemove != nil {
					if err := scenario.beforeRemove(cleanup, name, podman, command); err != nil {
						return err
					}
				}
				if err := invoke(cleanup, "remove", name, "--volumes"); err != nil {
					return err
				}
			}
			return errors.Join(requireEmptyGroup(cleanup, podman, group), hostClean())
		}))
	}()
	if err := check("ssh/up", func() error {
		return invoke(ctx, "up", name, "--with", "none", "--ssh-config", "--memory", "256m", "--cpus", "1", "--pids-limit", "128", "--shm-size", "16m")
	}); err != nil {
		return err
	}
	verifyAccess := func(phase string) error {
		var port string
		if err := check("ssh/"+phase+"/loopback", func() error {
			var err error
			port, err = readSSHBinding(ctx, podman, group, name)
			return err
		}); err != nil {
			return err
		}
		if err := check("ssh/"+phase+"/config", func() error {
			var output bytes.Buffer
			status, err := run(ctx, process.Request{Name: "ssh", Args: []string{"-G", name + "." + group}, Streams: process.Streams{Stdout: &output, Stderr: config.Stderr}})
			if err != nil || status != 0 {
				return errors.New(sshConfigMessage)
			}
			return verifySSHConfig(output.String(), keyDirectory, port, config.Host.OS)
		}); err != nil {
			return err
		}
		return check("ssh/"+phase+"/connect", func() error {
			connectionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var output, diagnostic bytes.Buffer
			status, err := run(connectionCtx, process.Request{Name: "ssh", Args: []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", name + "." + group, "id", "-un"}, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
			if config.Stderr != nil {
				_, _ = config.Stderr.Write(diagnostic.Bytes())
			}
			if err != nil || status != 0 || strings.TrimSpace(output.String()) != "agent" || diagnostic.Len() != 0 {
				return errors.New(sshIdentityMessage)
			}
			return nil
		})
	}
	if err := verifyAccess("created"); err != nil {
		return err
	}
	var originalState map[string]string
	var installedUserConfig []byte
	if err := check("ssh/created/setup-snapshot", func() error {
		var err error
		originalState, err = readSSHState(root)
		if err != nil {
			return err
		}
		installedUserConfig, err = readOptionalSSHFile(userConfig)
		return err
	}); err != nil {
		return err
	}
	if scenario.transition != nil {
		if err := scenario.transition(ctx, name, podman, command); err != nil {
			return err
		}
	} else {
		if err := check("ssh/stop", func() error { return invoke(ctx, "stop", name) }); err != nil {
			return err
		}
		if err := check("ssh/start", func() error { return invoke(ctx, "start", name) }); err != nil {
			return err
		}
	}
	if scenario.afterPhase == "" {
		return nil
	}
	if err := check("ssh/"+scenario.afterPhase+"/setup-unchanged", func() error {
		current, err := readSSHState(root)
		if err != nil {
			return err
		}
		userState, err := readOptionalSSHFile(userConfig)
		if err != nil {
			return err
		}
		if !maps.Equal(originalState, current) || !bytes.Equal(installedUserConfig, userState) {
			return errors.New(sshStateChangedMessage)
		}
		return nil
	}); err != nil {
		return err
	}
	return verifyAccess(scenario.afterPhase)
}

func verifySSHStandardAccount(output string) error {
	reader := csv.NewReader(strings.NewReader(output))
	reader.FieldsPerRecord = 4
	groups, err := reader.ReadAll()
	if err != nil || len(groups) == 0 {
		return errors.New(sshStandardAccountMessage)
	}
	for _, group := range groups {
		if !strings.HasPrefix(group[2], "S-1-") || group[2] == "S-1-5-32-544" {
			return errors.New(sshStandardAccountMessage)
		}
	}
	return nil
}

func readSSHBinding(ctx context.Context, podman process.Runner, group, name string) (string, error) {
	var records []struct {
		Name            string
		State           struct{ Running bool }
		Config          struct{ Labels map[string]string }
		NetworkSettings struct {
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if err := liveJSON(ctx, podman, []string{"container", "inspect", "sandboxed-agents." + group + "." + name}, &records); err != nil {
		return "", err
	}
	if len(records) != 1 {
		return "", errors.New(sshBindingMessage)
	}
	record := records[0]
	bindings := record.NetworkSettings.Ports["22/tcp"]
	if record.Name != "sandboxed-agents."+group+"."+name || !record.State.Running || record.Config.Labels[sandbox.OwnerLabel] != group || record.Config.Labels[sandbox.NameLabel] != name || len(bindings) != 1 {
		return "", errors.New(sshBindingMessage)
	}
	port, err := strconv.Atoi(bindings[0].HostPort)
	if bindings[0].HostIP != "127.0.0.1" || err != nil || port < 1 || port > 65535 {
		return "", errors.New(sshBindingMessage)
	}
	return bindings[0].HostPort, nil
}

func verifySSHConfig(output, directory, port, hostOS string) error {
	values := map[string][]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if found {
			values[key] = append(values[key], strings.TrimSpace(value))
		}
	}
	expected := map[string]string{"hostname": "127.0.0.1", "user": "agent", "port": port, "identitiesonly": "yes", "identityagent": "none", "forwardagent": "no", "globalknownhostsfile": "none"}
	for key, want := range expected {
		if len(values[key]) != 1 || values[key][0] != want {
			return errors.New(sshConfigMessage)
		}
	}
	strict := values["stricthostkeychecking"]
	if len(strict) != 1 || (strict[0] != "true" && strict[0] != "yes") {
		return errors.New(sshConfigMessage)
	}
	for key, file := range map[string]string{"identityfile": "id_ed25519", "userknownhostsfile": "known_hosts"} {
		if len(values[key]) != 1 {
			return errors.New(sshConfigMessage)
		}
		actual := filepath.ToSlash(filepath.Clean(strings.Trim(values[key][0], `"`)))
		want := filepath.ToSlash(filepath.Join(directory, file))
		if hostOS == "windows" {
			actual, want = strings.ToLower(actual), strings.ToLower(want)
		}
		if actual != want {
			return errors.New(sshConfigMessage)
		}
	}
	return nil
}

func readOptionalSSHFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func readSSHState(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	})
	return files, err
}
