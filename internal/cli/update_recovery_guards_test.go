package cli_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestCheckReportsEveryInterruptedUpdateStateWithoutRecovery(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, state := range []struct {
			name, status       string
			container, running bool
		}{
			{name: "backup alone"},
			{name: "replacement created", status: "created", container: true},
			{name: "replacement stopped", status: "exited", container: true},
			{name: "replacement started", status: "running", container: true, running: true},
			{name: "replacement ready", status: "running", container: true, running: true},
		} {
			for _, backupRunning := range []bool{false, true} {
				for _, conflict := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/backup-running-%t/conflict-%t", host.name, state.name, backupRunning, conflict), func(t *testing.T) {
						fakes, fixture, _, _ := sshSetupHost(t, host.windows)
						owner := "default"
						volumes := map[string]string{"workspace": owner, "home": owner, "ssh": owner}
						if conflict {
							volumes["home"] = "foreign"
						}
						responses := checkObjectResponses(t, owner, "agent01", owner, state.container, state.running, volumes, &owner, nil, "")
						responses = recoveryInspectState(t, responses, "sandboxed-agents-backup.default.agent01", backupRunning, map[bool]string{false: "exited", true: "running"}[backupRunning])
						if state.container {
							responses = recoveryInspectState(t, responses, "sandboxed-agents.default.agent01", state.running, state.status)
						}
						if state.running && !conflict {
							probe := testutil.Response{ExitCode: 42}
							if state.name == "replacement ready" {
								probe = testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}
							}
							responses = append(responses, probe)
						}
						scriptCheckObjects(fakes, host.windows, responses)
						stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
						if status == 0 || !strings.Contains(stderr, "agent01") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						for _, want := range []string{"interrupted update", "sandboxed-agents-backup.default.agent01", "update agent01"} {
							if !strings.Contains(stdout, want) {
								t.Errorf("output=%q lacks %q", stdout, want)
							}
						}
						if conflict {
							for _, want := range []string{"owner conflict", "sandboxed-agents.default.agent01.home", "Podman"} {
								if !strings.Contains(stdout, want) {
									t.Errorf("output=%q lacks %q", stdout, want)
								}
							}
						} else if !strings.Contains(stdout, "state update interrupted") {
							t.Errorf("interruption state missing: %q", stdout)
						}
						assertCheckReadOnly(t, fakes, host.windows, state.running && !conflict)
						assertNoSSHBesidesReadiness(t, fakes)
					})
				}
			}
		}
	}
}

func recoveryInspectState(t *testing.T, responses []testutil.Response, name string, running bool, status string) []testutil.Response {
	t.Helper()
	responses = slices.Clone(responses)
	found := false
	for index := range responses {
		if !strings.HasPrefix(strings.TrimSpace(responses[index].Stdout), "[") {
			continue
		}
		var records []map[string]any
		if err := json.Unmarshal([]byte(responses[index].Stdout), &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record["Name"] != name {
				continue
			}
			record["State"] = map[string]any{"Running": running, "Status": status}
			data, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			responses[index].Stdout = string(data)
			found = true
		}
	}
	if !found {
		t.Fatalf("inspect fixture lacks %s", name)
	}
	return responses
}

func TestUpdateRecoveryRefusesEveryOwnerConflictBeforeHandlingTheBackup(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, container := range []bool{false, true} {
			for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
				if object == "container" && !container {
					continue
				}
				for _, owner := range []string{"", "foreign"} {
					for _, backupRunning := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/container-%t/%s/owner-%s/backup-running-%t", host.name, container, object, owner, backupRunning), func(t *testing.T) {
							fakes, fixture := resourceLimitHost(t, host.windows)
							responses := recoveryObjectResponses(t, container, false, backupRunning, "invalid")
							objectName := recoveryConflictObject("agent01", object)
							responses = updateObjectOwner(t, responses, objectName, owner)
							scriptUpdate(t, fakes, host.windows, responses)
							stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
							if status == 0 || strings.Contains(stdout, "is updated") {
								t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
							}
							for _, want := range []string{"owner conflict", objectName, "Podman"} {
								if !strings.Contains(stderr, want) {
									t.Errorf("stderr=%q lacks %q", stderr, want)
								}
							}
							if strings.Contains(stderr, "update agent01") || strings.Contains(stderr, "update-was-running") || strings.Contains(stderr, "restored") || strings.Contains(stderr, "completed") {
								t.Errorf("owner conflict did not win over recovery: %q", stderr)
							}
							assertUpdateChecksReadOnly(t, fakes, host.windows, fakes.Calls("podman"))
						})
					}
				}
			}
		}
	}
}

func recoveryConflictObject(name, object string) string {
	if object == "backup" {
		return "sandboxed-agents-backup.default." + name
	}
	objectName := "sandboxed-agents.default." + name
	if object != "container" {
		objectName += "." + object
	}
	return objectName
}

func TestUpdateAllRecoveryPassesOverEveryOwnerConflictAndReportsCurrentNeighbors(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, container := range []bool{false, true} {
			for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
				if object == "container" && !container {
					continue
				}
				for _, owner := range []string{"", "foreign"} {
					t.Run(fmt.Sprintf("%s/container-%t/%s/owner-%s", host.name, container, object, owner), func(t *testing.T) {
						fakes, fixture := resourceLimitHost(t, host.windows)
						responses := updateAllInventory("agent01", "agent03")
						responses[1] = listJSONResponse([]map[string]any{
							{"Names": []string{"sandboxed-agents.default.agent01"}},
							{"Names": []string{"sandboxed-agents-backup.default.agent02"}},
							{"Names": []string{"sandboxed-agents.default.agent03"}},
						})
						responses = append(responses, updateAllObjects(t, "agent01", true, "current-base", "")...)
						responses = append(responses, updateAllCurrentImage("")...)
						conflicting := recoveryAllObjects(t, "agent02", container, false, false, "invalid")
						objectName := recoveryConflictObject("agent02", object)
						responses = append(responses, updateObjectOwner(t, conflicting, objectName, owner)...)
						responses = append(responses, updateAllObjects(t, "agent03", false, "current-base", "")...)
						responses = append(responses, updateAllCurrentImage("")...)
						scriptUpdateAll(t, fakes, host.windows, responses)
						stdout, stderr, status := runCLI(t, fixture, "update", "--all")
						if status == 0 || strings.Contains(stdout, "agent02") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						for _, want := range []string{"owner conflict", objectName, "Podman", "failed"} {
							if !strings.Contains(stderr, want) {
								t.Errorf("stderr=%q lacks %q", stderr, want)
							}
						}
						for _, name := range []string{"agent01", "agent03"} {
							if !strings.Contains(stdout, "Sandbox "+name+" is already up to date.") {
								t.Errorf("current sandbox was not reported: %q", stdout)
							}
						}
						if strings.Contains(stderr, "update agent02") || strings.Contains(stderr, "update-was-running") || strings.Contains(stderr, "restored") || strings.Contains(stderr, "completed") {
							t.Errorf("owner conflict did not win over recovery: %q", stderr)
						}
						imageCalls := 0
						for _, call := range fakes.Calls("podman") {
							args := podmanUpdateArgs(call.Args)
							if slices.Contains([]string{"build", "create", "exec", "rename", "rm", "start", "stop"}, args[0]) || args[0] == "volume" && args[1] == "rm" {
								t.Fatalf("owner conflict or current sandbox changed or probed: %v", args)
							}
							if args[0] == "image" {
								imageCalls++
								if len(args) != 3 || !slices.Contains([]string{"exists", "inspect"}, args[1]) {
									t.Fatalf("changed an image: %v", args)
								}
							}
							if strings.Contains(strings.Join(args, " "), "agent02") && (len(args) != 3 || !slices.Contains([]string{"container", "volume"}, args[0]) || !slices.Contains([]string{"exists", "inspect"}, args[1])) {
								t.Fatalf("owner-conflicting sandbox reached planning or recovery: %v", args)
							}
						}
						if imageCalls != 4 {
							t.Fatalf("image checks=%d want two per current neighbor", imageCalls)
						}
						assertNoSSHBesidesReadiness(t, fakes)
						if len(updateReadinessProbes(fakes)) != 0 {
							t.Fatal("owner conflict or current sandbox attempted SSH readiness")
						}
					})
				}
			}
		}
	}
}

func TestUpdateRecoveryRefusesMissingOrInvalidPreviousStateWithoutMutation(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{false, true} {
			for _, backupRunning := range []bool{false, true} {
				for _, label := range []string{"missing", "empty", "running", "1", "TRUE"} {
					t.Run(fmt.Sprintf("%s/running-%t/backup-running-%t/label-%s", host.name, running, backupRunning, label), func(t *testing.T) {
						fakes, fixture := resourceLimitHost(t, host.windows)
						responses := recoveryObjectResponses(t, true, running, backupRunning, label)
						if label == "missing" || label == "empty" {
							responses = updateObjectLabels(t, responses, "sandboxed-agents.default.agent01", func(labels map[string]any) {
								if label == "missing" {
									delete(labels, "io.github.sandboxed-agents.update-was-running")
								} else {
									labels["io.github.sandboxed-agents.update-was-running"] = ""
								}
							})
						}
						scriptUpdate(t, fakes, host.windows, responses)
						stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
						if status == 0 || strings.Contains(stdout, "is updated") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						for _, want := range []string{"sandboxed-agents.default.agent01", "update-was-running", "interrupted update", "Podman"} {
							if !strings.Contains(stderr, want) {
								t.Errorf("stderr=%q lacks %q", stderr, want)
							}
						}
						if strings.Contains(stderr, "restored") || strings.Contains(stderr, "completed") {
							t.Errorf("invalid previous state led to recovery: %q", stderr)
						}
						assertUpdateChecksReadOnly(t, fakes, host.windows, fakes.Calls("podman"))
					})
				}
			}
		}
	}
}
