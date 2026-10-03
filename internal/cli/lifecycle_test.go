package cli_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestLifecycleKeepsConfigurationAndHonorsTheRequestedState(t *testing.T) {
	for _, test := range []struct {
		command string
		running bool
		changes []string
	}{
		{"start", false, []string{"start"}},
		{"start", true, nil},
		{"stop", false, nil},
		{"stop", true, []string{"stop"}},
		{"restart", false, []string{"start"}},
		{"restart", true, []string{"stop", "start"}},
	} {
		forces := []bool{false}
		if test.command != "start" {
			forces = append(forces, true)
		}
		for _, force := range forces {
			t.Run(fmt.Sprintf("%s/running-%t/force-%t", test.command, test.running, force), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, test.running, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
				query := test.running && test.command != "start"
				if query {
					responses = append(responses, testutil.Response{Stdout: "[]\n"})
				}
				responses = append(responses, make([]testutil.Response, len(test.changes))...)
				fakes.Script("podman", responses...)
				args := []string{test.command, "agent01"}
				if force {
					args = append(args, "--force")
				}
				stdout, stderr, status := runCLI(t, "production", args...)
				state := "running"
				if test.command == "stop" {
					state = "stopped"
				}
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is "+state) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				want := []testutil.Call{
					{Args: []string{"container", "exists", "sandboxed-agents.default.agent01"}},
					{Args: []string{"container", "inspect", "sandboxed-agents.default.agent01"}},
					{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.workspace"}},
					{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.workspace"}},
					{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.home"}},
					{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.home"}},
					{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.ssh"}},
					{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.ssh"}},
					{Args: []string{"container", "exists", "sandboxed-agents-backup.default.agent01"}},
				}
				if query {
					want = append(want, testutil.Call{Args: sessionQueryArgs()})
				}
				for _, change := range test.changes {
					want = append(want, testutil.Call{Args: []string{change, "sandboxed-agents.default.agent01"}})
				}
				if calls := fakes.Calls("podman"); !reflect.DeepEqual(calls, want) {
					t.Fatalf("calls=%v want=%v", calls, want)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func sessionQueryArgs() []string {
	return []string{"exec", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "sessions", "list"}
}

func TestStopAndRestartProtectRunningAgentSessions(t *testing.T) {
	for _, command := range []string{"stop", "restart"} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force-%t", command, force), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
				responses = append(responses, testutil.Response{Stdout: `[{"name":"coding","agent":"codex"},{"name":"review","agent":"claude"}]`})
				fakes.Script("podman", append(responses, testutil.Response{}, testutil.Response{})...)
				args := []string{command, "agent01"}
				if force {
					args = append(args, "--force")
				}
				stdout, stderr, status := runCLI(t, "production", args...)
				output := stderr
				if force {
					output = stdout
					if status != 0 || stderr != "" || !strings.Contains(stdout, "Ended agent sessions") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
				} else if status == 0 || !strings.Contains(stderr, "--force") {
					t.Fatalf("status=%d stderr=%q", status, stderr)
				}
				for _, session := range []string{"coding (codex)", "review (claude)"} {
					if !strings.Contains(output, session) {
						t.Fatalf("missing session %s: %q", session, output)
					}
				}
				calls := fakes.Calls("podman")
				queryIndex := len(responses) - 1
				if len(calls) <= queryIndex || !reflect.DeepEqual(calls[queryIndex].Args, sessionQueryArgs()) {
					t.Fatalf("manager was not queried: %v", calls)
				}
				if !force && len(calls) != len(responses) {
					t.Fatalf("refused command changed state: %v", calls)
				}
				if force {
					want := stopOrRestartCalls(command)
					if !reflect.DeepEqual(calls[queryIndex+1:], want) {
						t.Fatalf("calls=%v want mutations=%v", calls, want)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestStopAndRestartRefuseWhenSessionsCannotBeDeterminedUnlessForced(t *testing.T) {
	for _, command := range []string{"stop", "restart"} {
		for _, answer := range []testutil.Response{
			{ExitCode: 125, Stderr: "manager unavailable"},
			{Stdout: ""}, {Stdout: "not JSON"}, {Stdout: "null"}, {Stdout: "{}"},
			{Stdout: `[{}]`}, {Stdout: `[{"name":"coding"}]`},
			{Stdout: `[{"name":"coding","agent":"codex"},{}]`},
			{Stdout: `[{"name":" ","agent":"codex"}]`},
			{Stdout: `[{"name":"coding","agent":42}]`},
			{Stdout: "[] trailing"},
		} {
			for _, force := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%q/force-%t", command, answer.Stdout, force), func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					owned := "default"
					responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
					responses = append(responses, answer)
					fakes.Script("podman", append(responses, testutil.Response{}, testutil.Response{})...)
					args := []string{command, "agent01"}
					if force {
						args = append(args, "--force")
					}
					stdout, stderr, status := runCLI(t, "production", args...)
					calls := fakes.Calls("podman")
					if !reflect.DeepEqual(calls[len(responses)-1].Args, sessionQueryArgs()) {
						t.Fatalf("missing manager query: %v", calls)
					}
					if force {
						want := stopOrRestartCalls(command)
						if status != 0 || stderr != "" || !strings.Contains(stdout, "may have been running were ended and cannot be named") || !reflect.DeepEqual(calls[len(responses):], want) || strings.Contains(stdout, "Ended agent sessions:") {
							t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
						}
					} else if status == 0 || len(calls) != len(responses) || !strings.Contains(stderr, "cannot rule out running agent sessions") || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "--force") {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestLifecycleNamesUnknownSandboxesAndAdoptableKeptVolumes(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart"} {
		for mask := 0; mask < 8; mask++ {
			t.Run(fmt.Sprintf("%s/kept-%03b", command, mask), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				volumes := map[string]string{}
				for index, role := range sandboxVolumeRoles {
					if mask&(1<<index) != 0 {
						volumes[role] = "default"
					}
				}
				fakes.Script("podman", sandboxObjectResponses(nil, false, volumes, nil)...)
				stdout, stderr, status := runCLI(t, "production", command, "agent01")
				if status == 0 || stdout != "" || !strings.Contains(stderr, "agent01") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if mask == 0 {
					if !strings.Contains(stderr, "does not exist") || strings.Contains(stderr, "up agent01") {
						t.Fatalf("stderr=%q", stderr)
					}
				} else if !strings.Contains(stderr, "no container") || !strings.Contains(stderr, "up agent01") {
					t.Fatalf("stderr=%q", stderr)
				}
				assertLifecycleReadOnly(t, fakes)
			})
		}
	}
}

func TestLifecycleRefusesForeignObjectsBeforeUpdateAndSessions(t *testing.T) {
	owned := "default"
	for _, command := range []string{"start", "stop", "restart"} {
		for _, running := range []bool{false, true} {
			for _, owner := range []string{"", "missing", "another-group"} {
				for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
					for _, withContainer := range []bool{false, true} {
						if !withContainer && (object == "container" || object == "backup") {
							continue
						}
						t.Run(fmt.Sprintf("%s/running-%t/%s/%s/container-%t", command, running, owner, object, withContainer), func(t *testing.T) {
							fakes := testutil.NewFakePrograms(t)
							var containerOwner *string
							if withContainer {
								containerOwner = &owned
							}
							volumes := map[string]string{"workspace": "default", "home": "default", "ssh": "default"}
							backupOwner := &owned
							name := "sandboxed-agents.default.agent01"
							switch object {
							case "container":
								containerOwner = &owner
							case "backup":
								backupOwner = &owner
								name = "sandboxed-agents-backup.default.agent01"
							default:
								volumes[object] = owner
								name += "." + object
							}
							responses := sandboxObjectResponses(containerOwner, running, volumes, backupOwner)
							if owner == "missing" {
								for index := range responses {
									responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `{"io.github.sandboxed-agents.owner":"missing"}`, `{}`)
								}
							}
							fakes.Script("podman", append(responses, testutil.Response{Stdout: `[{"name":"coding","agent":"codex"}]`})...)
							args := []string{command, "agent01"}
							if command != "start" {
								args = append(args, "--force")
							}
							stdout, stderr, status := runCLI(t, "production", args...)
							if status == 0 || stdout != "" || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, name) || !strings.Contains(stderr, "Podman") || !strings.Contains(stderr, "remove or rename") || strings.Contains(stderr, "up agent01") || strings.Contains(stderr, "interrupted update") || strings.Contains(stderr, "sessions") {
								t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
							}
							assertLifecycleReadOnly(t, fakes)
						})
					}
				}
			}
		}
	}
}

func TestLifecycleRefusesInterruptedUpdatesBeforeQueryingSessions(t *testing.T) {
	owned := "default"
	for _, command := range []string{"start", "stop", "restart"} {
		for _, running := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/running-%t", command, running), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := sandboxObjectResponses(&owned, running, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, &owned)
				fakes.Script("podman", append(responses, testutil.Response{Stdout: `[{"name":"coding","agent":"codex"}]`})...)
				stdout, stderr, status := runCLI(t, "production", command, "agent01")
				if status == 0 || stdout != "" || !strings.Contains(stderr, "sandboxed-agents-backup.default.agent01") || !strings.Contains(stderr, "interrupted update") || !strings.Contains(stderr, "update agent01") || strings.Contains(stderr, "sessions") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertLifecycleReadOnly(t, fakes)
			})
		}
	}
}

func TestStopAndRestartReportAnOwnerConflictBeforeRunningSessionsWithoutForce(t *testing.T) {
	for _, command := range []string{"stop", "restart"} {
		t.Run(command, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "other", "ssh": "default"}, nil)
			fakes.Script("podman", append(responses, testutil.Response{Stdout: `[{"name":"coding","agent":"codex"}]`})...)
			stdout, stderr, status := runCLI(t, "production", command, "agent01")
			if status == 0 || stdout != "" || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, "sandboxed-agents.default.agent01.home") || strings.Contains(stderr, "sessions") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertLifecycleReadOnly(t, fakes)
		})
	}
}

func TestLifecycleRejectsUsageErrorsBeforePodman(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart"} {
		forms := [][]string{nil, {""}, {"--force"}, {"-x"}, {".x"}, {"a/b"}, {"a b"}, {"agent01", "extra"}, {"agent01", "--unknown"}, {"agent01", "--force", "--force"}}
		if command == "start" {
			forms = append(forms, []string{"agent01", "--force"})
		}
		for _, args := range forms {
			t.Run(command+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, "production", append([]string{command}, args...)...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
			})
		}
	}
}

func TestLifecycleStopsAfterPodmanFailureWithoutClaimingSuccess(t *testing.T) {
	for _, test := range []struct {
		command   string
		running   bool
		mutations []testutil.Response
		last      string
	}{
		{"start", false, []testutil.Response{{ExitCode: 125, Stderr: "start failed"}}, "start"},
		{"stop", true, []testutil.Response{{ExitCode: 125, Stderr: "stop failed"}}, "stop"},
		{"restart", true, []testutil.Response{{ExitCode: 125, Stderr: "stop failed"}}, "stop"},
		{"restart", true, []testutil.Response{{}, {ExitCode: 125, Stderr: "start failed"}}, "start"},
	} {
		t.Run(test.command+"/"+test.last, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, test.running, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
			if test.running {
				responses = append(responses, testutil.Response{Stdout: `[{"name":"coding","agent":"codex"}]`})
			}
			responses = append(responses, test.mutations...)
			fakes.Script("podman", responses...)
			args := []string{test.command, "agent01"}
			if test.command != "start" {
				args = append(args, "--force")
			}
			stdout, stderr, status := runCLI(t, "production", args...)
			calls := fakes.Calls("podman")
			if status == 0 || !strings.Contains(stderr, "exit status 125") || strings.Contains(stdout, "Sandbox agent01 is") || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, []string{test.last, "sandboxed-agents.default.agent01"}) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
			if strings.Contains(stdout, "Ended agent sessions") != (test.last == "start" && test.running) {
				t.Fatalf("incorrect ended-session report: %q", stdout)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestLifecycleRefusesFailedOrUnusablePodmanLookups(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart"} {
		for _, test := range []struct {
			index    int
			response testutil.Response
			message  string
		}{
			{0, testutil.Response{ExitCode: 125, Stderr: "engine unavailable"}, "engine unavailable"},
			{1, testutil.Response{Stdout: "not JSON"}, "decode podman container inspect"},
			{1, testutil.Response{Stdout: "[]"}, "invalid podman container inspect"},
			{1, testutil.Response{Stdout: `[{"Name":"foreign","Config":{"Labels":{}},"State":{"Running":true}}]`}, "invalid podman container inspect"},
			{2, testutil.Response{ExitCode: 125, Stderr: "volume unavailable"}, "volume unavailable"},
			{3, testutil.Response{Stdout: "null"}, "invalid podman volume inspect"},
			{8, testutil.Response{ExitCode: 125, Stderr: "backup unavailable"}, "backup unavailable"},
		} {
			t.Run(fmt.Sprintf("%s/lookup-%d/%s", command, test.index, test.message), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
				responses[test.index] = test.response
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "production", command, "agent01")
				if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) || len(fakes.Calls("podman")) != test.index+1 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertLifecycleReadOnly(t, fakes)
			})
		}
	}
}

func TestStartResumesTheSameSandboxWithoutChangingConfiguration(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, false, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
	fakes.Script("podman", append(responses, testutil.Response{})...)
	stdout, stderr, status := runCLI(t, "production", "start", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses)+1 || !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
		t.Fatalf("calls=%v", calls)
	}
	assertNoSSH(t, fakes)
}

func assertLifecycleReadOnly(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	for _, call := range fakes.Calls("podman") {
		if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
			t.Fatalf("calls=%v", call)
		}
	}
	assertNoSSH(t, fakes)
}

func stopOrRestartCalls(command string) []testutil.Call {
	calls := []testutil.Call{{Args: []string{"stop", "sandboxed-agents.default.agent01"}}}
	if command == "restart" {
		calls = append(calls, testutil.Call{Args: []string{"start", "sandboxed-agents.default.agent01"}})
	}
	return calls
}
