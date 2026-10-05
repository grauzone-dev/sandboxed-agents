package cli_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func recoveryObjectResponses(t *testing.T, container bool, running, backupRunning bool, wasRunning string) []testutil.Response {
	t.Helper()
	owner := "default"
	var responses []testutil.Response
	if container {
		responses = updateObjectResponses(t, running, "", "", "")
		responses = updateObjectLabels(t, responses, "sandboxed-agents.default.agent01", func(labels map[string]any) {
			labels["io.github.sandboxed-agents.update-was-running"] = wasRunning
		})
		responses = append(responses[:len(responses)-1], testutil.Response{})
	} else {
		responses = append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, sandboxObjectResponses(nil, false, map[string]string{"workspace": owner, "home": owner, "ssh": owner}, &owner)...)
		responses = responses[:len(responses)-1]
	}
	return append(responses, testutil.Response{Stdout: fmt.Sprintf(`[{"Name":"sandboxed-agents-backup.default.agent01","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default"}},"State":{"Running":%t}}]`, backupRunning)})
}

func TestUpdateCompletesAnInterruptedUpdateWithoutBuildingOrReplacingTheSandbox(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{true, false} {
			for _, wasRunning := range []string{"true", "false"} {
				t.Run(fmt.Sprintf("%s/running-%t/previous-%s", host.name, running, wasRunning), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					checkSSH := installUpdateSSHFixture(t, sshDir, state)
					responses := recoveryObjectResponses(t, true, running, false, wasRunning)
					var want [][]string
					if !running {
						responses = append(responses, testutil.Response{})
						want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
					}
					responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{})
					want = append(want, []string{"rm", "sandboxed-agents-backup.default.agent01"})
					if wasRunning == "false" {
						responses = append(responses, testutil.Response{})
						want = append(want, []string{"stop", "sandboxed-agents.default.agent01"})
					}
					scriptUpdate(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
					if status != 0 || stderr != "" || !strings.Contains(stdout, "interrupted update") || !strings.Contains(stdout, "completed") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
						t.Fatalf("changes=%v want=%v", changes, want)
					}
					assertRecoverySkipsImagePlanning(t, fakes)
					if len(fakes.Calls("ssh-keyscan")) != 1 {
						t.Fatal("recovery did not check sshd readiness")
					}
					checkSSH()
				})
			}
		}
	}
}

func assertRecoverySkipsImagePlanning(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if slices.Contains([]string{"image", "build", "create"}, args[0]) || slices.Contains(args, "sessions") {
			t.Fatalf("recovery planned another update or queried sessions: %v", args)
		}
	}
	assertNoSSH(t, fakes)
}

func TestUpdateRestoresABackupWithoutStartingTwoContainersOnItsVolumes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, state := range []struct {
			name                              string
			container, running, backupRunning bool
			wasRunning                        string
		}{
			{name: "running backup alone", backupRunning: true},
			{name: "stopped backup alone"},
			{name: "running backup beside created replacement", container: true, backupRunning: true, wasRunning: "true"},
			{name: "replacement start failed previously running", container: true, wasRunning: "true"},
			{name: "replacement start failed previously stopped", container: true, wasRunning: "false"},
		} {
			t.Run(host.name+"/"+state.name, func(t *testing.T) {
				fakes, fixture, sshDir, hostState := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, hostState)
				responses := recoveryObjectResponses(t, state.container, state.running, state.backupRunning, state.wasRunning)
				var want [][]string
				if state.container && !state.backupRunning {
					responses = append(responses, testutil.Response{ExitCode: 42})
					want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
				}
				if state.container {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"})
				}
				responses = append(responses, testutil.Response{})
				want = append(want, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
				if state.container && !state.backupRunning && state.wasRunning == "true" {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
				}
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "interrupted update") || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "not updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if !state.container && !state.backupRunning {
					if !strings.Contains(stderr, "could not be determined") || !strings.Contains(stderr, "start agent01") {
						t.Fatalf("unknown previous state not explained: %q", stderr)
					}
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				assertRecoverySkipsImagePlanning(t, fakes)
				if len(fakes.Calls("ssh-keyscan")) != 0 {
					t.Fatal("recovery probed a replacement that must be removed")
				}
				checkSSH()
			})
		}
	}
}
