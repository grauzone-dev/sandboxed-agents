package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func installUpdateSSHFixture(t *testing.T, sshDir, state string) func() {
	t.Helper()
	for _, path := range []string{filepath.Join(sshDir, "config"), filepath.Join(state, "group-default", "agent01", "id_ed25519"), filepath.Join(state, "group-default", "agent01", "known_hosts")} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("installed SSH file stays unchanged\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
	return func() {
		t.Helper()
		if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
			t.Fatal("failed update changed installed SSH files")
		}
	}
}

func assertUpdateChangesPreserveData(t *testing.T, fakes *testutil.FakePrograms) [][]string {
	t.Helper()
	var changes [][]string
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if args[0] == "volume" && !slices.Contains([]string{"ls", "exists", "inspect"}, args[1]) {
			t.Fatalf("failed update changed a volume: %v", args)
		}
		if args[0] == "rm" && (slices.Contains(args, "--volumes") || slices.Contains(args, "-v")) {
			t.Fatalf("failed update removed container volumes: %v", args)
		}
		if slices.Contains([]string{"rename", "create", "stop", "start", "rm"}, args[0]) {
			if args[0] == "create" {
				args = []string{"create"}
			}
			changes = append(changes, args)
		}
	}
	assertNoSSHBesidesReadiness(t, fakes)
	return changes
}

func TestUpdateRestoresTheOriginalContainerWithoutStoppingItWhenCreationFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{true, false} {
			t.Run(host.name+"/"+map[bool]string{true: "running", false: "stopped"}[running], func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				responses := updateObjectResponses(t, running, "old-image", "", "")
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
				if running {
					responses = append(responses, testutil.Response{Stdout: `[]`})
				}
				responses = append(responses, testutil.Response{}, testutil.Response{ExitCode: 42}, testutil.Response{}, testutil.Response{})
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "create") || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "42") || strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				want := [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"},
					{"create"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				checkSSH()
			})
		}
	}
}

func TestUpdateRestoresTheOriginalRunningStateWhenStoppingOrStartingFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{true, false} {
			for _, failure := range []string{"stop", "start"} {
				if !running && failure == "stop" {
					continue
				}
				t.Run(host.name+"/"+map[bool]string{true: "running", false: "stopped"}[running]+"/"+failure, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					checkSSH := installUpdateSSHFixture(t, sshDir, state)
					responses := updateObjectResponses(t, running, "old-image", "", "")
					responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
					if running {
						responses = append(responses, testutil.Response{Stdout: `[]`})
					}
					responses = append(responses, testutil.Response{}, testutil.Response{})
					want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"}}
					if running {
						want = append(want, []string{"stop", "sandboxed-agents-backup.default.agent01"})
						if failure != "stop" {
							responses = append(responses, testutil.Response{})
						}
					}
					if failure == "start" {
						want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
					}
					responses = append(responses, testutil.Response{ExitCode: 42}, testutil.Response{}, testutil.Response{})
					want = append(want, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
					if running {
						responses = append(responses, testutil.Response{})
						want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
					}
					scriptUpdate(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
					if status == 0 || !strings.Contains(stderr, failure) || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "42") || strings.Contains(stdout, "updated") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
						t.Fatalf("changes=%v want=%v", changes, want)
					}
					checkSSH()
				})
			}
		}
	}
}

func TestUpdateLeavesTheOriginalContainerUntouchedWhenRenameFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			checkSSH := installUpdateSSHFixture(t, sshDir, state)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`}, testutil.Response{Stdout: `[]`}, testutil.Response{ExitCode: 42})
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "rename") || !strings.Contains(stderr, "42") || strings.Contains(stderr, "restored") || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}}
			if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes=%v want=%v", changes, want)
			}
			checkSSH()
		})
	}
}

func TestUpdateKeepsTheNewContainerWhenCleanupAfterReadinessFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, scenario := range []struct {
			name      string
			running   bool
			finalStop bool
		}{
			{name: "running backup removal", running: true},
			{name: "stopped backup removal"},
			{name: "stopped final stop", finalStop: true},
		} {
			t.Run(host.name+"/"+scenario.name, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				responses := updateObjectResponses(t, scenario.running, "old-image", "", "")
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
				if scenario.running {
					responses = append(responses, testutil.Response{Stdout: `[]`})
				}
				responses = append(responses, testutil.Response{}, testutil.Response{})
				want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"}}
				if scenario.running {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"stop", "sandboxed-agents-backup.default.agent01"})
				}
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"})
				want = append(want, []string{"start", "sandboxed-agents.default.agent01"}, []string{"rm", "sandboxed-agents-backup.default.agent01"})
				if scenario.finalStop {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"stop", "sandboxed-agents.default.agent01"})
				}
				responses = append(responses, testutil.Response{ExitCode: 42})
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "42") || strings.Contains(stderr, "restored") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				phrases := []string{"succeeded", "backup container", "sandboxed-agents-backup.default.agent01", "update interrupted", "other commands refuse", "update agent01", "cleans up"}
				if scenario.finalStop {
					phrases = []string{"done", "stop", "still running"}
					if strings.Contains(stderr, "update interrupted") {
						t.Fatalf("final stop failure claimed an interrupted update: %q", stderr)
					}
				} else if !scenario.running {
					phrases = append(phrases, "still running")
				}
				for _, phrase := range phrases {
					if !strings.Contains(stderr, phrase) {
						t.Errorf("missing %q: %q", phrase, stderr)
					}
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				checkSSH()
			})
		}
	}
}

func TestUpdateRestoresASandboxWhenEitherReadinessProbeNeverAnswers(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		windows bool
		running bool
		manager bool
	}{
		{name: "running manager", manager: true, running: true},
		{name: "stopped SSH", windows: true},
		{name: "running SSH", windows: true, running: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, scenario.windows)
			checkSSH := installUpdateSSHFixture(t, sshDir, state)
			responses := updateObjectResponses(t, scenario.running, "old-image", "", "")
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
			if scenario.running {
				responses = append(responses, testutil.Response{Stdout: `[]`})
			}
			responses = append(responses, testutil.Response{}, testutil.Response{})
			want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"}}
			if scenario.running {
				responses = append(responses, testutil.Response{})
				want = append(want, []string{"stop", "sandboxed-agents-backup.default.agent01"})
			}
			responses = append(responses, testutil.Response{})
			want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
			probe := testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}
			if scenario.manager {
				probe = testutil.Response{ExitCode: 42, RepeatForArgs: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}}
			}
			responses = append(responses, probe, testutil.Response{}, testutil.Response{})
			want = append(want, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
			if scenario.running {
				responses = append(responses, testutil.Response{})
				want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
			}
			scriptUpdate(t, fakes, scenario.windows, responses)
			if !scenario.manager {
				fakes.Script("ssh", testutil.Response{ExitCode: 255, RepeatForArgs: updateReadinessArgs("2300")})
			}
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "readiness") || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "deadline exceeded") || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes=%v want=%v", changes, want)
			}
			checkSSH()
		})
	}
}

func TestUpdateStopsAnIncompleteRollbackBeforeFurtherContainerChanges(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, failureIndex := range []int{0, 1, 2} {
			t.Run(host.name+"/"+[]string{"remove", "rename", "start"}[failureIndex], func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				responses := updateObjectResponses(t, true, "old-image", "", "")
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`}, testutil.Response{Stdout: `[]`}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{ExitCode: 42})
				responses = append(responses, make([]testutil.Response, failureIndex)...)
				responses = append(responses, testutil.Response{ExitCode: 43})
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				for _, phrase := range []string{"start the new", "incomplete", []string{"remove", "rename", "start"}[failureIndex], "42", "43", "Podman"} {
					if !strings.Contains(stderr, phrase) {
						t.Errorf("missing %q: %q", phrase, stderr)
					}
				}
				if status == 0 || strings.Contains(stderr, "was restored:") || strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				want := [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"stop", "sandboxed-agents-backup.default.agent01"}, {"start", "sandboxed-agents.default.agent01"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
					{"start", "sandboxed-agents.default.agent01"},
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want[:5+failureIndex]) {
					t.Fatalf("changes=%v want=%v", changes, want[:5+failureIndex])
				}
				checkSSH()
			})
		}
	}
}
