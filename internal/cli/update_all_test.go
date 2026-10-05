package cli_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func updateAllInventory(names ...string) []testutil.Response {
	containers := make([]map[string]any, 0, len(names))
	for _, name := range names {
		containers = append(containers, map[string]any{"Names": []string{"sandboxed-agents.default." + name}})
	}
	return []testutil.Response{{Stdout: "podman version 5.0.0\n"}, listJSONResponse(containers), {Stdout: `[]`}}
}

func updateAllObjects(t *testing.T, name string, running bool, image, selection string) []testutil.Response {
	t.Helper()
	responses := updateObjectResponses(t, running, image, selection, "")[1:]
	for index := range responses {
		responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, "agent01", name)
	}
	return responses
}

func updateAllCurrentImage(selection string) []testutil.Response {
	responses := []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}}
	if selection != "" {
		responses = append(responses, testutil.Response{}, listJSONResponse([]map[string]any{{"Id": "current-" + selection, "Labels": map[string]string{"io.github.sandboxed-agents.base-image": "current-base"}}}))
	}
	return responses
}

func updateAllOutdatedImage(selection string, stale bool) []testutil.Response {
	missing := updateAllCurrentImage(selection)
	if stale {
		missing[3] = listJSONResponse([]map[string]any{{"Id": "old-" + selection, "Labels": map[string]string{"io.github.sandboxed-agents.base-image": "old-base"}}})
	} else {
		missing = append(missing[:2], testutil.Response{ExitCode: 1})
	}
	return missing
}

func updateAllBuildImage(selection string, stale bool) []testutil.Response {
	responses := updateAllOutdatedImage(selection, stale)
	responses = append(responses, testutil.Response{}, updateAllCurrentImage(selection)[3])
	return append(responses, updateAllCurrentImage(selection)...)
}

func scriptUpdateAll(t *testing.T, fakes *testutil.FakePrograms, windows bool, responses []testutil.Response) {
	t.Helper()
	scriptUpdate(t, fakes, windows, responses)
	fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t), RepeatForArgs: []string{"-T", "1", "-t", "ed25519", "-p", "2300", "127.0.0.1"}})
}

func updateAllSandboxChanges(t *testing.T, fakes *testutil.FakePrograms, name string) [][]string {
	t.Helper()
	var changes [][]string
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if !slices.Contains([]string{"rename", "create", "start", "stop", "rm"}, args[0]) || !(slices.Contains(args, "sandboxed-agents.default."+name) || slices.Contains(args, "sandboxed-agents-backup.default."+name)) {
			continue
		}
		if args[0] == "create" {
			args = []string{"create"}
		}
		changes = append(changes, args)
	}
	return changes
}

func TestUpdateAllBuildsDistinctMissingAndStaleImagesBeforeUpdatingEverySandbox(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			if !host.windows {
				fixture = "linux-build"
			}
			responses := updateAllInventory("agent01", "agent02", "agent03")
			for _, sandbox := range []struct{ name, selection string }{{"agent01", "dotnet"}, {"agent02", "native"}, {"agent03", "dotnet"}} {
				responses = append(responses, updateAllObjects(t, sandbox.name, true, "old-"+sandbox.selection, sandbox.selection)...)
				responses = append(responses, updateAllOutdatedImage(sandbox.selection, sandbox.selection == "native")...)
			}
			responses = append(responses, updateAllBuildImage("dotnet", false)...)
			responses = append(responses, updateAllBuildImage("native", true)...)
			for range 3 {
				responses = append(responses, successfulRunningUpdateResponses()...)
			}
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			var builds []string
			var renamed []string
			buildCount := 0
			pendingBackup := ""
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if args[0] == "build" {
					buildCount++
					if len(renamed) != 0 {
						t.Fatalf("built after changing sandbox: %v", args)
					}
					for _, selection := range []string{"dotnet", "native"} {
						if slices.Contains(args, "io.github.sandboxed-agents.toolchains="+selection) {
							builds = append(builds, selection)
						}
					}
				}
				if args[0] == "rename" {
					if pendingBackup != "" {
						t.Fatalf("began another update before completing %s: %v", pendingBackup, args)
					}
					pendingBackup = args[2]
					renamed = append(renamed, args[1])
				}
				if args[0] == "rm" && args[1] == pendingBackup {
					pendingBackup = ""
				}
				if args[0] == "create" {
					selection := "dotnet"
					if slices.Contains(args, "sandboxed-agents.default.agent02") {
						selection = "native"
					}
					if args[len(args)-1] != "current-"+selection || !slices.Contains(args, "io.github.sandboxed-agents.toolchains="+selection) {
						t.Fatalf("replacement lost toolchains or used the stale image: %v", args)
					}
				}
			}
			if buildCount != 2 || pendingBackup != "" || !reflect.DeepEqual(builds, []string{"dotnet", "native"}) || !reflect.DeepEqual(renamed, []string{"sandboxed-agents.default.agent01", "sandboxed-agents.default.agent02", "sandboxed-agents.default.agent03"}) {
				t.Fatalf("builds=%v renamed=%v", builds, renamed)
			}
			for _, name := range []string{"agent01", "agent02", "agent03"} {
				if !strings.Contains(stdout, "Sandbox "+name+" is updated.") {
					t.Errorf("missing result for %s: %q", name, stdout)
				}
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllLeavesCurrentSandboxesUntouchedAndKeepsStoppedSandboxesStopped(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			checkSSH := installUpdateSSHFixture(t, sshDir, state)
			responses := updateAllInventory("agent01", "agent02")
			responses = append(responses, updateAllObjects(t, "agent01", false, "old-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, updateAllObjects(t, "agent02", true, "current-native", "native")...)
			responses = append(responses, updateAllCurrentImage("native")...)
			responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is updated.") || !strings.Contains(stdout, "Sandbox agent02 is already up to date.") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			var stateChanges [][]string
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if args[0] == "build" {
					t.Fatalf("rebuilt an image already on the current base: %v", args)
				}
				if slices.Contains([]string{"rename", "create", "start", "stop", "rm", "exec"}, args[0]) && strings.Contains(strings.Join(args, " "), "agent02") {
					t.Fatalf("touched the current sandbox: %v", args)
				}
				if slices.Contains([]string{"start", "stop", "rm"}, args[0]) {
					stateChanges = append(stateChanges, args)
				}
				if args[0] == "create" && !slices.Contains(args, "io.github.sandboxed-agents.update-was-running=false") {
					t.Fatalf("replacement lost its original stopped state: %v", args)
				}
			}
			want := [][]string{{"start", "sandboxed-agents.default.agent01"}, {"rm", "sandboxed-agents-backup.default.agent01"}, {"stop", "sandboxed-agents.default.agent01"}}
			if !reflect.DeepEqual(stateChanges, want) || len(fakes.Calls("ssh-keyscan")) != 1 {
				t.Fatalf("state changes=%v SSH probes=%v", stateChanges, fakes.Calls("ssh-keyscan"))
			}
			checkSSH()
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllRestoresAFailedSandboxAndContinuesWithTheRemainingSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{true, false} {
			t.Run(host.name+"/"+map[bool]string{true: "running", false: "stopped"}[running], func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				responses := updateAllInventory("agent01", "agent02", "agent03")
				for _, name := range []string{"agent01", "agent02", "agent03"} {
					responses = append(responses, updateAllObjects(t, name, name != "agent02" || running, "old-base", "")...)
					responses = append(responses, updateAllCurrentImage("")...)
				}
				responses = append(responses, successfulRunningUpdateResponses()...)
				if running {
					responses = append(responses, testutil.Response{Stdout: `[]`})
				}
				responses = append(responses, testutil.Response{}, testutil.Response{})
				want := [][]string{{"rename", "sandboxed-agents.default.agent02", "sandboxed-agents-backup.default.agent02"}, {"create"}}
				if running {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"stop", "sandboxed-agents-backup.default.agent02"})
				}
				responses = append(responses, testutil.Response{ExitCode: 42}, testutil.Response{}, testutil.Response{})
				want = append(want, []string{"start", "sandboxed-agents.default.agent02"}, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent02"}, []string{"rename", "sandboxed-agents-backup.default.agent02", "sandboxed-agents.default.agent02"})
				if running {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"start", "sandboxed-agents.default.agent02"})
				}
				responses = append(responses, successfulRunningUpdateResponses()...)
				scriptUpdateAll(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "--all")
				if status == 0 || !strings.Contains(stderr, "agent02") || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "42") || !strings.Contains(stdout, "Sandbox agent01 is updated.") || !strings.Contains(stdout, "Sandbox agent03 is updated.") || strings.Contains(stdout, "Sandbox agent02 is updated.") {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				changes := updateAllSandboxChanges(t, fakes, "agent02")
				if !reflect.DeepEqual(changes, want) {
					t.Fatalf("failed sandbox changes=%v want=%v", changes, want)
				}
				checkSSH()
				assertUpdateChangesPreserveData(t, fakes)
			})
		}
	}
}

func TestUpdateAllBuildFailureLeavesEverySandboxAndItsSSHFilesUnchanged(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			checkSSH := installUpdateSSHFixture(t, sshDir, state)
			responses := updateAllInventory("agent01", "agent02")
			for _, sandbox := range []struct{ name, selection string }{{"agent01", "dotnet"}, {"agent02", "native"}} {
				responses = append(responses, updateAllObjects(t, sandbox.name, true, "old-"+sandbox.selection, sandbox.selection)...)
				responses = append(responses, updateAllOutdatedImage(sandbox.selection, false)...)
			}
			responses = append(responses, updateAllBuildImage("dotnet", false)...)
			responses = append(responses, updateAllOutdatedImage("native", false)...)
			responses = append(responses, testutil.Response{ExitCode: 42})
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stderr, "42") || strings.Contains(stdout, "is updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			builds := 0
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Contains([]string{"rename", "stop", "start", "create", "rm", "exec"}, args[0]) {
					t.Fatalf("build failure changed or probed a sandbox: %v", args)
				}
				if args[0] == "build" {
					builds++
				}
			}
			if builds != 2 || len(fakes.Calls("ssh-keyscan")) != 0 {
				t.Fatalf("builds=%d SSH probes=%v", builds, fakes.Calls("ssh-keyscan"))
			}
			checkSSH()
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllRequiresPreflightBeforeDiscoveringOrChangingSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := []testutil.Response{{Stdout: "podman version 4.9.9\n"}}
			if host.windows {
				responses = healthyWindowsPodman()
				responses[0].Stdout = "podman version 4.9.9\n"
			} else {
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
			}
			fakes.Script("podman", responses...)
			_, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stderr, "prerequisites") {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Contains([]string{"ps", "volume", "container", "image", "build", "rename", "create", "start", "stop", "rm"}, args[0]) {
					t.Fatalf("failed preflight reached sandbox discovery: %v", args)
				}
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpdateAllReportsASandboxThatDisappearsAfterDiscoveryAndContinues(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateAllInventory("agent01", "agent02")
			responses = append(responses, updateAllObjects(t, "agent01", true, "old-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, sandboxObjectResponses(nil, false, nil, nil)...)
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stderr, "agent02 does not exist in this controller group") || strings.Contains(stderr, "adopts its volumes") || !strings.Contains(stdout, "Sandbox agent01 is updated.") || strings.Contains(stdout, "agent02") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllReportsCurrentSandboxesEvenWhenAnImageBuildFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateAllInventory("agent01", "agent02")
			responses = append(responses, updateAllObjects(t, "agent01", true, "current-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, updateAllObjects(t, "agent02", true, "old-native", "native")...)
			responses = append(responses, updateAllOutdatedImage("native", false)...)
			responses = append(responses, updateAllOutdatedImage("native", false)...)
			responses = append(responses, testutil.Response{ExitCode: 42})
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stderr, "42") || !strings.Contains(stdout, "Sandbox agent01 is already up to date.") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Contains([]string{"rename", "create", "start", "stop", "rm", "exec"}, args[0]) {
					t.Fatalf("image build failure changed or probed a sandbox: %v", args)
				}
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllSkipsRetainedVolumesAndExcludesOtherControllerGroups(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateAllInventory("agent01")
			responses[1] = listJSONResponse([]map[string]any{
				{"Names": []string{"sandboxed-agents.default.agent01"}},
				{"Names": []string{"sandboxed-agents.other.hidden"}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "default"}},
				{"Names": []string{"sandboxed-agents-backup.other.hidden"}},
			})
			responses[2] = listJSONResponse([]map[string]any{
				{"Name": "sandboxed-agents.default.agent02.home"},
				{"Name": "sandboxed-agents.other.hidden.workspace"},
			})
			responses = append(responses, updateAllObjects(t, "agent01", true, "old-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			retained := sandboxObjectResponses(nil, false, map[string]string{"home": "default"}, nil)
			for index := range retained {
				retained[index].Stdout = strings.ReplaceAll(retained[index].Stdout, "agent01", "agent02")
			}
			responses = append(responses, retained...)
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is updated.") || !strings.Contains(stdout, "agent02") || !strings.Contains(stdout, "volumes") || strings.Contains(stdout+stderr, "hidden") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if strings.Contains(strings.Join(args, " "), "hidden") {
					t.Fatalf("named another controller group's sandbox: %v", args)
				}
				if args[0] == "build" || slices.Contains([]string{"rename", "create", "start", "stop", "rm", "exec"}, args[0]) && strings.Contains(strings.Join(args, " "), "agent02") {
					t.Fatalf("changed retained volumes or built an unnecessary image: %v", args)
				}
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateAllReportsOwnerConflictsWithoutBuildingOrChangingThoseSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
			for _, owner := range []string{"", "other"} {
				t.Run(host.name+"/"+object+"/owner-"+owner, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					responses := updateAllInventory("agent01", "agent02")
					responses = append(responses, updateAllObjects(t, "agent01", true, "old-dotnet", "dotnet")...)
					responses = append(responses, updateAllOutdatedImage("dotnet", false)...)
					conflicting := updateAllObjects(t, "agent02", true, "old-native", "native")
					objectName := "sandboxed-agents.default.agent02"
					if object == "backup" {
						objectName = "sandboxed-agents-backup.default.agent02"
						conflicting[len(conflicting)-1] = testutil.Response{}
						backup := conflicting[1]
						backup.Stdout = strings.ReplaceAll(backup.Stdout, "sandboxed-agents.default.agent02", objectName)
						conflicting = append(conflicting, backup)
					} else if object != "container" {
						objectName += "." + object
					}
					conflicting = updateObjectOwner(t, conflicting, objectName, owner)
					responses = append(responses, conflicting...)
					responses = append(responses, updateAllBuildImage("dotnet", false)...)
					responses = append(responses, successfulRunningUpdateResponses()...)
					scriptUpdateAll(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "--all")
					if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, objectName) || !strings.Contains(stderr, "Podman") || !strings.Contains(stdout, "Sandbox agent01 is updated.") || strings.Contains(stdout, "Sandbox agent02 is updated.") {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
					}
					builds := 0
					for _, call := range fakes.Calls("podman") {
						args := podmanUpdateArgs(call.Args)
						if args[0] == "build" {
							builds++
							if !slices.Contains(args, "io.github.sandboxed-agents.toolchains=dotnet") {
								t.Fatalf("built the conflicting sandbox's unique toolchain set: %v", args)
							}
						}
						if slices.Contains([]string{"rename", "create", "start", "stop", "rm", "exec"}, args[0]) && strings.Contains(strings.Join(args, " "), "agent02") {
							t.Fatalf("changed a sandbox with an owner conflict: %v", args)
						}
					}
					if builds != 1 {
						t.Fatalf("builds=%d", builds)
					}
					assertUpdateChangesPreserveData(t, fakes)
				})
			}
		}
	}
}

func TestUpdateAllContinuesAfterBackupRemovalOrFinalStopFails(t *testing.T) {
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
				responses := updateAllInventory("agent01", "agent02")
				responses = append(responses, updateAllObjects(t, "agent01", scenario.running, "old-base", "")...)
				responses = append(responses, updateAllCurrentImage("")...)
				responses = append(responses, updateAllObjects(t, "agent02", true, "old-base", "")...)
				responses = append(responses, updateAllCurrentImage("")...)
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
				responses = append(responses, successfulRunningUpdateResponses()...)
				scriptUpdateAll(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "--all")
				if status == 0 || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "42") || strings.Contains(stderr, "restored") || !strings.Contains(stdout, "Sandbox agent02 is updated.") {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				phrases := []string{"succeeded", "backup container", "update interrupted", "update agent01"}
				if scenario.finalStop {
					phrases = []string{"done", "stop", "still running"}
				} else if !scenario.running {
					phrases = append(phrases, "still running")
				}
				for _, phrase := range phrases {
					if !strings.Contains(stderr, phrase) {
						t.Errorf("missing %q: %q", phrase, stderr)
					}
				}
				changes := updateAllSandboxChanges(t, fakes, "agent01")
				if !reflect.DeepEqual(changes, want) {
					t.Fatalf("completed sandbox changes=%v want=%v", changes, want)
				}
				checkSSH()
				assertUpdateChangesPreserveData(t, fakes)
			})
		}
	}
}

func TestUpdateRequiresExactlyOneNamedOrAllTargetBeforeCallingPodman(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, args := range [][]string{{"update"}, {"update", "agent01", "--all"}, {"update", "--all", "agent01"}} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}
