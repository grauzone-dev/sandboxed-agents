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

func removeObjectResponses(containerOwner *string, running bool, volumes map[string]string, backupOwner *string) []testutil.Response {
	return upObjectResponses(containerOwner, running, volumes, backupOwner)[1:]
}

func TestRemoveKeepsVolumesOfAStoppedSandbox(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	fakes.Script("podman", append(removeObjectResponses(&owned, false, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil), testutil.Response{})...)
	stdout, stderr, status := runCLI(t, "production", "remove", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "agent01") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"rm", "sandboxed-agents.default.agent01"}) {
		t.Fatalf("calls=%v", calls)
	}
	for _, call := range calls[:len(calls)-1] {
		if call.Args[1] != "exists" && call.Args[1] != "inspect" {
			t.Fatalf("unexpected mutation: %v", call)
		}
	}
	assertUpNoSSH(t, fakes)
}

func TestRemoveDeletesOnlyOwnedVolumesWhenAsked(t *testing.T) {
	for _, foreignOwner := range []string{"default", "", "other"} {
		t.Run("home-owner-"+foreignOwner, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := removeObjectResponses(&owned, false, map[string]string{"workspace": owned, "home": foreignOwner, "ssh": owned}, nil)
			mutations := 3
			if foreignOwner == owned {
				mutations++
			}
			fakes.Script("podman", append(responses, make([]testutil.Response, mutations)...)...)
			stdout, stderr, status := runCLI(t, "production", "remove", "agent01", "--volumes")
			if (status == 0) != (foreignOwner == owned) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			want := []testutil.Call{{Args: []string{"rm", "sandboxed-agents.default.agent01"}}, {Args: []string{"volume", "rm", "sandboxed-agents.default.agent01.workspace"}}}
			if foreignOwner == owned {
				want = append(want, testutil.Call{Args: []string{"volume", "rm", "sandboxed-agents.default.agent01.home"}})
			} else if !strings.Contains(stdout, "Kept volume sandboxed-agents.default.agent01.home") || !strings.Contains(stdout, "Podman") {
				t.Fatalf("stdout=%q", stdout)
			}
			want = append(want, testutil.Call{Args: []string{"volume", "rm", "sandboxed-agents.default.agent01.ssh"}})
			if got := removeMutations(t, fakes); !reflect.DeepEqual(got, want) {
				t.Fatalf("mutations=%v want=%v", got, want)
			}
			assertUpNoSSH(t, fakes)
		})
	}
}

func TestRemoveHandlesEverySubsetOfKeptVolumes(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		for _, deleteVolumes := range []bool{false, true} {
			t.Run(fmt.Sprintf("volumes-%03b/delete-%t", mask, deleteVolumes), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				volumes := map[string]string{}
				var want []testutil.Call
				for index, role := range sandboxVolumeRoles {
					if mask&(1<<index) != 0 {
						volumes[role] = "default"
						if deleteVolumes {
							want = append(want, testutil.Call{Args: []string{"volume", "rm", "sandboxed-agents.default.agent01." + role}})
						}
					}
				}
				responses := removeObjectResponses(nil, false, volumes, nil)
				fakes.Script("podman", append(responses, make([]testutil.Response, len(want))...)...)
				args := []string{"remove", "agent01"}
				if deleteVolumes {
					args = append(args, "--volumes")
				}
				stdout, stderr, status := runCLI(t, "production", args...)
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if !deleteVolumes && (!strings.Contains(stdout, "no container") || !strings.Contains(stdout, "remove agent01 --volumes")) {
					t.Fatalf("stdout=%q", stdout)
				}
				if deleteVolumes {
					for role := range volumes {
						if !strings.Contains(stdout, "Removed volume sandboxed-agents.default.agent01."+role) {
							t.Fatalf("stdout=%q", stdout)
						}
					}
				}
				if got := removeMutations(t, fakes); !reflect.DeepEqual(got, want) {
					t.Fatalf("mutations=%v want=%v", got, want)
				}
				assertUpNoSSH(t, fakes)
			})
		}
	}
}

func TestRemoveRefusesForeignObjectsAndInterruptedUpdates(t *testing.T) {
	for _, foreignOwner := range []string{"", "other"} {
		for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
			for _, withContainer := range []bool{false, true} {
				if object == "container" && !withContainer {
					continue
				}
				for _, deleteVolumes := range []bool{false, true} {
					if withContainer && deleteVolumes && object != "container" && object != "backup" {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/container-%t/delete-%t", foreignOwner, object, withContainer, deleteVolumes), func(t *testing.T) {
						fakes := testutil.NewFakePrograms(t)
						owned := "default"
						var containerOwner *string
						if withContainer {
							containerOwner = &owned
						}
						volumes := map[string]string{"workspace": owned, "home": owned, "ssh": owned}
						backupOwner := &owned
						name := "sandboxed-agents.default.agent01"
						switch object {
						case "container":
							containerOwner = &foreignOwner
						case "backup":
							backupOwner = &foreignOwner
							name = "sandboxed-agents-backup.default.agent01"
						default:
							volumes[object] = foreignOwner
							name += "." + object
						}
						fakes.Script("podman", removeObjectResponses(containerOwner, true, volumes, backupOwner)...)
						args := []string{"remove", "agent01", "--force"}
						if deleteVolumes {
							args = append(args, "--volumes")
						}
						stdout, stderr, status := runCLI(t, "production", args...)
						if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, name) || !strings.Contains(stderr, "Podman") || strings.Contains(stderr, "interrupted update") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						assertUpReadOnly(t, fakes)
					})
				}
			}
		}
	}
	for _, withContainer := range []bool{false, true} {
		for _, deleteVolumes := range []bool{false, true} {
			t.Run(fmt.Sprintf("backup/container-%t/delete-%t", withContainer, deleteVolumes), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				var containerOwner *string
				if withContainer {
					containerOwner = &owned
				}
				fakes.Script("podman", removeObjectResponses(containerOwner, true, map[string]string{"workspace": owned}, &owned)...)
				args := []string{"remove", "agent01", "--force"}
				if deleteVolumes {
					args = append(args, "--volumes")
				}
				stdout, stderr, status := runCLI(t, "production", args...)
				if status == 0 || !strings.Contains(stderr, "update agent01") || !strings.Contains(stderr, "interrupted update") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertUpReadOnly(t, fakes)
			})
		}
	}
}

func TestRemoveRejectsUnknownSandboxesAndUsage(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", removeObjectResponses(nil, false, nil, nil)...)
	stdout, stderr, status := runCLI(t, "production", "remove", "agent01", "--volumes", "--force")
	if status == 0 || !strings.Contains(stderr, "agent01") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertUpReadOnly(t, fakes)
	for _, args := range [][]string{{"remove"}, {"remove", "-x"}, {"remove", "a/b"}, {"remove", "agent01", "extra"}, {"remove", "agent01", "--unknown"}, {"remove", "agent01", "--force", "--force"}, {"remove", "agent01", "--volumes", "--volumes"}, {"remove", "agent01", "--volumes=true"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "production", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("usage ran an external program")
			}
		})
	}
}

func removeMutations(t *testing.T, fakes *testutil.FakePrograms) []testutil.Call {
	t.Helper()
	var mutations []testutil.Call
	for _, call := range fakes.Calls("podman") {
		args := call.Args
		if len(args) == 3 && (args[0] == "container" || args[0] == "volume") && (args[1] == "exists" || args[1] == "inspect") {
			continue
		}
		mutations = append(mutations, call)
	}
	return mutations
}

func TestRemoveGuardsRunningSessionsAndStopsBeforeRemoving(t *testing.T) {
	for _, test := range []struct {
		name     string
		response testutil.Response
		sessions bool
		unknown  bool
	}{
		{name: "empty", response: testutil.Response{Stdout: "[]\n"}},
		{name: "sessions", response: testutil.Response{Stdout: `[{"name":"work-1","agent":"codex"},{"name":"work-2","agent":"claude"}]`}, sessions: true},
		{name: "unreachable", response: testutil.Response{ExitCode: 125, Stderr: "manager unavailable"}, unknown: true},
		{name: "invalid-json", response: testutil.Response{Stdout: "invalid"}, unknown: true},
		{name: "null", response: testutil.Response{Stdout: "null"}, unknown: true},
		{name: "missing-name", response: testutil.Response{Stdout: `[{"agent":"codex"}]`}, unknown: true},
		{name: "missing-agent", response: testutil.Response{Stdout: `[{"name":"work-1"}]`}, unknown: true},
	} {
		for _, force := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/force-%t", test.name, force), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := append(removeObjectResponses(&owned, true, map[string]string{"workspace": owned}, nil), test.response)
				allowed := force || (!test.sessions && !test.unknown)
				if allowed {
					responses = append(responses, testutil.Response{}, testutil.Response{})
				}
				fakes.Script("podman", responses...)
				args := []string{"remove", "agent01"}
				if force {
					args = append(args, "--force")
				}
				stdout, stderr, status := runCLI(t, "production", args...)
				if (status == 0) != allowed {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				query := testutil.Call{Args: []string{"exec", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "sessions", "list"}}
				want := []testutil.Call{query}
				if allowed {
					want = append(want, testutil.Call{Args: []string{"stop", "sandboxed-agents.default.agent01"}}, testutil.Call{Args: []string{"rm", "sandboxed-agents.default.agent01"}})
				}
				if got := removeMutations(t, fakes); !reflect.DeepEqual(got, want) {
					t.Fatalf("calls=%v want=%v", got, want)
				}
				message := stderr
				if allowed {
					message = stdout
				}
				if test.sessions {
					for _, session := range []string{"work-1", "work-2", "codex", "claude"} {
						if !strings.Contains(message, session) {
							t.Fatalf("missing session %s: %q", session, message)
						}
					}
					if allowed && !strings.Contains(stdout, "Ended") {
						t.Fatalf("stdout=%q", stdout)
					}
				}
				if test.unknown {
					if allowed {
						if !strings.Contains(stdout, "cannot be named") || !strings.Contains(stdout, "were ended") {
							t.Fatalf("stdout=%q", stdout)
						}
					} else if !strings.Contains(stderr, "cannot be ruled out") {
						t.Fatalf("stderr=%q", stderr)
					}
				}
				if !allowed && !strings.Contains(stderr, "--force") {
					t.Fatalf("stderr=%q", stderr)
				}
				assertUpNoSSH(t, fakes)
			})
		}
	}
}

func TestRemovePreservesABoundWorkspaceAndHandlesItsUnusedVolume(t *testing.T) {
	for _, deleteVolumes := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete-%t", deleteVolumes), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			workspace := t.TempDir()
			marker := filepath.Join(workspace, "work.txt")
			if err := os.WriteFile(marker, []byte("keep this work"), 0600); err != nil {
				t.Fatal(err)
			}
			responses := removeObjectResponses(&owned, false, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
			responses[1].Stdout = fmt.Sprintf(`[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.workspace-kind":"bind"}},"State":{"Running":false},"Mounts":[{"Type":"bind","Source":%q,"Destination":"/workspace"}]}]`, workspace)
			mutations := 1
			if deleteVolumes {
				mutations += 3
			}
			fakes.Script("podman", append(responses, make([]testutil.Response, mutations)...)...)
			args := []string{"remove", "agent01"}
			if deleteVolumes {
				args = append(args, "--volumes")
			}
			stdout, stderr, status := runCLI(t, "production", args...)
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			data, err := os.ReadFile(marker)
			if err != nil || string(data) != "keep this work" {
				t.Fatalf("workspace changed: %q %v", data, err)
			}
			verb := "Kept"
			if deleteVolumes {
				verb = "Removed"
			}
			if !strings.Contains(stdout, verb+" volume sandboxed-agents.default.agent01.workspace") {
				t.Fatalf("stdout=%q", stdout)
			}
			for _, call := range fakes.Calls("podman") {
				if strings.Contains(strings.Join(call.Args, " "), workspace) {
					t.Fatalf("Podman touched host path: %v", call)
				}
			}
			assertUpNoSSH(t, fakes)
		})
	}
}

func TestRefusedRemoveLeavesTheHostSSHSetupUntouched(t *testing.T) {
	for _, reason := range []string{"owner", "backup", "sessions", "manager"} {
		t.Run(reason, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
			t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
			paths := []string{filepath.Join(home, ".ssh", "config"), filepath.Join(home, "state", "sandboxed-agents", "group-default", "agent01", "id_ed25519"), filepath.Join(home, "local", "sandboxed-agents", "group-default", "agent01", "known_hosts")}
			for _, path := range paths {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			volumes := map[string]string{"workspace": owned}
			var backup *string
			if reason == "owner" {
				volumes["workspace"] = "foreign"
			}
			if reason == "backup" {
				backup = &owned
			}
			responses := removeObjectResponses(&owned, true, volumes, backup)
			if reason == "sessions" {
				responses = append(responses, testutil.Response{Stdout: `[{"name":"work","agent":"codex"}]`})
			}
			if reason == "manager" {
				responses = append(responses, testutil.Response{ExitCode: 1})
			}
			fakes.Script("podman", responses...)
			_, stderr, status := runCLI(t, "production", "remove", "agent01")
			if status == 0 {
				t.Fatalf("refusal expected: %q", stderr)
			}
			for _, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "untouched" {
					t.Fatalf("SSH setup changed: %s %q %v", path, data, err)
				}
			}
			for _, call := range removeMutations(t, fakes) {
				if call.Args[0] != "exec" {
					t.Fatalf("refusal mutated Podman: %v", call)
				}
			}
			assertUpNoSSH(t, fakes)
		})
	}
}

func TestRemoveStopsOnPodmanFailuresBeforeDeletingAnythingElse(t *testing.T) {
	for _, operation := range []string{"query-owner", "stop", "rm", "volume-rm"} {
		t.Run(operation, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := removeObjectResponses(&owned, true, map[string]string{"workspace": owned, "home": owned}, nil)
			if operation == "query-owner" {
				responses[1].Stdout = `[{"Name":"another-container","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default"}},"State":{"Running":true}}]`
			} else {
				responses = append(responses, testutil.Response{Stdout: "[]"})
				for _, next := range []string{"stop", "rm", "volume-rm"} {
					if next == operation {
						responses = append(responses, testutil.Response{ExitCode: 125})
						break
					}
					responses = append(responses, testutil.Response{})
				}
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "production", "remove", "agent01", "--volumes")
			if status == 0 {
				t.Fatalf("failure expected stdout=%q stderr=%q", stdout, stderr)
			}
			calls := removeMutations(t, fakes)
			want := 0
			if operation != "query-owner" {
				want = map[string]int{"stop": 2, "rm": 3, "volume-rm": 4}[operation]
			}
			if len(calls) != want {
				t.Fatalf("mutations=%v want-count=%d", calls, want)
			}
		})
	}
}

func TestRemoveReportsForeignVolumesBeforeAnInterruptedUpdateEvenWithVolumes(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	fakes.Script("podman", removeObjectResponses(&owned, true, map[string]string{"workspace": "foreign", "home": owned}, &owned)...)
	stdout, stderr, status := runCLI(t, "production", "remove", "agent01", "--volumes", "--force")
	if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, "sandboxed-agents.default.agent01.workspace") || strings.Contains(stderr, "interrupted update") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertUpReadOnly(t, fakes)
}
