package cli_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestRecoveryRestoresThePreviousStateAfterCancellationDuringEitherReadinessProbe(t *testing.T) {
	for _, probe := range []string{"podman", "ssh-keyscan"} {
		for _, wasRunning := range []string{"true", "false"} {
			t.Run(probe+"/previous-"+wasRunning, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := recoveryObjectResponses(t, true, true, false, wasRunning)[1:]
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
				want := [][]string{{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, {"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}}
				if wasRunning == "true" {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
				}
				fakes.Script("podman", responses...)
				fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t)})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var stdout, stderr bytes.Buffer
				cleanupCalls := 0
				update := sandbox.NewUpdate("agent01", "default", "fixture-assets", sandbox.UpdateOptions{}, func(requestCtx context.Context, request process.Request) (int, error) {
					if ctx.Err() != nil {
						cleanupCalls++
						deadline, present := requestCtx.Deadline()
						if requestCtx.Err() != nil || !present || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
							t.Fatalf("recovery context error=%v deadline=%v present=%v", requestCtx.Err(), deadline, present)
						}
					}
					status, err := platform.Run(requestCtx, request)
					if request.Name == probe && (probe == "ssh-keyscan" || request.Args[0] == "exec") {
						cancel()
					}
					return status, err
				}, process.Streams{Stdout: &stdout, Stderr: &stderr})
				for _, check := range []func(context.Context) error{update.CheckContainer, update.CheckOwner} {
					if err := check(ctx); err != nil {
						t.Fatal(err)
					}
				}
				err := update.RecoverInterruptedUpdate(ctx)
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "restored") || cleanupCalls != len(want) {
					t.Fatalf("error=%v cleanup calls=%d", err, cleanupCalls)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				assertRecoverySkipsImagePlanning(t, fakes)
			})
		}
	}
}

func TestRecoveryStopsWhenRemovingRenamingOrStartingTheRestoredContainerFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, step := range []string{"remove", "rename", "start"} {
			t.Run(host.name+"/"+step, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := recoveryObjectResponses(t, true, false, step != "start", "true")
				var want [][]string
				if step == "start" {
					responses = append(responses, testutil.Response{ExitCode: 41})
					want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
				}
				want = append(want, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"})
				if step != "remove" {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
				}
				if step == "start" {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
				}
				responses = append(responses, testutil.Response{ExitCode: 42})
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "42") || !strings.Contains(stderr, "Podman") || strings.Contains(stderr, "was restored") || strings.Contains(stdout, "completed") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				assertRecoverySkipsImagePlanning(t, fakes)
			})
		}
	}
}

func TestRecoveryKeepsAReadyReplacementWhenBackupRemovalOrItsFinalStopFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, state := range []struct {
			name       string
			wasRunning string
			finalStop  bool
		}{
			{name: "backup removal previously running", wasRunning: "true"},
			{name: "backup removal previously stopped", wasRunning: "false"},
			{name: "final stop", wasRunning: "false", finalStop: true},
		} {
			t.Run(host.name+"/"+state.name, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := recoveryObjectResponses(t, true, true, false, state.wasRunning)
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"})
				want := [][]string{{"rm", "sandboxed-agents-backup.default.agent01"}}
				if state.finalStop {
					responses = append(responses, testutil.Response{})
					want = append(want, []string{"stop", "sandboxed-agents.default.agent01"})
				}
				responses = append(responses, testutil.Response{ExitCode: 42})
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "42") || strings.Contains(stderr, "restored") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if state.wasRunning == "false" && !strings.Contains(stderr, "still running") {
					t.Fatalf("missing remaining running state: %q", stderr)
				}
				if !state.finalStop && (!strings.Contains(stderr, "update interrupted") || !strings.Contains(stderr, "update agent01")) {
					t.Fatalf("missing recovery advice: %q", stderr)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
					t.Fatalf("changes=%v want=%v", changes, want)
				}
				assertRecoverySkipsImagePlanning(t, fakes)
			})
		}
	}
}

func TestRecoveryRestoresTheSandboxWhenEitherReadinessProbeNeverAnswers(t *testing.T) {
	for _, state := range []struct {
		probe, wasRunning string
		windows           bool
	}{
		{probe: "manager", wasRunning: "true"},
		{probe: "SSH", wasRunning: "false", windows: true},
	} {
		t.Run(state.probe, func(t *testing.T) {
			fakes, fixture, sshDir, hostState := sshSetupHost(t, state.windows)
			checkSSH := installUpdateSSHFixture(t, sshDir, hostState)
			responses := recoveryObjectResponses(t, true, true, false, state.wasRunning)
			probe := testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}
			if state.probe == "manager" {
				probe = testutil.Response{ExitCode: 42, RepeatForArgs: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}}
			}
			responses = append(responses, probe, testutil.Response{}, testutil.Response{})
			want := [][]string{{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, {"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}}
			if state.wasRunning == "true" {
				responses = append(responses, testutil.Response{})
				want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
			}
			scriptUpdate(t, fakes, state.windows, responses)
			if state.probe == "SSH" {
				fakes.Script("ssh-keyscan", testutil.Response{ExitCode: 42, RepeatForArgs: []string{"-T", "1", "-t", "ed25519", "-p", "2300", "127.0.0.1"}})
			}
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "readiness") || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "deadline exceeded") || strings.Contains(stdout, "completed") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes=%v want=%v", changes, want)
			}
			checkSSH()
			assertRecoverySkipsImagePlanning(t, fakes)
		})
	}
}

func TestUpdateRunsNormallyOnTheNextCallAfterRestoringAnInterruptedUpdate(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := recoveryObjectResponses(t, false, false, true, "")
			responses = append(responses, testutil.Response{})
			if host.windows {
				responses = append(healthyWindowsPodman(), responses[1:]...)
			}
			second := updateObjectResponses(t, true, "old-image", "", "")
			second = append(second, updateAllCurrentImage("")...)
			second = append(second, successfulRunningUpdateResponses()...)
			if host.windows {
				second = append(healthyWindowsPodman(), second[1:]...)
			}
			fakes.Script("podman", append(responses, second...)...)
			fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t)})
			_, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "restored") {
				t.Fatalf("first status=%d stderr=%q", status, stderr)
			}
			before := len(fakes.Calls("podman"))
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is updated.") {
				t.Fatalf("second status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			changes := updateAllSandboxChanges(t, fakes, "agent01")
			if len(changes) != 6 || len(fakes.Calls("podman")) <= before {
				t.Fatalf("normal second update missing: %v", changes)
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestRecoveryRestoresAReplacementWhoseSSHPortCannotBeRead(t *testing.T) {
	fakes, fixture := resourceLimitHost(t, false)
	responses := recoveryObjectResponses(t, true, true, false, "false")
	responses = updateObjectLabels(t, responses, "sandboxed-agents.default.agent01", func(labels map[string]any) { delete(labels, sandbox.SSHPortLabel) })
	responses = append(responses, testutil.Response{}, testutil.Response{})
	scriptUpdate(t, fakes, false, responses)
	_, stderr, status := runCLI(t, fixture, "update", "agent01")
	if status == 0 || !strings.Contains(stderr, "ssh-port") || !strings.Contains(stderr, "restored") {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	want := [][]string{{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, {"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}}
	if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes=%v want=%v", changes, want)
	}
	assertRecoverySkipsImagePlanning(t, fakes)
}
