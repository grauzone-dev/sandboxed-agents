package cli_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func updateSessionQuery(name string) []string {
	args := sessionQueryArgs()
	args[2] = "sandboxed-agents.default." + name
	return args
}

func TestUpdateRefusesRunningAgentSessionsBeforeReplacingTheSandbox(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, testutil.Response{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"},{"name":"sandboxed-agents-claude","agent":"claude"}]`}, testutil.Response{ExitCode: 42})
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "sandboxed-agents-codex") || !strings.Contains(stderr, "sandboxed-agents-claude") || !strings.Contains(stderr, "--force") || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if !slices.Equal(podmanUpdateArgs(calls[len(calls)-1].Args), updateSessionQuery("agent01")) {
				t.Fatalf("refusal did not end at the session guard: %v", calls)
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); len(changes) != 0 {
				t.Fatalf("refused update changed the sandbox: %v", changes)
			}
			if len(fakes.Calls("ssh-keyscan")) != 0 {
				t.Fatal("refused update checked readiness")
			}
		})
	}
}

func TestForcedUpdateEndsReportedSessionsOnlyByStoppingTheOldContainer(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, testutil.Response{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"}]`})
			responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{})
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--with=none", "--force")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") || !strings.Contains(stdout, "ended") || !strings.Contains(stdout, "sandboxed-agents-codex") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var operations []string
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if args[0] == "exec" {
					if slices.Equal(args, updateSessionQuery("agent01")) {
						operations = append(operations, "sessions")
					} else if args[len(args)-1] != "version" {
						t.Fatalf("update ended a session through the manager: %v", args)
					}
				} else if slices.Contains([]string{"rename", "create", "stop", "start", "rm"}, args[0]) {
					operations = append(operations, args[0])
				}
			}
			if !slices.Equal(operations, []string{"sessions", "rename", "create", "stop", "start", "rm"}) {
				t.Fatalf("session and container operations=%v", operations)
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateRefusesWhenTheManagerCannotRuleOutRunningSessions(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, answer := range []testutil.Response{
			{ExitCode: 42, Stderr: "manager unavailable"},
			{Stdout: "not JSON"},
			{Stdout: "null"},
			{Stdout: `[{"name":"sandboxed-agents-codex"}]`},
		} {
			t.Run(host.name+"/"+answer.Stdout, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := updateObjectResponses(t, true, "old-image", "", "")
				responses = append(responses, updateAllCurrentImage("")...)
				responses = append(responses, answer)
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "cannot rule out running agent sessions") || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "--force") || strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				if !slices.Equal(podmanUpdateArgs(calls[len(calls)-1].Args), updateSessionQuery("agent01")) {
					t.Fatalf("queried anything after the refusing guard: %v", calls)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); len(changes) != 0 {
					t.Fatalf("unknown sessions changed the sandbox: %v", changes)
				}
			})
		}
	}
}

func TestForcedUpdateReportsUnknownSessionsAfterTheOldContainerStops(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, testutil.Response{ExitCode: 42, Stderr: "manager unavailable"})
			responses = append(responses, successfulRunningUpdateResponses()[1:]...)
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--force")
			for _, phrase := range []string{"agent01", "updated", "sessions that may have been running", "ended", "cannot be named"} {
				if !strings.Contains(stdout, phrase) {
					t.Errorf("missing %q in %q", phrase, stdout)
				}
			}
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			queryCount := 0
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Equal(args, updateSessionQuery("agent01")) {
					queryCount++
				} else if args[0] == "exec" && args[len(args)-1] != "version" {
					t.Fatalf("update ended a session itself: %v", args)
				}
			}
			if queryCount != 1 {
				t.Fatalf("forced update queried running sessions %d times", queryCount)
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateSessionGuardRunsAfterAllImageBuildsAndBeforeAnyReplacement(t *testing.T) {
	for _, answer := range []testutil.Response{
		{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"}]`},
		{ExitCode: 42},
	} {
		t.Run(answer.Stdout, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			responses = append(responses, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{})
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, answer)
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "--force") || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			built := false
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if args[0] == "build" {
					built = true
				}
				if slices.Equal(args, updateSessionQuery("agent01")) && !built {
					t.Fatal("guard ran before the image build")
				}
			}
			calls := fakes.Calls("podman")
			if !built || !slices.Equal(podmanUpdateArgs(calls[len(calls)-1].Args), updateSessionQuery("agent01")) || len(assertUpdateChangesPreserveData(t, fakes)) != 0 {
				t.Fatalf("guard did not follow builds before mutation: %v", calls)
			}
		})
	}
}

func TestForcedUpdatePreservesSessionsBeforeTheOldContainerStopsAndReportsThemAfter(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, known := range []bool{true, false} {
			for _, failure := range []string{"rename", "create", "stop", "start"} {
				t.Run(host.name+"/"+failure+"/"+map[bool]string{true: "known", false: "unknown"}[known], func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					checkSSH := installUpdateSSHFixture(t, sshDir, state)
					responses := updateObjectResponses(t, true, "old-image", "", "")
					responses = append(responses, updateAllCurrentImage("")...)
					answer := testutil.Response{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"}]`}
					if !known {
						answer = testutil.Response{ExitCode: 42}
					}
					responses = append(responses, answer)
					want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}}
					switch failure {
					case "rename":
						responses = append(responses, testutil.Response{ExitCode: 43})
					case "create":
						responses = append(responses, testutil.Response{}, testutil.Response{ExitCode: 43}, testutil.Response{}, testutil.Response{})
						want = append(want, []string{"create"}, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
					case "stop":
						responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{ExitCode: 43}, testutil.Response{}, testutil.Response{}, testutil.Response{})
						want = append(want, []string{"create"}, []string{"stop", "sandboxed-agents-backup.default.agent01"}, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}, []string{"start", "sandboxed-agents.default.agent01"})
					case "start":
						responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{ExitCode: 43}, testutil.Response{}, testutil.Response{}, testutil.Response{})
						want = append(want, []string{"create"}, []string{"stop", "sandboxed-agents-backup.default.agent01"}, []string{"start", "sandboxed-agents.default.agent01"}, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}, []string{"start", "sandboxed-agents.default.agent01"})
					}
					scriptUpdate(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--force")
					if status == 0 || !strings.Contains(stderr, "43") || failure != "rename" && !strings.Contains(stderr, "restored") || strings.Contains(stdout, "updated") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if failure == "start" {
						for _, phrase := range []string{"agent01", "ended", map[bool]string{true: "sandboxed-agents-codex", false: "cannot be named"}[known]} {
							if !strings.Contains(stdout, phrase) {
								t.Errorf("missing %q in %q", phrase, stdout)
							}
						}
					} else if failure == "stop" {
						for _, phrase := range []string{"agent01", "may have ended", "were not restarted", map[bool]string{true: "sandboxed-agents-codex", false: "cannot be named"}[known]} {
							if !strings.Contains(stdout, phrase) {
								t.Errorf("missing %q in %q", phrase, stdout)
							}
						}
					} else if strings.Contains(stdout, "ended") || strings.Contains(stdout, "sandboxed-agents-codex") {
						t.Fatalf("failure before stop claimed sessions ended: %q", stdout)
					}
					if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
						t.Fatalf("changes=%v want=%v", changes, want)
					}
					queries := 0
					for _, call := range fakes.Calls("podman") {
						args := podmanUpdateArgs(call.Args)
						if slices.Equal(args, updateSessionQuery("agent01")) {
							queries++
						} else if args[0] == "exec" {
							t.Fatalf("forced rollback called the manager to change sessions: %v", args)
						}
					}
					if queries != 1 {
						t.Fatalf("queries=%d", queries)
					}
					checkSSH()
				})
			}
		}
	}
}

func TestForcedUpdateReportsEndedSessionsWhenReadinessFailsAndRollsBack(t *testing.T) {
	fakes, fixture, sshDir, state := sshSetupHost(t, false)
	checkSSH := installUpdateSSHFixture(t, sshDir, state)
	responses := updateObjectResponses(t, true, "old-image", "", "")
	responses = append(responses, updateAllCurrentImage("")...)
	responses = append(responses, testutil.Response{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"}]`})
	responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{})
	responses = append(responses, testutil.Response{ExitCode: 42, RepeatForArgs: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}}, testutil.Response{}, testutil.Response{}, testutil.Response{})
	scriptUpdate(t, fakes, false, responses)
	stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--force")
	if status == 0 || !strings.Contains(stderr, "readiness") || !strings.Contains(stderr, "deadline exceeded") || !strings.Contains(stderr, "restored") || !strings.Contains(stdout, "ended") || !strings.Contains(stdout, "sandboxed-agents-codex") || strings.Contains(stdout, "updated") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := [][]string{
		{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
		{"stop", "sandboxed-agents-backup.default.agent01"}, {"start", "sandboxed-agents.default.agent01"},
		{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
		{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"}, {"start", "sandboxed-agents.default.agent01"},
	}
	if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes=%v want=%v", changes, want)
	}
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if args[0] == "exec" && !slices.Equal(args, updateSessionQuery("agent01")) && args[len(args)-1] != "version" {
			t.Fatalf("rollback restarted or stopped an agent session: %v", args)
		}
	}
	checkSSH()
}

func TestUpdateAllSkipsGuardedSandboxesUnlessForcedAndContinuesAfterThem(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, known := range []bool{true, false} {
			for _, force := range []bool{true, false} {
				t.Run(host.name+"/"+map[bool]string{true: "known", false: "unknown"}[known]+"/"+map[bool]string{true: "forced", false: "guarded"}[force], func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					if !host.windows {
						fixture = "linux-build"
					}
					responses := updateAllInventory("agent01", "agent02", "agent03")
					for _, name := range []string{"agent01", "agent02", "agent03"} {
						selection := ""
						if name == "agent02" {
							selection = "native"
						}
						responses = append(responses, updateAllObjects(t, name, true, "old-image", selection)...)
						if selection == "" {
							responses = append(responses, updateAllCurrentImage("")...)
						} else {
							responses = append(responses, updateAllOutdatedImage(selection, false)...)
						}
					}
					responses = append(responses, updateAllBuildImage("native", false)...)
					responses = append(responses, successfulRunningUpdateResponses()...)
					answer := testutil.Response{Stdout: `[{"name":"sandboxed-agents-codex","agent":"codex"}]`}
					if !known {
						answer = testutil.Response{ExitCode: 42}
					}
					responses = append(responses, answer)
					if force {
						responses = append(responses, successfulRunningUpdateResponses()[1:]...)
					}
					responses = append(responses, successfulRunningUpdateResponses()...)
					scriptUpdateAll(t, fakes, host.windows, responses)
					args := []string{"update", "--all"}
					if force {
						args = append(args, "--force")
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if !strings.Contains(stdout, "Sandbox agent01 is updated.") || !strings.Contains(stdout, "Sandbox agent03 is updated.") {
						t.Fatalf("bulk update did not continue: status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if force {
						if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent02 is updated.") || !strings.Contains(stdout, "ended") {
							t.Fatalf("forced bulk update: status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						phrase := map[bool]string{true: "sandboxed-agents-codex", false: "cannot be named"}[known]
						if !strings.Contains(stdout, phrase) {
							t.Fatalf("forced bulk update omitted ended sessions: %q", stdout)
						}
					} else {
						if status == 0 || !strings.Contains(stderr, "agent02") || !strings.Contains(stderr, "--force") || strings.Contains(stdout, "Sandbox agent02 is updated.") || len(updateAllSandboxChanges(t, fakes, "agent02")) != 0 {
							t.Fatalf("guarded sandbox was not skipped: status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						phrase := map[bool]string{true: "sandboxed-agents-codex", false: "cannot rule out running agent sessions"}[known]
						if !strings.Contains(stderr, phrase) {
							t.Fatalf("bulk refusal omitted sessions: %q", stderr)
						}
					}
					built, queries := false, 0
					for _, call := range fakes.Calls("podman") {
						podmanArgs := podmanUpdateArgs(call.Args)
						if podmanArgs[0] == "build" {
							if queries != 0 {
								t.Fatalf("bulk guard ran before all builds: %v", podmanArgs)
							}
							built = true
						}
						if podmanArgs[0] == "exec" && slices.Equal(podmanArgs[len(podmanArgs)-2:], []string{"sessions", "list"}) {
							if !built {
								t.Fatal("session query preceded the build")
							}
							queries++
						}
					}
					if queries != 3 {
						t.Fatalf("bulk session queries=%d", queries)
					}
					assertUpdateChangesPreserveData(t, fakes)
				})
			}
		}
	}
}

func TestUpdateCurrentAndStoppedSandboxesSkipTheSessionGuardEvenWithForce(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, current := range []bool{true, false} {
			for _, force := range []bool{true, false} {
				t.Run(host.name+"/"+map[bool]string{true: "current-running", false: "outdated-stopped"}[current]+"/"+map[bool]string{true: "force", false: "guard"}[force], func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					image := "old-image"
					if current {
						image = "current-base"
					}
					responses := updateObjectResponses(t, current, image, "", "")
					responses = append(responses, updateAllCurrentImage("")...)
					if !current {
						responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
					}
					scriptUpdate(t, fakes, host.windows, responses)
					args := []string{"update", "agent01"}
					if force {
						args = append(args, "--force")
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					want := map[bool]string{true: "already up to date", false: "updated"}[current]
					if status != 0 || stderr != "" || !strings.Contains(stdout, want) || strings.Contains(stdout, "ended") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					for _, call := range fakes.Calls("podman") {
						if slices.Equal(podmanUpdateArgs(call.Args), updateSessionQuery("agent01")) {
							t.Fatalf("queried a current or stopped sandbox: %v", call.Args)
						}
					}
					if current && len(assertUpdateChangesPreserveData(t, fakes)) != 0 {
						t.Fatal("current sandbox was changed")
					}
				})
			}
		}
	}
}

func TestUpdateWithoutSessionsBehavesTheSameWithAndWithoutForce(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			var firstOutput string
			var firstChanges [][]string
			for _, force := range []bool{false, true} {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := updateObjectResponses(t, true, "old-image", "", "")
				responses = append(responses, updateAllCurrentImage("")...)
				responses = append(responses, successfulRunningUpdateResponses()...)
				scriptUpdate(t, fakes, host.windows, responses)
				args := []string{"update", "agent01"}
				if force {
					args = append(args, "--force")
				}
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") || strings.Contains(stdout, "ended") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				changes := assertUpdateChangesPreserveData(t, fakes)
				if force && (stdout != firstOutput || !reflect.DeepEqual(changes, firstChanges)) {
					t.Fatalf("force changed a session-free update: stdout=%q first=%q changes=%v first=%v", stdout, firstOutput, changes, firstChanges)
				}
				firstOutput, firstChanges = stdout, changes
			}
		})
	}
}

func TestUpdateRejectsDuplicateForceAndInvalidForceCombinationsBeforeHostQueries(t *testing.T) {
	for _, args := range [][]string{
		{"update", "agent01", "--force", "--force"},
		{"update", "--all", "--force", "--force"},
		{"update", "--all", "--force", "--with", "native"},
		{"update", "agent01", "--force=true"},
		{"update", "agent01", "--force", "--all"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "linux-preflight", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}
