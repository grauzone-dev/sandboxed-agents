package livesuite_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type sshFixture struct {
	t                                *testing.T
	config                           livesuite.Config
	name, state, sshRoot, userConfig string
	volumes                          bool
	requests                         []process.Request
	userBaseline                     []byte
}

func newSSHFixture(t *testing.T, hostOS string) *sshFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("CONTAINER_CONNECTION", "")
	root, err := controllergroup.StateDirectory(hostOS, "live")
	if err != nil {
		t.Fatal(err)
	}
	f := &sshFixture{t: t, sshRoot: filepath.Join(root, "ssh"), userConfig: filepath.Join(home, ".ssh", "config")}
	f.config = livesuite.Config{OptIn: true, SSH: true, Commit: commit, Repository: t.TempDir(), OutputDirectory: t.TempDir(), Host: platform.Host{OS: hostOS, Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}, Run: f.run, Stdout: io.Discard, Stderr: io.Discard}
	return f
}

func (f *sshFixture) keyDirectory() string {
	return filepath.Join(f.sshRoot, "sandbox-"+hex.EncodeToString([]byte(f.name)))
}

func (f *sshFixture) run(_ context.Context, request process.Request) (int, error) {
	f.requests = append(f.requests, request)
	write := func(value any) (int, error) { return 0, json.NewEncoder(request.Streams.Stdout).Encode(value) }
	args := request.Args
	if request.Name == "git" || request.Name == "go" {
		return 0, nil
	}
	if request.Name == "powershell.exe" {
		fmt.Fprintln(request.Streams.Stdout, "standard-account")
		return 0, nil
	}
	if request.Name == "ssh" {
		if slices.Contains(args, "-V") {
			version := "OpenSSH_9.2p1"
			if f.config.Host.OS == "windows" {
				version = "OpenSSH_for_Windows_9.5p1"
			}
			fmt.Fprintln(request.Streams.Stderr, version)
		} else if slices.Contains(args, "-G") {
			fmt.Fprintf(request.Streams.Stdout, "hostname 127.0.0.1\nuser agent\nport 2222\nidentitiesonly yes\nidentityagent none\nforwardagent no\nstricthostkeychecking true\nglobalknownhostsfile none\nidentityfile \"%s\"\nuserknownhostsfile \"%s\"\n", filepath.ToSlash(filepath.Join(f.keyDirectory(), "id_ed25519")), filepath.ToSlash(filepath.Join(f.keyDirectory(), "known_hosts")))
		} else {
			if f.state != "running" {
				return 255, nil
			}
			fmt.Fprintln(request.Streams.Stdout, "agent")
		}
		return 0, nil
	}
	if request.Name == "podman" {
		if args[0] == "--connection" {
			args = args[2:]
		}
		switch args[0] {
		case "info":
			return write(map[string]any{"host": map[string]any{"serviceIsRemote": false}})
		case "machine":
			if args[1] == "list" {
				return write([]any{map[string]any{"Name": "selected-machine", "Default": true, "Running": true, "VMType": "wsl"}})
			}
			return write([]any{map[string]any{"Name": "selected-machine", "State": "running", "Rootful": false}})
		case "ps":
			items := []any{}
			if f.state != "" {
				items = append(items, map[string]any{"Names": []string{"sandboxed-agents.live." + f.name}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			return write(items)
		case "volume":
			items := []any{}
			if f.volumes {
				items = append(items, map[string]any{"Name": "sandboxed-agents.live." + f.name + ".home", "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			return write(items)
		case "container":
			return write([]any{map[string]any{"Name": "sandboxed-agents.live." + f.name, "State": map[string]any{"Running": true}, "Config": map[string]any{"Labels": map[string]string{"io.github.sandboxed-agents.owner": "live", "io.github.sandboxed-agents.sandbox-name": f.name}}, "NetworkSettings": map[string]any{"Ports": map[string]any{"22/tcp": []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": "2222"}}}}}})
		}
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(request.Streams.Stdout, "sandboxed-agents %s\nassets %s\n", commit, strings.Repeat("a", 64))
	case "list":
		fmt.Fprintln(request.Streams.Stdout, "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES")
	case "up":
		f.name, f.state, f.volumes = args[1], "running", true
		f.userBaseline, _ = os.ReadFile(f.userConfig)
		if !slices.Contains(args, "--ssh-config") {
			f.t.Fatal("SSH sandbox created without SSH setup")
		}
		for _, path := range []string{filepath.Dir(f.userConfig), f.keyDirectory()} {
			if err := os.MkdirAll(path, 0700); err != nil {
				return 1, err
			}
		}
		for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "entry"} {
			if err := os.WriteFile(filepath.Join(f.keyDirectory(), name), []byte("private-fixture-"+name), 0600); err != nil {
				return 1, err
			}
		}
		if err := os.WriteFile(filepath.Join(f.sshRoot, "config"), []byte("Host "+f.name+".live\n"), 0600); err != nil {
			return 1, err
		}
		installed := append([]byte("Include \""+filepath.ToSlash(filepath.Join(f.sshRoot, "config"))+"\"\n"), f.userBaseline...)
		return 0, os.WriteFile(f.userConfig, installed, 0600)
	case "stop":
		f.state = "stopped"
	case "start":
		if len(args) != 2 {
			f.t.Fatal("restart reinstalled SSH setup")
		}
		f.state = "running"
	case "remove":
		if !slices.Contains(args, "--volumes") || slices.Contains(args, "--force") {
			f.t.Fatalf("incorrect cleanup: %v", args)
		}
		f.state, f.volumes = "", false
		if err := os.RemoveAll(f.sshRoot); err != nil {
			return 1, err
		}
		return 0, os.WriteFile(f.userConfig, f.userBaseline, 0600)
	default:
		f.t.Fatalf("unexpected process: %s %v", request.Name, args)
	}
	return 0, nil
}

func TestLiveSSHConnectsBeforeAndAfterRestartAndCleansUp(t *testing.T) {
	for _, hostOS := range []string{"linux", "windows"} {
		t.Run(hostOS, func(t *testing.T) {
			f := newSSHFixture(t, hostOS)
			if err := livesuite.Run(context.Background(), f.config); err != nil {
				t.Fatal(err)
			}
			platformName := map[string]string{"linux": "linux", "windows": "windows-11"}[hostOS]
			summary := readSummary(t, f.config, platformName)
			for _, name := range []string{"ssh/up", "ssh/created/loopback", "ssh/created/config", "ssh/created/connect", "ssh/stop", "ssh/start", "ssh/started-again/connect", "ssh/cleanup", "ssh"} {
				if !hasCheck(summary, name, "pass") {
					t.Fatalf("missing check %s: %+v", name, summary)
				}
			}
			connections := 0
			for _, request := range f.requests {
				if request.Name == "ssh" && !slices.Contains(request.Args, "-G") && !slices.Contains(request.Args, "-V") {
					connections++
					want := []string{"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", f.name + ".live", "id", "-un"}
					if !slices.Equal(request.Args, want) {
						t.Fatalf("ssh args=%v", request.Args)
					}
				}
			}
			if connections != 2 || f.state != "" || f.volumes {
				t.Fatalf("connections=%d sandbox=%q volumes=%t", connections, f.state, f.volumes)
			}
			if summary.Result != "pass" {
				t.Fatalf("summary=%+v", summary)
			}
			data, err := os.ReadFile(filepath.Join(f.config.OutputDirectory, "live-suite-"+platformName+".json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{f.name, f.sshRoot, f.userConfig, "selected-machine", "private-fixture"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("summary contains %q: %s", secret, data)
				}
			}
			if hostOS == "windows" && !hasCheck(summary, "ssh/standard-account", "pass") {
				t.Fatal("Windows standard-account check missing")
			}
		})
	}
}

func TestLiveSSHRejectsUnsafeBindingsAndConfiguration(t *testing.T) {
	for _, failure := range []string{"wildcard-ip", "ipv6-ip", "empty-ip", "extra-binding", "missing-binding", "foreign-owner", "identity-agent", "extra-identity", "hostkey-check", "user", "wrong-user", "permission-error", "ssh-exit", "start-key-change", "reconnect"} {
		t.Run(failure, func(t *testing.T) {
			f := newSSHFixture(t, "linux")
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				var output bytes.Buffer
				original := request.Streams.Stdout
				request.Streams.Stdout = &output
				status, err := f.run(ctx, request)
				data := output.String()
				if request.Name == "podman" && slices.Contains(request.Args, "inspect") && request.Args[0] == "container" {
					switch failure {
					case "wildcard-ip":
						data = strings.ReplaceAll(data, "127.0.0.1", "0.0.0.0")
					case "ipv6-ip":
						data = strings.ReplaceAll(data, "127.0.0.1", "::1")
					case "empty-ip":
						data = strings.ReplaceAll(data, "127.0.0.1", "")
					case "extra-binding":
						data = strings.ReplaceAll(data, `"HostPort":"2222"}`, `"HostPort":"2222"},{"HostIp":"0.0.0.0","HostPort":"2222"}`)
					case "missing-binding":
						data = strings.ReplaceAll(data, "22/tcp", "23/tcp")
					case "foreign-owner":
						data = strings.ReplaceAll(data, `"io.github.sandboxed-agents.owner":"live"`, `"io.github.sandboxed-agents.owner":"other"`)
					}
				}
				if request.Name == "ssh" && slices.Contains(request.Args, "-G") {
					switch failure {
					case "identity-agent":
						data = strings.ReplaceAll(data, "identityagent none", "identityagent /host/agent.sock")
					case "extra-identity":
						data += "identityfile /host/other-key\n"
					case "hostkey-check":
						data = strings.ReplaceAll(data, "stricthostkeychecking true", "stricthostkeychecking no")
					case "user":
						data = strings.ReplaceAll(data, "user agent", "user root")
					}
				}
				if request.Name == "ssh" && slices.Contains(request.Args, "id") {
					switch failure {
					case "wrong-user":
						data = "root\n"
					case "permission-error":
						fmt.Fprintln(request.Streams.Stderr, "WARNING: UNPROTECTED PRIVATE KEY FILE! private-fixture-secret")
					case "ssh-exit":
						status = 255
					case "reconnect":
						for _, prior := range f.requests {
							if len(prior.Args) > 0 && prior.Args[0] == "start" {
								status = 255
							}
						}
					}
				}
				if failure == "start-key-change" && request.Args[0] == "start" {
					err = os.WriteFile(filepath.Join(f.keyDirectory(), "known_hosts"), []byte("changed-pin"), 0600)
				}
				if original != nil {
					fmt.Fprint(original, data)
				}
				return status, err
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("SSH violation passed")
			}
			summary := readSummary(t, f.config, "linux")
			if summary.Result != "fail" || !hasCheck(summary, "ssh", "fail") || !hasCheck(summary, "ssh/cleanup", "pass") || f.state != "" || f.volumes {
				t.Fatalf("summary=%+v sandbox=%s volumes=%t", summary, f.state, f.volumes)
			}
		})
	}
}

func TestLiveSSHRefusesWindowsAdministratorAndNonWindowsOpenSSH(t *testing.T) {
	for _, failure := range []string{"administrator", "account-query-failure", "account-query-malformed", "git-openssh"} {
		t.Run(failure, func(t *testing.T) {
			f := newSSHFixture(t, "windows")
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				if request.Name == "powershell.exe" {
					if failure == "administrator" {
						return 1, nil
					}
					if failure == "account-query-failure" {
						return 0, os.ErrPermission
					}
					if failure == "account-query-malformed" {
						fmt.Fprintln(request.Streams.Stdout, "unknown")
						return 0, nil
					}
				}
				if request.Name == "ssh" && slices.Contains(request.Args, "-V") {
					fmt.Fprintln(request.Streams.Stderr, "OpenSSH_9.2p1")
					return 0, nil
				}
				return f.run(ctx, request)
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("unsupported Windows SSH run passed")
			}
			if f.name != "" {
				t.Fatal("unsafe Windows run reached up")
			}
			summary := readSummary(t, f.config, "windows-11")
			if !hasCheck(summary, "ssh", "fail") {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestLiveSSHReportsNotRunWithoutSelectingThePart(t *testing.T) {
	f := newSSHFixture(t, "linux")
	f.config.SSH = false
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	if f.name != "" || !hasCheck(readSummary(t, f.config, "linux"), "ssh", "not-run") {
		t.Fatal("unselected SSH part ran or was reported as verified")
	}
}

func TestLiveSSHRefusesStaleGroupSSHFilesAndIncludeBeforeUp(t *testing.T) {
	for _, stale := range []string{"files", "include"} {
		t.Run(stale, func(t *testing.T) {
			f := newSSHFixture(t, "linux")
			path := filepath.Join(f.sshRoot, "config")
			data := []byte("Host previous.live\n")
			if stale == "include" {
				path = f.userConfig
				data = []byte("Include \"" + filepath.ToSlash(filepath.Join(f.sshRoot, "config")) + "\"\n")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("stale SSH setup passed")
			}
			if f.name != "" || !hasCheck(readSummary(t, f.config, "linux"), "ssh/host-clean", "fail") {
				t.Fatal("stale setup reached up")
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, data) {
				t.Fatalf("existing setup changed: %q %v", current, err)
			}
		})
	}
}

func TestLiveSSHCleansUpAfterCancellationWithAFreshDeadline(t *testing.T) {
	f := newSSHFixture(t, "linux")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaned := false
	f.config.Run = func(callCtx context.Context, request process.Request) (int, error) {
		if request.Name == "ssh" && slices.Contains(request.Args, "id") {
			cancel()
			return 0, ctx.Err()
		}
		if request.Args[0] == "remove" {
			deadline, ok := callCtx.Deadline()
			if callCtx.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 90*time.Second {
				t.Fatal("cleanup did not use a fresh bounded context")
			}
			cleaned = true
		}
		return f.run(callCtx, request)
	}
	if err := livesuite.Run(ctx, f.config); err == nil {
		t.Fatal("cancelled run passed")
	}
	if !cleaned || f.state != "" || f.volumes || !hasCheck(readSummary(t, f.config, "linux"), "ssh/cleanup", "pass") {
		t.Fatal("cancelled SSH run left state behind")
	}
}

func TestLiveSSHCannotPassWhenCleanupLeavesObjectsOrHostFiles(t *testing.T) {
	for _, leftover := range []string{"volume", "key", "include", "remove-fails"} {
		t.Run(leftover, func(t *testing.T) {
			f := newSSHFixture(t, "linux")
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] == "remove" && leftover == "remove-fails" {
					return 42, nil
				}
				status, err := f.run(ctx, request)
				if request.Args[0] == "remove" {
					switch leftover {
					case "volume":
						f.volumes = true
					case "key":
						if err = os.MkdirAll(f.keyDirectory(), 0700); err == nil {
							err = os.WriteFile(filepath.Join(f.keyDirectory(), "id_ed25519"), []byte("private-fixture"), 0600)
						}
					case "include":
						err = os.WriteFile(f.userConfig, []byte("Include private-fixture\n"), 0600)
					}
				}
				return status, err
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("incomplete cleanup passed")
			}
			summary := readSummary(t, f.config, "linux")
			if summary.Result != "fail" || !hasCheck(summary, "ssh/cleanup", "fail") || !hasCheck(summary, "ssh", "fail") {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestLiveSSHRestoresExistingUserConfigurationAndCleansPartialUp(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			f := newSSHFixture(t, "linux")
			original := []byte("# existing user configuration\nHost personal\n  HostName example.invalid\nInclude /another-group/config\n")
			if err := os.MkdirAll(filepath.Dir(f.userConfig), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.userConfig, original, 0600); err != nil {
				t.Fatal(err)
			}
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				status, err := f.run(ctx, request)
				if partial && request.Args[0] == "up" {
					return 42, nil
				}
				return status, err
			}
			err := livesuite.Run(context.Background(), f.config)
			if (err != nil) != partial {
				t.Fatalf("partial=%t error=%v", partial, err)
			}
			current, err := os.ReadFile(f.userConfig)
			if err != nil || !bytes.Equal(original, current) || f.state != "" || f.volumes {
				t.Fatalf("config=%q error=%v sandbox=%s volumes=%t", current, err, f.state, f.volumes)
			}
			if !hasCheck(readSummary(t, f.config, "linux"), "ssh/cleanup", "pass") {
				t.Fatal("SSH cleanup did not pass")
			}
		})
	}
}
