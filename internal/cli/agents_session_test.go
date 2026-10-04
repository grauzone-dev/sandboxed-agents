package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAgentSessionStopsWithoutATerminalOrEnabledAgent(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			if fixture == "windows" {
				responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
			}
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{Stdout: "no agent session was running\n"})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, "agents", "session", "agent01", "codex", "--stop")
			if status != 0 || stdout != "no agent session was running\n" || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
			want := []string{"exec", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "session", "agent01", "codex", "--stop"}
			if len(fakes.Calls("podman")) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) || !reflect.DeepEqual(calls[len(calls)-2].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}) {
				t.Fatalf("calls=%v", calls)
			}
			for _, call := range calls {
				if strings.Contains(strings.Join(call.Args, " "), "check-enabled") {
					t.Fatal("stop checked whether the agent is enabled")
				}
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestAgentSessionBoundsQueriesAndForwardsUnboundedSessionStreams(t *testing.T) {
	for _, failAt := range []string{"", "version", "check-enabled"} {
		for _, failure := range []error{errors.New("cannot start"), context.Canceled, context.DeadlineExceeded} {
			if failAt == "" && failure != context.Canceled {
				continue
			}
			t.Run(failAt+"/"+failure.Error(), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := sandboxObjectResponses(&owner, true, nil, nil)
				if failAt == "" {
					responses = append(responses, testutil.Response{WantStdin: "session input\x00\r\n", Stdout: "session output\x00\r\n", Stderr: "session diagnostic\n"})
				}
				fakes.Script("podman", responses...)
				var stdout, stderr bytes.Buffer
				queries, sessions := 0, 0
				status := -1
				withInteractiveTerminal(t, func() {
					run := func(ctx context.Context, request process.Request) (int, error) {
						query := ""
						if slices.Contains(request.Args, "version") {
							query = "version"
						} else if slices.Contains(request.Args, "check-enabled") {
							query = "check-enabled"
						}
						if query != "" {
							queries++
							deadline, ok := ctx.Deadline()
							if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
								t.Fatalf("deadline=%v ok=%t", deadline, ok)
							}
							if query == failAt {
								return 0, failure
							}
							if query == "version" {
								io.WriteString(request.Streams.Stdout, "sandboxed-agents-manager test\n")
							} else {
								io.WriteString(request.Streams.Stdout, "true\n")
							}
							return 0, nil
						}
						if slices.Contains(request.Args, "session") {
							sessions++
							if _, ok := ctx.Deadline(); ok {
								t.Fatal("agent session has a time limit")
							}
							if request.Streams.Stdin != os.Stdin || request.Streams.Stdout != os.Stdout || request.Streams.Stderr != &stderr {
								t.Fatalf("session streams=%+v", request.Streams)
							}
							request.Streams.Stdin = strings.NewReader("session input\x00\r\n")
							request.Streams.Stdout = &stdout
						}
						return platform.Run(ctx, request)
					}
					status = cli.RunWithHost([]string{"agents", "session", "agent01", "codex"}, os.Stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: run})
				})
				if failAt == "" {
					if status != 0 || sessions != 1 || queries != 2 || stdout.String() != "session output\x00\r\n" || stderr.String() != "session diagnostic\n" {
						t.Fatalf("status=%d stdout=%q stderr=%q sessions=%d queries=%d", status, stdout.String(), stderr.String(), sessions, queries)
					}
				} else {
					wantQueries := 1
					if failAt == "check-enabled" {
						wantQueries = 2
					}
					if status != 1 || sessions != 0 || queries != wantQueries || stdout.Len() != 0 || !strings.Contains(stderr.String(), "manager does not answer") || !strings.Contains(stderr.String(), "check agent01") || !strings.Contains(stderr.String(), "restart agent01") {
						t.Fatalf("status=%d stdout=%q stderr=%q sessions=%d queries=%d", status, stdout.String(), stderr.String(), sessions, queries)
					}
				}
				if len(fakes.Calls("podman")) != len(responses) {
					t.Fatal(fakes.Calls("podman"))
				}
			})
		}
	}
}

func TestAgentSessionRequiresBothTerminalInputAndSuppliedOutput(t *testing.T) {
	for _, missing := range []string{"stdin", "stdout", "buffer"} {
		t.Run(missing, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{Stdout: "true"})
			fakes.Script("podman", responses...)
			var stdout, stderr bytes.Buffer
			status := -1
			withInteractiveTerminal(t, func() {
				file, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				var output io.Writer = os.Stdout
				switch missing {
				case "stdin":
					os.Stdin = file
				case "stdout":
					output = file
				case "buffer":
					output = &stdout
				}
				status = cli.RunWithHost([]string{"agents", "session", "agent01", "codex"}, output, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run})
			})
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "interactive terminal") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout.String(), stderr.String(), fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentSessionRejectsUsageAndUnknownAgentsBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			args    []string
			message string
		}{
			{nil, "missing sandbox name"},
			{[]string{"agent01"}, "missing agent name"},
			{[]string{".bad", "codex"}, "invalid sandbox name"},
			{[]string{"agent01", "unknown"}, "valid agents: claude, codex, copilot, opencode"},
			{[]string{"agent01", "unknown", "--stop"}, "valid agents: claude, codex, copilot, opencode"},
			{[]string{"agent01", "codex", "extra"}, "unexpected argument"},
			{[]string{"agent01", "codex", "--force"}, "unknown option"},
			{[]string{"agent01", "codex", "--stop", "--stop"}, "unknown option"},
		} {
			t.Run(fixture+"/"+strings.Join(test.args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, append([]string{"agents", "session"}, test.args...)...)
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
			})
		}
	}
}

func TestAgentSessionReportsTheEarliestFailureAndStartsNoSession(t *testing.T) {
	owner, foreign, missing := "default", "other", ""
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, stop := range []bool{false, true} {
			for _, test := range []struct {
				name             string
				container        *string
				running          bool
				volumes          map[string]string
				backup           *string
				manager, enabled *testutil.Response
				message          string
			}{
				{name: "unknown", message: "does not exist"},
				{name: "volumes only", volumes: map[string]string{"home": owner}, message: "up agent01"},
				{name: "foreign kept volume", volumes: map[string]string{"home": foreign}, message: "owner conflict"},
				{name: "foreign container", container: &foreign, backup: &owner, message: "owner conflict"},
				{name: "unlabelled container", container: &missing, message: "owner conflict"},
				{name: "foreign volume before update", container: &owner, volumes: map[string]string{"home": foreign}, backup: &owner, message: "owner conflict"},
				{name: "foreign backup", container: &owner, backup: &foreign, message: "owner conflict"},
				{name: "update before stopped", container: &owner, backup: &owner, message: "update agent01"},
				{name: "stopped before disabled and terminal", container: &owner, message: "start agent01"},
				{name: "manager before disabled and terminal", container: &owner, running: true, manager: &testutil.Response{ExitCode: 125}, message: "manager does not answer"},
				{name: "malformed manager before terminal", container: &owner, running: true, manager: &testutil.Response{Stdout: "not manager"}, message: "manager does not answer"},
				{name: "disabled before terminal", container: &owner, running: true, manager: &testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, enabled: &testutil.Response{Stdout: "false"}, message: "agents enable agent01 codex"},
				{name: "terminal after enabled", container: &owner, running: true, manager: &testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, enabled: &testutil.Response{Stdout: "true"}, message: "interactive terminal"},
				{name: "invalid enabled state", container: &owner, running: true, manager: &testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, enabled: &testutil.Response{Stdout: "null"}, message: "manager does not answer"},
				{name: "malformed enabled state", container: &owner, running: true, manager: &testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, enabled: &testutil.Response{Stdout: "{}"}, message: "manager does not answer"},
				{name: "failed enabled query", container: &owner, running: true, manager: &testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, enabled: &testutil.Response{ExitCode: 1}, message: "manager does not answer"},
			} {
				if stop && test.enabled != nil {
					continue
				}
				t.Run(fmt.Sprintf("%s/stop=%t/%s", fixture, stop, test.name), func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
					if fixture == "windows" {
						responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
					}
					if test.manager != nil {
						responses = append(responses, *test.manager)
					}
					if test.enabled != nil {
						responses = append(responses, *test.enabled)
					}
					fakes.Script("podman", responses...)
					args := []string{"agents", "session", "agent01", "codex"}
					if stop {
						args = append(args, "--stop")
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || len(fakes.Calls("podman")) != len(responses) {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
					}
					if test.message == "manager does not answer" && (!strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || strings.Contains(stderr, "interactive terminal")) {
						t.Fatal(stderr)
					}
					for _, call := range shellOperationCalls(t, fixture, fakes.Calls("podman")) {
						if strings.Contains(strings.Join(call.Args, " "), "agents session") {
							t.Fatalf("refusal started or attached to a session: %v", call)
						}
						if test.manager == nil && call.Args[0] != "container" && call.Args[0] != "volume" {
							t.Fatalf("unexpected work: %v", call)
						}
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestAgentSessionForwardsTheTerminalAndNormalizesExitStatus(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, stop := range []bool{false, true} {
			for _, code := range []int{0, 17, 125, 127} {
				t.Run(fmt.Sprintf("%s/stop=%t/status=%d", fixture, stop, code), func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					owner := "default"
					responses := sandboxObjectResponses(&owner, true, nil, nil)
					if fixture == "windows" {
						responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
					}
					responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"})
					if !stop {
						responses = append(responses, testutil.Response{Stdout: "true"})
					}
					responses = append(responses, testutil.Response{Stderr: "session diagnostic\n", ExitCode: code})
					fakes.Script("podman", responses...)
					args := []string{"agents", "session", "agent01", "codex"}
					if stop {
						args = append(args, "--stop")
					}
					var stderr bytes.Buffer
					status := 0
					withInteractiveTerminal(t, func() {
						command := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
						command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE="+fixture)
						command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, &stderr
						if err := command.Run(); err != nil {
							if exit, ok := err.(*exec.ExitError); ok {
								status = exit.ExitCode()
								return
							}
							t.Fatal(err)
						}
					})
					wantStatus := 0
					if code != 0 {
						wantStatus = 1
					}
					if status != wantStatus || !strings.Contains(stderr.String(), "session diagnostic") {
						t.Fatalf("status=%d stderr=%q", status, stderr.String())
					}
					calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
					want := []string{"exec", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent"}
					if !stop {
						want = append(want, "--interactive", "--tty")
					}
					want = append(want, "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "session", "agent01", "codex")
					if stop {
						want = append(want, "--stop")
					}
					if len(fakes.Calls("podman")) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
						t.Fatalf("calls=%v", calls)
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestAgentSessionExplainsUsageWithoutPodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, fixture, "agents", "session", "--help")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Usage: sandboxed-agents agents session NAME AGENT [--stop]") || !strings.Contains(stdout, "Ctrl-b d") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentSessionStatusShowsTheManagersSessionState(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, report := range []string{
			"Agent codex is enabled (version 1.2.3).\nSign-in state: unknown.\nAgent session: running.\n",
			"Agent codex is enabled (version 1.2.3).\nSign-in state: unknown.\nAgent session: not running.\n",
			"Agent codex is not enabled.\nAgent session: not running.\n",
		} {
			t.Run(fixture+"/"+report, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := sandboxObjectResponses(&owner, true, nil, nil)
				if fixture == "windows" {
					responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
				}
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{Stdout: report})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "agents", "status", "agent01", "codex")
				if status != 0 || stdout != report || stderr != "" || len(fakes.Calls("podman")) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				want := []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "status", "codex"}
				if !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatal(calls)
				}
			})
		}
	}
}

func TestWindowsAgentSessionUsesTheSelectedTargetAndControllerGroup(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	for _, name := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
		t.Setenv(name, "unchecked")
	}
	fakes := testutil.NewFakePrograms(t)
	owner := "team-a"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	objects := sandboxObjectResponses(&owner, true, nil, nil)
	for i := range objects {
		objects[i].Stdout = strings.ReplaceAll(objects[i].Stdout, ".default.", ".team-a.")
	}
	responses = append(responses, objects...)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{})
	for i := range responses {
		responses[i].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "agents", "session", "agent01", "codex", "--stop")
	if status != 0 || stdout != "" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	operations := windowsOperationCalls(t, fakes.Calls("podman")[2:], "podman-machine-default")
	for _, call := range operations {
		if !strings.Contains(strings.Join(call.Args, " "), ".team-a.agent01") || strings.Contains(strings.Join(call.Args, " "), ".default.") {
			t.Fatalf("wrong controller group: %v", call)
		}
	}
	if len(fakes.Calls("podman")) != len(responses) {
		t.Fatal(operations)
	}
}
