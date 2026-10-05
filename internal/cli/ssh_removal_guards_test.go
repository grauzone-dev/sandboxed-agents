package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestSSHRemovalGuardsPreserveInstalledHostState(t *testing.T) {
	owned, foreign, missing := "default", "other", ""
	type refusal struct {
		name, want string
		container  *string
		running    bool
		volumes    map[string]string
		backup     *string
	}
	cases := []refusal{
		{name: "unknown", want: "controller group"},
		{name: "foreign container before backup", container: &foreign, backup: &owned, want: "owner conflict"},
		{name: "missing container owner", container: &missing, want: "owner conflict"},
		{name: "interrupted running", container: &owned, running: true, backup: &owned, want: "interrupted update"},
		{name: "interrupted stopped", container: &owned, backup: &owned, want: "interrupted update"},
		{name: "backup only", backup: &owned, want: "interrupted update"},
		{name: "foreign backup", container: &owned, backup: &foreign, want: "owner conflict"},
		{name: "missing backup owner", container: &owned, backup: &missing, want: "owner conflict"},
		{name: "foreign backup only", backup: &foreign, want: "owner conflict"},
		{name: "missing backup owner only", backup: &missing, want: "owner conflict"},
	}
	for _, role := range sandboxVolumeRoles {
		for _, owner := range []string{foreign, missing} {
			for _, container := range []*string{&owned, nil} {
				cases = append(cases, refusal{
					name:      fmt.Sprintf("volume-%s/owner-%q/container-%t", role, owner, container != nil),
					container: container, volumes: map[string]string{role: owner}, want: "owner conflict",
				})
			}
			cases = append(cases, refusal{
				name:      fmt.Sprintf("volume-%s/owner-%q/before-backup", role, owner),
				container: &owned, volumes: map[string]string{role: owner}, backup: &owned, want: "owner conflict",
			})
		}
	}
	for _, host := range resourceLimitHosts {
		for _, args := range [][]string{{"ssh-config", "agent01", "--remove"}, {"remove", "agent01"}, {"remove", "agent01", "--volumes"}} {
			for _, test := range cases {
				if args[0] == "remove" && len(args) == 3 && test.container == &owned && test.backup == nil && len(test.volumes) != 0 {
					continue
				}
				t.Run(host.name+"/"+strings.Join(args, " ")+"/"+test.name, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					sshBefore, stateBefore := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
					beforeCalls := len(fakes.Calls("podman"))
					responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if test.want == "owner conflict" && strings.Contains(stderr, "interrupted update") {
						t.Fatalf("backup interruption preceded owner conflict: %q", stderr)
					}
					if !reflect.DeepEqual(sshBefore, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(stateBefore, managedSSHFiles(t, state)) {
						t.Fatal("refusal changed host SSH files")
					}
					assertSSHRemovalObjectQueries(t, fakes.Calls("podman")[beforeCalls:], host.windows, false)
				})
			}
		}
	}
}

func TestRemoveSessionGuardsPreserveInstalledSSHState(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, volumes := range []bool{false, true} {
			for _, test := range []struct {
				name, want string
				response   testutil.Response
			}{
				{name: "manager unavailable", want: "manager did not answer", response: testutil.Response{ExitCode: 1}},
				{name: "sessions", want: "agent sessions are running", response: testutil.Response{Stdout: `[{"name":"work","agent":"codex"}]`}},
			} {
				t.Run(fmt.Sprintf("%s/volumes-%t/%s", host.name, volumes, test.name), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					sshBefore, stateBefore := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
					beforeCalls := len(fakes.Calls("podman"))
					owned := "default"
					responses := append(sandboxObjectResponses(&owned, true, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil), test.response)
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					args := []string{"remove", "agent01"}
					if volumes {
						args = append(args, "--volumes")
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if !reflect.DeepEqual(sshBefore, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(stateBefore, managedSSHFiles(t, state)) {
						t.Fatal("session refusal changed host SSH files")
					}
					assertSSHRemovalObjectQueries(t, fakes.Calls("podman")[beforeCalls:], host.windows, true)
				})
			}
		}
	}
}

func assertSSHRemovalObjectQueries(t *testing.T, calls []testutil.Call, windows, sessions bool) {
	t.Helper()
	if windows {
		calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
	}
	queries := 0
	for _, call := range calls {
		if len(call.Args) == 3 && (call.Args[0] == "container" || call.Args[0] == "volume") && (call.Args[1] == "exists" || call.Args[1] == "inspect") {
			continue
		}
		if sessions && reflect.DeepEqual(call.Args, sessionQueryArgs()) {
			queries++
			continue
		}
		t.Fatalf("refused removal changed sandbox state: %v", call.Args)
	}
	if sessions && queries != 1 {
		t.Fatalf("session queries=%d, want 1", queries)
	}
}

func TestRemovalWithoutSSHSetupPreservesOtherSetupAndPersonalSSHFiles(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{false, true} {
			for _, args := range [][]string{{"ssh-config", "agent01", "--remove"}, {"remove", "agent01"}, {"remove", "agent01", "--volumes"}} {
				t.Run(fmt.Sprintf("%s/running-%t/%s", host.name, running, strings.Join(args, " ")), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent02", "default")
					for name, contents := range map[string]string{"personal_key": "personal private key\n", "known_hosts": "personal host key\r\n"} {
						if err := os.WriteFile(filepath.Join(sshDir, name), []byte(contents), 0600); err != nil {
							t.Fatal(err)
						}
					}
					sshBefore, stateBefore := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
					beforeCalls := len(fakes.Calls("podman"))
					owned := "default"
					responses := sandboxObjectResponses(&owned, running, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
					if args[0] == "remove" {
						if running {
							responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
						}
						responses = append(responses, testutil.Response{})
						if len(args) == 3 {
							responses = append(responses, make([]testutil.Response, 3)...)
						}
					} else {
						responses = append(responses, testutil.Response{ExitCode: 125})
					}
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if !reflect.DeepEqual(sshBefore, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(stateBefore, managedSSHFiles(t, state)) {
						t.Fatal("removing absent SSH setup changed host SSH files")
					}
					if args[0] == "ssh-config" {
						assertSSHRemovalObjectQueries(t, fakes.Calls("podman")[beforeCalls:], host.windows, false)
					}
				})
			}
		}
	}
}

func TestSSHRemovalFromStoppedSandboxThenStartAndInstallReauthorizes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, _, state := sshSetupHost(t, host.windows)
			installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", false)
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "remains authorized") {
				t.Fatalf("remove: status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")); !os.IsNotExist(err) {
				t.Fatalf("removed SSH setup remains: %v", err)
			}
			owned := "default"
			responses := append(sandboxObjectResponses(&owned, false, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil), testutil.Response{})
			if host.windows {
				responses = append(healthyWindowsPodman()[1:3], responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status = runCLI(t, fixture, "start", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("start: status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			beforeCalls := len(fakes.Calls("podman"))
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshChangedHostKey + "\n"})
			scriptSSHDefaults(fakes, "agent01")
			fakes.Script("ssh-keygen", testutil.Response{GenerateSSHKey: sshChangedHostKey + " new key\n"})
			stdout, stderr, status = runCLI(t, fixture, "ssh-config", "agent01", "--install")
			if status != 0 || stderr != "" {
				t.Fatalf("reinstall: status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")[beforeCalls:]
			if host.windows {
				calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
			}
			var authorize [][]string
			for _, call := range calls {
				if len(call.Args) != 0 && call.Args[len(call.Args)-1] == "authorize" {
					authorize = append(authorize, call.Args)
				}
			}
			want := [][]string{{"exec", "--interactive", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "ssh", "authorize"}}
			if !reflect.DeepEqual(authorize, want) {
				t.Fatalf("authorize=%v want=%v", authorize, want)
			}
			publicKey, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031", "id_ed25519.pub"))
			if err != nil || string(publicKey) != sshChangedHostKey+"\n" {
				t.Fatalf("reinstalled public key=%q error=%v", publicKey, err)
			}
		})
	}
}
