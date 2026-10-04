package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func sshSetupHost(t *testing.T, windows bool) (*testutil.FakePrograms, string, string, string) {
	t.Helper()
	fakes, fixture := resourceLimitHost(t, windows)
	home, state := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
	return fakes, fixture, filepath.Join(home, ".ssh"), filepath.Join(state, "sandboxed-agents")
}

func TestSSHConfigPrintsUninstalledEntryWithoutChangingFiles(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{false, true} {
			t.Run(host.name+"/running-"+stringBool(running), func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				owned := "default"
				scriptLifecycleObjects(t, fakes, host.windows, upObjectResponses(&owned, running, nil, nil), map[string]string{"ssh-port": "2222"})
				beforeSSH, beforeState := sshDirectoryContents(t, sshDir), sshDirectoryContents(t, state)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01")
				if status != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, want := range []string{"Host agent01\n", "HostName 127.0.0.1", "Port 2222", "User agent", "IdentitiesOnly yes", "IdentityAgent none", "ForwardAgent no", "StrictHostKeyChecking yes"} {
					if !strings.Contains(stdout, want) {
						t.Errorf("entry=%q lacks %q", stdout, want)
					}
				}
				if !strings.Contains(stderr, "not installed") || !strings.Contains(stderr, "ssh-config agent01 --install") {
					t.Errorf("note=%q", stderr)
				}
				if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) {
					t.Fatal("printing changed files")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func stringBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

const sshClientPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const sshHostPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEB"
const sshChangedHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgIC"

func scriptSSHInstall(t *testing.T, fakes *testutil.FakePrograms, windows bool, name, group string, running bool, extra ...testutil.Response) {
	t.Helper()
	responses := upObjectResponses(&group, running, nil, nil)
	for i := range responses {
		responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, "sandboxed-agents.default.agent01", "sandboxed-agents."+group+"."+name)
	}
	responses = withRecordedContainerLabels(t, responses, map[string]string{"ssh-port": "2222"})[1:]
	if windows {
		responses = append(healthyWindowsPodman()[1:3], responses...)
	}
	fakes.Script("podman", append(responses, extra...)...)
}

func scriptSSHDefaults(fakes *testutil.FakePrograms, name string) {
	defaults := "host " + name + "\nhostname " + name + "\nport 22\nuser fixture\n"
	fakes.Script("ssh", testutil.Response{Stdout: defaults}, testutil.Response{Stdout: defaults})
	fakes.Script("ssh-keygen", testutil.Response{GenerateSSHKey: sshClientPublicKey + " comment\n"})
}

func TestSSHSetupInstallsDedicatedKeyPinAndSingleIncludeAndReinstallsWithoutChanges(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			if err := os.MkdirAll(sshDir, 0700); err != nil {
				t.Fatal(err)
			}
			personal := "Host personal\r\n  HostName personal.example\r\n"
			if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(personal), 0640); err != nil {
				t.Fatal(err)
			}
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + " host\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
			scriptSSHDefaults(fakes, "agent01")
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			configPath := filepath.Join(state, "group-default", "ssh", "config")
			managed, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			user, err := os.ReadFile(filepath.Join(sshDir, "config"))
			if err != nil {
				t.Fatal(err)
			}
			wantInclude := "Include \"" + filepath.ToSlash(configPath) + "\"\n"
			if string(user) != wantInclude+personal {
				t.Fatalf("user config=%q", user)
			}
			keyDir := filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")
			pin, err := os.ReadFile(filepath.Join(keyDir, "known_hosts"))
			if err != nil {
				t.Fatal(err)
			}
			if string(pin) != "[127.0.0.1]:2222 "+sshHostPublicKey+"\n" {
				t.Fatalf("pin=%q", pin)
			}
			entry, err := os.ReadFile(filepath.Join(keyDir, "entry"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(managed), string(entry)) {
				t.Fatalf("entry absent: %q", managed)
			}
			if runtime.GOOS != "windows" {
				for path, want := range map[string]os.FileMode{configPath: 0600, filepath.Join(keyDir, "id_ed25519"): 0600, filepath.Join(keyDir, "id_ed25519.pub"): 0644, filepath.Join(keyDir, "known_hosts"): 0600, filepath.Join(sshDir, "config"): 0640} {
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != want {
						t.Fatalf("mode %s: %v %v", path, info, err)
					}
				}
			}
			beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"})
			stdout, stderr, status = runCLI(t, fixture, "ssh-config", "agent01", "--install")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "unchanged") {
				t.Fatalf("repeat status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
				t.Fatal("reinstall changed files")
			}
			if len(fakes.Calls("ssh-keygen")) != 1 {
				t.Fatal("reinstall generated another key")
			}
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", false)
			stdout, stderr, status = runCLI(t, fixture, "ssh-config", "agent01")
			if status != 0 || stderr != "" || stdout != string(entry) {
				t.Fatalf("installed print status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
				t.Fatal("installed print changed files")
			}
		})
	}
}

func TestSSHSetupRefusesStoppedManagerUnavailableAndConfiguredNamesWithoutFileChanges(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, test := range []struct {
			name      string
			running   bool
			responses []testutil.Response
			conflict  bool
			want      string
		}{
			{name: "stopped", running: false, want: "start agent01"},
			{name: "manager unavailable", running: true, responses: []testutil.Response{{ExitCode: 1}}, want: "manager does not answer"},
			{name: "configured host", running: true, responses: []testutil.Response{{Stdout: "sandboxed-agents-manager v1\n"}, {Stdout: sshHostPublicKey + "\n"}}, conflict: true, want: "configured host"},
		} {
			t.Run(host.name+"/"+test.name, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", test.running, test.responses...)
				if test.conflict {
					fakes.Script("ssh", testutil.Response{Stdout: "host agent01\nhostname personal.example\nport 22\n"}, testutil.Response{Stdout: "host agent01\nhostname agent01\nport 22\n"})
				}
				beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
				if status == 0 || !strings.Contains(stderr, test.want) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if test.name == "manager unavailable" && (!strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01")) {
					t.Fatalf("manager guidance=%q", stderr)
				}
				if test.conflict && !strings.Contains(stderr, "agent01") {
					t.Fatalf("conflict not named: %q", stderr)
				}
				if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
					t.Fatal("refusal changed files")
				}
				if len(fakes.Calls("ssh-keygen")) != 0 {
					t.Fatal("refusal generated key")
				}
				for _, call := range fakes.Calls("podman") {
					if slices.Contains(call.Args, "start") {
						t.Fatal("refusal started sandbox")
					}
				}
			})
		}
	}
}

func TestSSHSetupKeepsEachControllerGroupAndAddsOneIncludePerGroup(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			for _, target := range []struct{ name, group, entry string }{{"agent01", "default", "agent01"}, {"agent02", "default", "agent02"}, {"agent01", "live", "agent01.live"}} {
				t.Setenv("SANDBOXED_AGENTS_GROUP", target.group)
				defaultBefore := sshDirectoryContents(t, filepath.Join(state, "group-default"))
				scriptSSHInstall(t, fakes, host.windows, target.name, target.group, true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
				scriptSSHDefaults(fakes, target.entry)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", target.name, "--install")
				if status != 0 || stderr != "" {
					t.Fatalf("%s/%s status=%d stdout=%q stderr=%q", target.group, target.name, status, stdout, stderr)
				}
				config, err := os.ReadFile(filepath.Join(state, "group-"+target.group, "ssh", "config"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(config), "Host "+target.entry+"\n") {
					t.Fatalf("entry=%q", config)
				}
				if target.group == "live" && !reflect.DeepEqual(defaultBefore, sshDirectoryContents(t, filepath.Join(state, "group-default"))) {
					t.Fatal("live changed default host state")
				}
			}
			user, err := os.ReadFile(filepath.Join(sshDir, "config"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(user), "Include ") != 2 {
				t.Fatalf("include lines=%q", user)
			}
			config, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "config"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(config), "Host agent") != 2 {
				t.Fatalf("managed=%q", config)
			}
			if runtime.GOOS != "windows" {
				for path, want := range map[string]os.FileMode{sshDir: 0700, filepath.Join(sshDir, "config"): 0600} {
					info, err := os.Stat(path)
					if err != nil || info.Mode().Perm() != want {
						t.Fatalf("mode %s %v %v", path, info, err)
					}
				}
			}
		})
	}
}

func TestSSHSetupRejectsChangedHostKeyAndUnavailableManagerAfterInstallation(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
			scriptSSHDefaults(fakes, "agent01")
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
			if status != 0 {
				t.Fatalf("initial status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
			for _, managerDown := range []bool{false, true} {
				responses := []testutil.Response{{Stdout: "sandboxed-agents-manager v1\n"}, {Stdout: sshChangedHostKey + "\n"}}
				if managerDown {
					responses = []testutil.Response{{Stdout: "garbled response\n"}}
				}
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, responses...)
				stdout, stderr, status = runCLI(t, fixture, "ssh-config", "agent01", "--install")
				if status == 0 {
					t.Fatalf("unexpected success %q %q", stdout, stderr)
				}
				if managerDown {
					if !strings.Contains(stderr, "manager does not answer") {
						t.Fatal(stderr)
					}
				} else if !strings.Contains(stderr, "ssh-config agent01 --remove") || !strings.Contains(stderr, "ssh-config agent01 --install") {
					t.Fatalf("mismatch guidance=%q", stderr)
				}
				if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
					t.Fatal("failure changed files")
				}
			}
			if len(fakes.Calls("ssh-keygen")) != 1 {
				t.Fatal("failure generated key")
			}
		})
	}
}

func TestUpAndStartInstallTheSameSSHSetupAfterStartingSandbox(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"up-new", "up-existing", "up-running", "start"} {
			t.Run(host.name+"/"+command, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				port := unusedSSHPort(t)
				name := "up"
				args := []string{"up", "agent01", "--ssh-config"}
				install := []testutil.Response{{Stdout: "sandboxed-agents-manager v1\n"}, {Stdout: sshHostPublicKey + "\n"}, {WantStdin: sshClientPublicKey + "\n"}}
				switch command {
				case "up-new":
					responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
					scriptResourceLimitObjects(t, fakes, host.windows, append(responses, install...), nil)
					args = append(args, "--port", strconv.Itoa(port))
				case "up-existing", "up-running":
					group := "default"
					responses := upObjectResponses(&group, command == "up-running", nil, nil)
					if command == "up-running" {
						responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"})
					} else {
						responses = append(responses, testutil.Response{})
					}
					scriptResourceLimitObjects(t, fakes, host.windows, append(responses, install...), map[string]string{"ssh-port": strconv.Itoa(port)})
				case "start":
					name = "start"
					args = []string{"start", "agent01", "--ssh-config"}
					group := "default"
					responses := append(upObjectResponses(&group, false, nil, nil), testutil.Response{})
					scriptLifecycleObjects(t, fakes, host.windows, append(responses, install...), map[string]string{"ssh-port": strconv.Itoa(port)})
				}
				scriptSSHDefaults(fakes, "agent01")
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
					t.Fatalf("%s status=%d stdout=%q stderr=%q", name, status, stdout, stderr)
				}
				entry, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031", "entry"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(entry), "Port "+strconv.Itoa(port)+"\n") {
					t.Fatalf("recorded port entry=%q", entry)
				}
				if _, err := os.Stat(filepath.Join(sshDir, "config")); err != nil {
					t.Fatal(err)
				}
				calls := fakes.Calls("podman")
				var sawStart, sawAuthorize bool
				for _, call := range calls {
					if slices.Contains(call.Args, "start") {
						sawStart = true
					}
					if slices.Contains(call.Args, "authorize") {
						if !sawStart && command != "up-running" {
							t.Fatal("authorization before sandbox start")
						}
						sawAuthorize = true
						if !slices.Contains(call.Args, "--user=0:0") || !slices.Contains(call.Args, "--interactive") {
							t.Fatalf("authorization call=%v", call.Args)
						}
					}
				}
				if !sawAuthorize {
					t.Fatal("no authorization request")
				}
			})
		}
	}
}

func TestSSHSetupFailureAfterUpOrStartKeepsSandboxRunningAndNamesRetry(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"up", "start"} {
			for _, reason := range []string{"manager", "conflict", "keygen", "authorization"} {
				t.Run(host.name+"/"+command+"/"+reason, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					port := unusedSSHPort(t)
					owned := "default"
					responses := append(upObjectResponses(&owned, false, nil, nil), testutil.Response{})
					extra := []testutil.Response{{Stdout: "sandboxed-agents-manager v1\n"}, {Stdout: sshHostPublicKey + "\n"}}
					if reason == "manager" {
						extra = []testutil.Response{{ExitCode: 1}}
					}
					if reason == "authorization" {
						extra = append(extra, testutil.Response{ExitCode: 1, Stderr: "injected authorization refusal"})
					}
					responses = append(responses, extra...)
					if command == "up" {
						scriptResourceLimitObjects(t, fakes, host.windows, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
					} else {
						scriptLifecycleObjects(t, fakes, host.windows, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
					}
					scriptSSHDefaults(fakes, "agent01")
					if reason == "conflict" {
						fakes.Script("ssh", testutil.Response{Stdout: "host agent01\nhostname other\n"}, testutil.Response{Stdout: "host agent01\nhostname agent01\n"})
					}
					if reason == "keygen" {
						fakes.Script("ssh-keygen", testutil.Response{ExitCode: 1, Stderr: "injected key generation refusal"})
					}
					beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
					stdout, stderr, status := runCLI(t, fixture, command, "agent01", "--ssh-config")
					if status == 0 || !strings.Contains(stdout, "Sandbox agent01 is running") || !strings.Contains(stderr, "ssh-config agent01 --install") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					for _, call := range fakes.Calls("podman") {
						if slices.Contains(call.Args, "stop") || slices.Contains(call.Args, "rm") {
							t.Fatalf("installation failure stopped or removed sandbox: %v", call.Args)
						}
					}
					if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
						t.Fatal("failed installation changed host files")
					}
				})
			}
		}
	}
}

func TestSSHCommandsApplySandboxOwnerAndInterruptedUpdateChecksBeforeRunningAndInstallation(t *testing.T) {
	owned, foreign := "default", "another-group"
	for _, host := range resourceLimitHosts {
		for _, args := range [][]string{{"ssh-config", "agent01"}, {"ssh-config", "agent01", "--install"}, {"start", "agent01", "--ssh-config"}} {
			for _, test := range []struct {
				name    string
				owner   *string
				volumes map[string]string
				backup  *string
				want    string
			}{
				{name: "unknown", want: "does not exist"},
				{name: "volumes only", volumes: map[string]string{"ssh": owned}, want: "up agent01"},
				{name: "volumes only foreign", volumes: map[string]string{"ssh": foreign}, want: "Podman"},
				{name: "foreign container", owner: &foreign, backup: &owned, want: "Podman"},
				{name: "missing container owner", owner: new(string), want: "Podman"},
				{name: "foreign volume", owner: &owned, volumes: map[string]string{"home": foreign}, backup: &owned, want: "Podman"},
				{name: "missing volume owner", owner: &owned, volumes: map[string]string{"workspace": ""}, want: "Podman"},
				{name: "backup", owner: &owned, backup: &owned, want: "update agent01"},
			} {
				t.Run(host.name+"/"+strings.Join(args, " ")+"/"+test.name, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					responses := sandboxObjectResponses(test.owner, false, test.volumes, test.backup)
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status == 0 || stdout != "" || !strings.Contains(stderr, test.want) || strings.Contains(stderr, "start agent01") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
						t.Fatal("refusal changed files")
					}
					assertNoSSH(t, fakes)
					for _, call := range fakes.Calls("podman") {
						if slices.Contains(call.Args, "exec") || slices.Contains(call.Args, "start") {
							t.Fatalf("refusal executed or started: %v", call.Args)
						}
					}
				})
			}
		}
	}
}

func TestSSHUsageErrorsRunBeforePodmanOrHostWrites(t *testing.T) {
	for _, args := range [][]string{{"ssh-config"}, {"ssh-config", "invalid/name", "--install"}, {"ssh-config", "agent01", "--install", "--install"}, {"ssh-config", "agent01", "--unknown"}, {"ssh-config", "agent01", "extra"}, {"ssh-config", "agent01", "--remove"}, {"start", "agent01", "--ssh-config", "--ssh-config"}, {"up", "agent01", "--ssh-config", "--ssh-config"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, true)
			stdout, stderr, status := runCLI(t, fixture, args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("usage error called Podman")
			}
			assertNoSSH(t, fakes)
			if sshDirectoryContents(t, state) != nil || sshDirectoryContents(t, sshDir) != nil {
				t.Fatal("usage error wrote files")
			}
		})
	}
}

func TestUpSSHManagerCheckPrecedesOptionConflictAndNamesRetry(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			owned := "default"
			responses := append(upObjectResponses(&owned, true, nil, nil), testutil.Response{ExitCode: 1})
			scriptResourceLimitObjects(t, fakes, host.windows, responses, map[string]string{"memory": "8589934592", "ssh-port": "2222"})
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01", "--ssh-config", "--memory", "12g")
			if status == 0 || !strings.Contains(stderr, "manager does not answer") || !strings.Contains(stderr, "ssh-config agent01 --install") || strings.Contains(stderr, "conflict") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if sshDirectoryContents(t, state) != nil || sshDirectoryContents(t, sshDir) != nil {
				t.Fatal("manager failure wrote files")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestStopAndStartPreserveInstalledSSHSetupWithoutAuthorizingAgain(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
			scriptSSHDefaults(fakes, "agent01")
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
			if status != 0 {
				t.Fatalf("initial status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
			port := unusedSSHPort(t)
			owned := "default"
			stopResponses := append(upObjectResponses(&owned, true, nil, nil), testutil.Response{Stdout: "[]\n"}, testutil.Response{})
			scriptLifecycleObjects(t, fakes, host.windows, stopResponses, map[string]string{"ssh-port": strconv.Itoa(port)})
			stdout, stderr, status = runCLI(t, fixture, "stop", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("stop status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			startResponses := append(upObjectResponses(&owned, false, nil, nil), testutil.Response{})
			scriptLifecycleObjects(t, fakes, host.windows, startResponses, map[string]string{"ssh-port": strconv.Itoa(port)})
			stdout, stderr, status = runCLI(t, fixture, "start", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("start status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
				t.Fatal("lifecycle changed SSH files")
			}
			count := 0
			for _, call := range fakes.Calls("podman") {
				if slices.Contains(call.Args, "authorize") {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("authorization calls=%d", count)
			}
			if len(fakes.Calls("ssh-keygen")) != 1 {
				t.Fatal("lifecycle regenerated key")
			}
		})
	}
}

func TestStartAndUpRefuseChangedPinAndKeepInstalledFiles(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"start", "up"} {
			t.Run(host.name+"/"+command, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
				scriptSSHDefaults(fakes, "agent01")
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--install")
				if status != 0 {
					t.Fatalf("initial status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				beforeState, beforeSSH := sshDirectoryContents(t, state), sshDirectoryContents(t, sshDir)
				owned := "default"
				responses := upObjectResponses(&owned, true, nil, nil)
				if command == "up" {
					responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"})
				}
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshChangedHostKey + "\n"})
				if command == "up" {
					scriptResourceLimitObjects(t, fakes, host.windows, responses, map[string]string{"ssh-port": "2222"})
				} else {
					scriptLifecycleObjects(t, fakes, host.windows, responses, map[string]string{"ssh-port": "2222"})
				}
				stdout, stderr, status = runCLI(t, fixture, command, "agent01", "--ssh-config")
				if status == 0 || !strings.Contains(stderr, "differs from the pinned") || !strings.Contains(stderr, "ssh-config agent01 --remove") || !strings.Contains(stderr, "ssh-config agent01 --install") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if !reflect.DeepEqual(beforeState, sshDirectoryContents(t, state)) || !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) {
					t.Fatal("mismatch changed files")
				}
				for _, call := range fakes.Calls("podman") {
					if slices.Contains(call.Args, "stop") || slices.Contains(call.Args, "rm") {
						t.Fatalf("mismatch stopped or removed sandbox: %v", call.Args)
					}
				}
			})
		}
	}
}

func TestSSHConfigPrintsQualifiedUninstalledHostWithoutCreatingGroupState(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
			scriptSSHInstall(t, fakes, host.windows, "agent01.extra", "live", false)
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01.extra")
			if status != 0 || !strings.HasPrefix(stdout, "Host agent01.extra.live\n") || !strings.Contains(stdout, "group-live") || !strings.Contains(stderr, "ssh-config agent01.extra --install") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if sshDirectoryContents(t, state) != nil || sshDirectoryContents(t, sshDir) != nil {
				t.Fatal("print created files")
			}
			assertNoSSH(t, fakes)
		})
	}
}
