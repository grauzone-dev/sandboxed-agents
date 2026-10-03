package cli_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestShellPassesInputOutputAndExitStatusWithoutATerminal(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, code := range []int{0, 7, 125} {
			t.Run(fmt.Sprintf("%s/status-%d", fixture, code), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				input := "printf 'hello\\n'\nprintf 'diagnostic\\n' >&2\nexit 7\n"
				responses := shellResponses(fixture)
				fakes.Script("podman", append(responses, testutil.Response{WantStdin: input, Stdout: "hello\n", Stderr: "diagnostic\n", ExitCode: code})...)
				stdout, stderr, status := runCLIWithInput(t, "", fixture, strings.NewReader(input), "shell", "agent01")
				if status != code || stdout != "hello\n" || stderr != "diagnostic\n" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				want := []string{"exec", "--interactive", "--user=1000:1000", "--workdir=/workspace", "sandboxed-agents.default.agent01", "/bin/bash"}
				if len(calls) != 10 || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatalf("calls=%v want exec=%v", calls, want)
				}
			})
		}
	}
}

func TestShellUsesATerminalAndPreservesExitStatus(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, code := range []int{0, 7} {
			t.Run(fmt.Sprintf("%s/status-%d", fixture, code), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				fakes.Script("podman", append(shellResponses(fixture), testutil.Response{Stdout: "interactive output\n", Stderr: "interactive error\n", ExitCode: code})...)
				stdout, stderr, status := runCLIWithInput(t, "", fixture, shellTerminal(t), "shell", "agent01")
				if status != code || stdout != "interactive output\n" || stderr != "interactive error\n" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				want := []string{"exec", "--interactive", "--tty", "--user=1000:1000", "--workdir=/workspace", "sandboxed-agents.default.agent01", "/bin/bash"}
				if len(calls) != 10 || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatalf("calls=%v want exec=%v", calls, want)
				}
			})
		}
	}
}

func TestShellRefusesBeforeOpeningAndReportsTheFirstFailure(t *testing.T) {
	owned, foreign, missing := "default", "other", ""
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name, message string
			container     *string
			running       bool
			volumes       map[string]string
			backup        *string
			absent        []string
		}{
			{name: "unknown", message: "does not exist in this controller group"},
			{name: "stopped", container: &owned, message: "start agent01"},
			{name: "foreign container", container: &foreign, message: "owner conflict on sandboxed-agents.default.agent01", absent: []string{"start agent01"}},
			{name: "unlabelled container", container: &missing, message: "owner conflict on sandboxed-agents.default.agent01", absent: []string{"start agent01"}},
			{name: "foreign workspace", container: &owned, volumes: map[string]string{"workspace": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.workspace", absent: []string{"start agent01"}},
			{name: "unlabelled home", container: &owned, volumes: map[string]string{"home": missing}, message: "owner conflict on sandboxed-agents.default.agent01.home", absent: []string{"start agent01"}},
			{name: "foreign SSH volume", container: &owned, running: true, volumes: map[string]string{"ssh": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.ssh"},
			{name: "interrupted running", container: &owned, running: true, backup: &owned, message: "update agent01", absent: []string{"start agent01"}},
			{name: "interrupted stopped", container: &owned, backup: &owned, message: "update agent01", absent: []string{"start agent01"}},
			{name: "backup only", backup: &owned, message: "update agent01"},
			{name: "foreign backup", container: &owned, backup: &foreign, message: "owner conflict on sandboxed-agents-backup.default.agent01", absent: []string{"update agent01", "start agent01"}},
			{name: "conflict before interrupted", container: &owned, volumes: map[string]string{"home": foreign}, backup: &owned, message: "owner conflict on sandboxed-agents.default.agent01.home", absent: []string{"update agent01", "start agent01"}},
			{name: "all volumes only", volumes: map[string]string{"workspace": owned, "home": owned, "ssh": owned}, message: "up agent01"},
			{name: "partial volumes only", volumes: map[string]string{"home": owned}, message: "up agent01"},
			{name: "foreign volumes only", volumes: map[string]string{"workspace": foreign, "home": missing}, message: "owner conflict on sandboxed-agents.default.agent01.workspace, sandboxed-agents.default.agent01.home", absent: []string{"up agent01"}},
			{name: "foreign backup only", backup: &foreign, message: "owner conflict on sandboxed-agents-backup.default.agent01", absent: []string{"update agent01"}},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3:3], responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "shell", "agent01")
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, absent := range test.absent {
					if strings.Contains(stderr, absent) {
						t.Fatalf("reported a later failure %q: %q", absent, stderr)
					}
				}
				if strings.Contains(test.message, "owner conflict") && !strings.Contains(stderr, "Podman") {
					t.Fatalf("no Podman repair hint: %q", stderr)
				}
				for _, call := range shellOperationCalls(t, fixture, fakes.Calls("podman")) {
					if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
						t.Fatalf("refused shell opened or changed something: %v", call.Args)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestShellRejectsInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{{"shell"}, {"shell", ".bad"}, {"shell", "--help"}, {"shell", "agent01", "extra"}, {"shell", "agent01", "--force"}} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status != 1 || stdout != "" || !strings.Contains(stderr, "Usage: sandboxed-agents shell") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
			})
		}
	}
}

func TestShellLeavesSSHAndHostStateUntouched(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/populated-%t", fixture, populated), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				root := t.TempDir()
				t.Setenv("HOME", root)
				t.Setenv("USERPROFILE", root)
				t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
				t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
				files := map[string]string{}
				if populated {
					for _, name := range []string{".ssh/config", ".ssh/id_ed25519", ".ssh/known_hosts", "state/sandboxed-agents/group-default/ssh/key", "local/sandboxed-agents/group-default/ssh/config"} {
						path := filepath.Join(root, filepath.FromSlash(name))
						if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
							t.Fatal(err)
						}
						files[name] = "keep " + name + "\x00\r\n"
						if err := os.WriteFile(path, []byte(files[name]), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				fakes.Script("podman", append(shellResponses(fixture), testutil.Response{})...)
				stdout, stderr, status := runCLI(t, fixture, "shell", "agent01")
				if status != 0 || stdout != "" || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				after := map[string]string{}
				if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
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
					name, err := filepath.Rel(root, path)
					after[filepath.ToSlash(name)] = string(data)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(files, after) {
					t.Fatalf("host files changed: before=%v after=%v", files, after)
				}
				if !populated {
					entries, err := os.ReadDir(root)
					if err != nil || len(entries) != 0 {
						t.Fatalf("created host state: entries=%v err=%v", entries, err)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestShellOpensOnlyInTheSelectedControllerGroup(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			fakes := testutil.NewFakePrograms(t)
			responses := shellResponses(fixture)
			for index := range responses {
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, ".default.", ".team-a.")
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"default"`, `"team-a"`)
			}
			fakes.Script("podman", append(responses, testutil.Response{})...)
			stdout, stderr, status := runCLI(t, fixture, "shell", "agent01")
			if status != 0 || stdout != "" || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
			for _, call := range calls {
				if !strings.Contains(strings.Join(call.Args, " "), ".team-a.agent01") {
					t.Fatalf("another group accessed: %v", call.Args)
				}
			}
			want := []string{"exec", "--interactive", "--user=1000:1000", "--workdir=/workspace", "sandboxed-agents.team-a.agent01", "/bin/bash"}
			if !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("exec=%v want=%v", calls[len(calls)-1].Args, want)
			}
		})
	}
}

func TestShellOpensAfterUpWithoutSSHSetup(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/terminal-%t", fixture, terminal), func(t *testing.T) {
				var fakes *testutil.FakePrograms
				if fixture == "linux-preflight" {
					fakes = linuxHost(t)
				} else {
					fakes = testutil.NewFakePrograms(t)
				}
				responses := upObjectResponses(nil, false, nil, nil)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman(), responses[1:]...)
				}
				responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "up", "agent01")
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
					t.Fatalf("up status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				before := len(fakes.Calls("podman"))
				shellFixture := fixture
				if fixture == "linux-preflight" {
					shellFixture = "sandbox-host"
				}
				fakes.Script("podman", append(shellResponses(shellFixture), testutil.Response{})...)
				var input io.Reader
				if terminal {
					input = shellTerminal(t)
				}
				stdout, stderr, status = runCLIWithInput(t, "", shellFixture, input, "shell", "agent01")
				if status != 0 || stdout != "" || stderr != "" {
					t.Fatalf("shell status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := shellOperationCalls(t, shellFixture, fakes.Calls("podman")[before:])
				if len(calls) != 10 || calls[len(calls)-1].Args[0] != "exec" || slices.Contains(calls[len(calls)-1].Args, "--tty") != terminal {
					t.Fatalf("shell calls=%v", calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func shellResponses(fixture string) []testutil.Response {
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
	if fixture == "windows" {
		responses = append(healthyWindowsPodman()[1:3:3], responses...)
	}
	return responses
}

func shellOperationCalls(t *testing.T, fixture string, calls []testutil.Call) []testutil.Call {
	t.Helper()
	if fixture == "windows" {
		if len(calls) < 2 {
			t.Fatalf("missing Windows selection: %v", calls)
		}
		return windowsOperationCalls(t, calls[2:], "podman-machine-default")
	}
	return calls
}
