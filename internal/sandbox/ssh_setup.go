package sandbox

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
)

type SSHSetup struct {
	*sandboxObjects
	hostOS string
}

func NewSSHSetup(name, group, hostOS string, run process.Runner, streams process.Streams) *SSHSetup {
	return &SSHSetup{sandboxObjects: newSandboxObjects(name, group, run, streams), hostOS: hostOS}
}

type sshPaths struct{ groupDirectory, sandboxDirectory, config, userConfig string }

func (setup *SSHSetup) paths() (sshPaths, error) {
	root, err := controllergroup.StateDirectory(setup.hostOS, setup.group)
	if err != nil {
		return sshPaths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return sshPaths{}, err
	}
	if setup.hostOS == "windows" && os.Getenv("USERPROFILE") != "" {
		home = os.Getenv("USERPROFILE")
	}
	root = filepath.Join(root, "ssh")
	return sshPaths{groupDirectory: root, sandboxDirectory: filepath.Join(root, "sandbox-"+hex.EncodeToString([]byte(setup.name))), config: filepath.Join(root, "config"), userConfig: filepath.Join(home, ".ssh", "config")}, nil
}

func sshConfigPath(path string) string {
	return `"` + strings.ReplaceAll(filepath.ToSlash(path), `"`, `\"`) + `"`
}

func (setup *SSHSetup) hostName() string {
	if setup.group == "default" {
		return setup.name
	}
	return setup.name + "." + setup.group
}

func (setup *SSHSetup) entry(paths sshPaths) (string, error) {
	port, err := recordedSSHPort(setup.name, setup.containerLabels)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Host %s\n  HostName 127.0.0.1\n  Port %d\n  User agent\n  IdentityFile %s\n  UserKnownHostsFile %s\n  GlobalKnownHostsFile none\n  HostKeyAlgorithms ssh-ed25519\n  UpdateHostKeys no\n  IdentitiesOnly yes\n  IdentityAgent none\n  ForwardAgent no\n  StrictHostKeyChecking yes\n", setup.hostName(), port, sshConfigPath(filepath.Join(paths.sandboxDirectory, "id_ed25519")), sshConfigPath(filepath.Join(paths.sandboxDirectory, "known_hosts"))), nil
}

func (setup *SSHSetup) Print() error {
	paths, err := setup.paths()
	if err != nil {
		return err
	}
	entry, err := os.ReadFile(filepath.Join(paths.sandboxDirectory, "entry"))
	if errors.Is(err, os.ErrNotExist) {
		text, err := setup.entry(paths)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(setup.streams.Stderr, sshNotInstalledFormat, setup.name); err != nil {
			return err
		}
		_, err = fmt.Fprint(setup.streams.Stdout, text)
		return err
	}
	if err != nil {
		return err
	}
	config, err := os.ReadFile(paths.config)
	if err != nil {
		return err
	}
	if !bytes.Contains(config, entry) {
		return fmt.Errorf(sshIncompleteFormat, setup.name)
	}
	_, err = setup.streams.Stdout.Write(entry)
	return err
}

func (setup *SSHSetup) hostKey(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output, diagnostic bytes.Buffer
	status, err := setup.run(ctx, process.Request{Name: "podman", Args: managerArgs(setup.container, "ssh", "host-key", "--wait"), Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err != nil {
		return "", err
	}
	if status != 0 {
		return "", fmt.Errorf(sshHostKeyFailureFormat, strings.TrimSpace(diagnostic.String()))
	}
	return sshkeys.ParsePublicKey(output.Bytes())
}

func (setup *SSHSetup) checkHostEntryConflict(ctx context.Context, userConfig string) error {
	query := func(args ...string) (map[string]string, error) {
		var output, diagnostic bytes.Buffer
		status, err := setup.run(ctx, process.Request{Name: "ssh", Args: args, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
		if err != nil {
			return nil, err
		}
		if status != 0 {
			return nil, fmt.Errorf(sshQueryFailureFormat, strings.TrimSpace(diagnostic.String()))
		}
		values := make(map[string]string)
		for _, line := range strings.Split(output.String(), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			key := strings.ToLower(fields[0])
			values[key] += strings.Join(fields[1:], " ") + "\n"
		}
		if values["host"] == "" || values["hostname"] == "" {
			return nil, errors.New(sshInvalidQuery)
		}
		return values, nil
	}
	config := userConfig
	if _, err := os.Stat(userConfig); errors.Is(err, os.ErrNotExist) {
		config = "none"
	} else if err != nil {
		return err
	}
	actual, err := query("-G", "-F", config, setup.hostName())
	if err != nil {
		return err
	}
	defaults, err := query("-G", "-F", "none", setup.hostName())
	if err != nil {
		return err
	}
	if len(actual) != len(defaults) {
		return fmt.Errorf(sshNameConflictFormat, setup.hostName())
	}
	for key, value := range actual {
		if defaults[key] != value {
			return fmt.Errorf(sshNameConflictFormat, setup.hostName())
		}
	}
	return nil
}

func (setup *SSHSetup) Install(ctx context.Context) (err error) {
	paths, err := setup.paths()
	if err != nil {
		return err
	}
	key, err := setup.hostKey(ctx)
	if err != nil {
		return err
	}
	port, err := recordedSSHPort(setup.name, setup.containerLabels)
	if err != nil {
		return err
	}
	hostToken := "127.0.0.1"
	if port != 22 {
		hostToken = fmt.Sprintf("[127.0.0.1]:%d", port)
	}
	pin := hostToken + " " + key + "\n"
	installed, err := os.ReadFile(filepath.Join(paths.sandboxDirectory, "entry"))
	if err == nil {
		pinned, err := os.ReadFile(filepath.Join(paths.sandboxDirectory, "known_hosts"))
		if err != nil {
			return err
		}
		if string(pinned) != pin {
			return fmt.Errorf(sshHostKeyMismatchFormat, setup.name)
		}
		config, err := os.ReadFile(paths.config)
		if err != nil {
			return err
		}
		if !bytes.Contains(config, installed) {
			return fmt.Errorf(sshIncompleteFormat, setup.name)
		}
		_, err = fmt.Fprintf(setup.streams.Stdout, sshUnchangedFormat, setup.name)
		return err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Lstat(paths.sandboxDirectory); err == nil {
		return fmt.Errorf(sshExistingStateFormat, paths.sandboxDirectory)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := setup.checkHostEntryConflict(ctx, paths.userConfig); err != nil {
		return err
	}
	entry, err := setup.entry(paths)
	if err != nil {
		return err
	}
	config, err := readOptionalFile(paths.config)
	if err != nil {
		return err
	}
	userConfig, err := readOptionalFile(paths.userConfig)
	if err != nil {
		return err
	}
	include := "Include " + sshConfigPath(paths.config) + "\n"
	hasInclude := false
	for _, line := range strings.Split(string(userConfig), "\n") {
		if strings.TrimSuffix(line, "\r") == strings.TrimSuffix(include, "\n") {
			hasInclude = true
		}
	}
	committed := false
	created, err := createSSHDirectories(paths.groupDirectory)
	if err != nil {
		return err
	}
	defer func() {
		if !committed {
			for _, path := range created {
				if cleanup := os.Remove(path); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
					err = errors.Join(err, cleanup)
				}
			}
		}
	}()
	stage, err := os.MkdirTemp(paths.groupDirectory, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	private := filepath.Join(stage, "id_ed25519")
	var diagnostic bytes.Buffer
	status, err := setup.run(ctx, process.Request{Name: "ssh-keygen", Args: []string{"-q", "-t", "ed25519", "-N", "", "-f", private}, Streams: process.Streams{Stderr: &diagnostic}})
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf(sshKeyGenerationFailureFormat, strings.TrimSpace(diagnostic.String()))
	}
	if err := os.Chmod(private, 0600); err != nil {
		return err
	}
	public, err := os.ReadFile(private + ".pub")
	if err != nil {
		return err
	}
	authorized, err := sshkeys.ParsePublicKey(public)
	if err != nil {
		return err
	}
	for path, data := range map[string]string{"id_ed25519.pub": authorized + "\n", "known_hosts": pin, "entry": entry} {
		mode := os.FileMode(0600)
		if path == "id_ed25519.pub" {
			mode = 0644
		}
		if err := os.WriteFile(filepath.Join(stage, path), []byte(data), mode); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(stage, path), mode); err != nil {
			return err
		}
	}
	diagnostic.Reset()
	status, err = setup.run(ctx, process.Request{Name: "podman", Args: slices.Insert(managerArgs(setup.container, "ssh", "authorize"), 1, "--interactive"), Streams: process.Streams{Stdin: strings.NewReader(authorized + "\n"), Stderr: &diagnostic}})
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf(sshAuthorizationFailureFormat, strings.TrimSpace(diagnostic.String()))
	}
	if err := os.Rename(stage, paths.sandboxDirectory); err != nil {
		return err
	}
	defer func() {
		if !committed {
			err = errors.Join(err, os.RemoveAll(paths.sandboxDirectory))
		}
	}()
	managed := bytes.Clone(config)
	if len(managed) > 0 && managed[len(managed)-1] != '\n' {
		managed = append(managed, '\n')
	}
	managed = append(managed, []byte(entry+"Host *\n")...)
	configInfo, statErr := os.Stat(paths.config)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := writeSSHFile(paths.config, managed, 0600); err != nil {
		return err
	}
	defer func() {
		if !committed {
			var rollback error
			if configInfo == nil {
				rollback = os.Remove(paths.config)
			} else {
				rollback = writeSSHFile(paths.config, config, configInfo.Mode().Perm())
			}
			err = errors.Join(err, rollback)
		}
	}()
	if !hasInclude {
		directories, err := createSSHDirectories(filepath.Dir(paths.userConfig))
		if err != nil {
			return err
		}
		defer func() {
			if !committed {
				for _, directory := range directories {
					if cleanup := os.Remove(directory); cleanup != nil && !errors.Is(cleanup, os.ErrNotExist) {
						err = errors.Join(err, cleanup)
					}
				}
			}
		}()
		mode := os.FileMode(0600)
		if info, err := os.Stat(paths.userConfig); err == nil {
			mode = info.Mode().Perm()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeSSHFile(paths.userConfig, append([]byte(include), userConfig...), mode); err != nil {
			return err
		}
	}
	committed = true
	_, err = fmt.Fprintf(setup.streams.Stdout, sshInstalledFormat, setup.name)
	return err
}

func readOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func writeSSHFile(path string, data []byte, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ssh-write-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (up *Up) CheckSSHManager(ctx context.Context, hostOS string) error {
	if !up.containerExists || !up.containerRunning {
		return nil
	}
	return (&SSHSetup{sandboxObjects: up.sandboxObjects, hostOS: hostOS}).CheckManager(ctx)
}

func (up *Up) InstallSSH(ctx context.Context, hostOS string) error {
	state := *up.sandboxObjects
	state.containerLabels = maps.Clone(state.containerLabels)
	if state.containerLabels == nil {
		state.containerLabels = make(map[string]string)
	}
	state.containerLabels[SSHPortLabel] = fmt.Sprint(up.port)
	setup := &SSHSetup{sandboxObjects: &state, hostOS: hostOS}
	if err := setup.CheckManager(ctx); err != nil {
		return err
	}
	return setup.Install(ctx)
}

func (lifecycle *Lifecycle) InstallSSH(ctx context.Context, hostOS string) error {
	setup := &SSHSetup{sandboxObjects: lifecycle.sandboxObjects, hostOS: hostOS}
	if err := setup.CheckManager(ctx); err != nil {
		return err
	}
	return setup.Install(ctx)
}

func createSSHDirectories(path string) ([]string, error) {
	var missing []string
	for directory := path; ; directory = filepath.Dir(directory) {
		info, err := os.Stat(directory)
		if err == nil {
			if !info.IsDir() {
				return nil, &os.PathError{Op: "mkdir", Path: directory, Err: os.ErrInvalid}
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, directory)
		if filepath.Dir(directory) == directory {
			return nil, err
		}
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0700); err != nil {
			for i := index + 1; i < len(missing); i++ {
				os.Remove(missing[i])
			}
			return nil, err
		}
	}
	return missing, nil
}
