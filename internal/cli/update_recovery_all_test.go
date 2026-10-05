package cli_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func recoveryAllObjects(t *testing.T, name string, container, running, backupRunning bool, wasRunning string) []testutil.Response {
	t.Helper()
	responses := recoveryObjectResponses(t, container, running, backupRunning, wasRunning)[1:]
	for index := range responses {
		responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, "agent01", name)
	}
	return responses
}

func TestUpdateAllCompletesAndRestoresInterruptedUpdatesAndUpdatesOtherSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateAllInventory("agent01", "agent03")
			responses[1] = listJSONResponse([]map[string]any{
				{"Names": []string{"sandboxed-agents.default.agent01"}},
				{"Names": []string{"sandboxed-agents-backup.default.agent01"}},
				{"Names": []string{"sandboxed-agents-backup.default.agent02"}},
				{"Names": []string{"sandboxed-agents.default.agent03"}},
			})
			responses = append(responses, recoveryAllObjects(t, "agent01", true, true, false, "false")...)
			responses = append(responses, recoveryAllObjects(t, "agent02", false, false, false, "")...)
			responses = append(responses, updateAllObjects(t, "agent03", true, "old-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
			responses = append(responses, testutil.Response{})
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stdout, "interrupted update of sandbox agent01 was completed") || !strings.Contains(stdout, "is updated") || !strings.Contains(stderr, "agent02") || !strings.Contains(stderr, "not updated") || !strings.Contains(stderr, "restored") || !strings.Contains(stdout, "Sandbox agent03 is updated.") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for name, want := range map[string][][]string{
				"agent01": {{"rm", "sandboxed-agents-backup.default.agent01"}, {"stop", "sandboxed-agents.default.agent01"}},
				"agent02": {{"rename", "sandboxed-agents-backup.default.agent02", "sandboxed-agents.default.agent02"}},
			} {
				if changes := updateAllSandboxChanges(t, fakes, name); !reflect.DeepEqual(changes, want) {
					t.Fatalf("%s changes=%v want=%v", name, changes, want)
				}
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllFinishesEveryImageBuildBeforeRecoveringInterruptedUpdates(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, buildFails := range []bool{true, false} {
			t.Run(host.name+"/"+map[bool]string{true: "build fails", false: "build succeeds"}[buildFails], func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				if !host.windows {
					fixture = "linux-build"
				}
				responses := updateAllInventory("agent01", "agent02")
				responses = append(responses, recoveryAllObjects(t, "agent01", true, false, true, "true")...)
				responses = append(responses, updateAllObjects(t, "agent02", true, "old-native", "native")...)
				responses = append(responses, updateAllOutdatedImage("native", false)...)
				if buildFails {
					responses = append(responses, updateAllOutdatedImage("native", false)...)
					responses = append(responses, testutil.Response{ExitCode: 42})
				} else {
					responses = append(responses, updateAllBuildImage("native", false)...)
					responses = append(responses, testutil.Response{}, testutil.Response{})
					responses = append(responses, successfulRunningUpdateResponses()...)
				}
				scriptUpdateAll(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "--all")
				if status == 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				built := false
				for _, call := range fakes.Calls("podman") {
					args := podmanUpdateArgs(call.Args)
					if args[0] == "build" {
						built = true
					}
					if slices.Contains([]string{"rename", "create", "start", "stop", "rm", "exec"}, args[0]) && (buildFails || !built) {
						t.Fatalf("recovery or update preceded successful builds: %v", args)
					}
				}
				if !built {
					t.Fatal("missing toolchain image was not built")
				}
				if buildFails {
					if !strings.Contains(stderr, "42") || strings.Contains(stderr, "restored") {
						t.Fatalf("build failure=%q", stderr)
					}
				} else {
					if !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "restored") || !strings.Contains(stdout, "Sandbox agent02 is updated.") {
						t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
					}
					want := [][]string{{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, {"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}}
					if changes := updateAllSandboxChanges(t, fakes, "agent01"); !reflect.DeepEqual(changes, want) {
						t.Fatalf("changes=%v want=%v", changes, want)
					}
				}
				assertUpdateChangesPreserveData(t, fakes)
			})
		}
	}
}
