package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestRunAgentPassesArgumentsStreamsAndExitStatus(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, code := range []int{0, 7, 125, 127} {
			t.Run(fmt.Sprintf("%s/%d", fixture, code), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, nil, nil)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3:3], responses...)
				}
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{WantStdin: "input\x00\r\n", Stdout: "output\x00\r\n", Stderr: "agent diagnostic\n", ExitCode: code})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLIWithInput(t, "", fixture, strings.NewReader("input\x00\r\n"), "agents", "run", "agent01", "codex", "--help", "-x", "value", "--", "--y", "", "two words", "$(touch /tmp/evil)")
				if status != code || stdout != "output\x00\r\n" || stderr != "agent diagnostic\n" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				want := []testutil.Call{
					{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
					{Args: []string{"exec", "--interactive", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "run", "agent01", "codex", "--help", "-x", "value", "--", "--y", "", "two words", "$(touch /tmp/evil)"}},
				}
				if len(fakes.Calls("podman")) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2:], want) {
					t.Fatalf("calls=%v want final calls=%v", calls, want)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestRunAgentRejectsUsageAndUnknownNamesBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			args    []string
			message string
		}{
			{[]string{"agents", "run"}, "missing sandbox name"},
			{[]string{"agents", "run", "agent01"}, "missing agent name"},
			{[]string{"agents", "run", ".bad", "codex"}, "invalid sandbox name"},
			{[]string{"agents", "run", "agent01", "unknown"}, "valid agents: claude, codex, copilot, opencode"},
		} {
			t.Run(fixture+"/"+strings.Join(test.args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, test.args...)
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
	for _, data := range []string{
		`{"schema_version":1,"entries":[{"name":"future","delivered":false,"command":"future","install":{"kind":"npm","package":"future"}},{"name":"codex","delivered":true,"command":"codex","install":{"kind":"npm","package":"@openai/codex"}}]}`,
		`{"schema_version":1,"entries":[{"name":"future","delivered":false,"command":"future","install":{"kind":"npm","package":"future"}}]}`,
	} {
		t.Run(data, func(t *testing.T) {
			catalog, err := agentcatalog.Load([]byte(data))
			if err != nil {
				t.Fatal(err)
			}
			fakes := testutil.NewFakePrograms(t)
			var stdout, stderr bytes.Buffer
			status := cli.RunWithCatalog([]string{"agents", "run", "agent01", "future"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `unknown agent "future"`) || strings.Contains(stderr.String(), "valid agents: future") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			message := "no valid agent names are available yet"
			if strings.Contains(data, "codex") {
				message = "valid agents: codex"
			}
			if !strings.Contains(stderr.String(), message) {
				t.Fatal(stderr.String())
			}
		})
	}
}

func TestRunAgentReportsTheFirstSandboxFailureWithoutRunning(t *testing.T) {
	owned, foreign, missing := "default", "other", ""
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name, message string
			container     *string
			running       bool
			volumes       map[string]string
			backup        *string
		}{
			{name: "unknown", message: "does not exist in this controller group"},
			{name: "volumes only", volumes: map[string]string{"home": owned}, message: "up agent01"},
			{name: "foreign volumes only", volumes: map[string]string{"home": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.home"},
			{name: "foreign container before stopped", container: &foreign, message: "owner conflict on sandboxed-agents.default.agent01"},
			{name: "missing owner before stopped", container: &missing, message: "owner conflict on sandboxed-agents.default.agent01"},
			{name: "foreign workspace before backup", container: &owned, volumes: map[string]string{"workspace": foreign}, backup: &owned, message: "owner conflict on sandboxed-agents.default.agent01.workspace"},
			{name: "missing home owner", container: &owned, volumes: map[string]string{"home": missing}, message: "owner conflict on sandboxed-agents.default.agent01.home"},
			{name: "foreign SSH volume", container: &owned, running: true, volumes: map[string]string{"ssh": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.ssh"},
			{name: "foreign backup before stopped", container: &owned, backup: &foreign, message: "owner conflict on sandboxed-agents-backup.default.agent01"},
			{name: "interrupted before stopped", container: &owned, backup: &owned, message: "update agent01"},
			{name: "backup only", backup: &owned, message: "update agent01"},
			{name: "stopped before manager and not enabled", container: &owned, message: "start agent01"},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3:3], responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "agents", "run", "agent01", "codex")
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || strings.Contains(stderr, "agents enable") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if strings.Contains(test.message, "owner conflict") && !strings.Contains(stderr, "Podman") {
					t.Fatal(stderr)
				}
				for _, call := range shellOperationCalls(t, fixture, fakes.Calls("podman")) {
					if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
						t.Fatalf("refusal started work: %v", call)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestRunAgentRefusesWhenTheManagerDoesNotAnswerBeforeCheckingSelection(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, response := range []testutil.Response{{ExitCode: 127, Stderr: "missing manager"}, {}, {Stdout: "sandboxed-agents-manager \n"}, {Stdout: "other-program test\n"}, {Stdout: "sandboxed-agents-manager test\nextra\n"}} {
			t.Run(fixture+"/"+response.Stdout+response.Stderr, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, nil, nil)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3:3], responses...)
				}
				responses = append(responses, response)
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "agents", "run", "agent01", "codex")
				if status != 1 || stdout != "" || !strings.Contains(stderr, "manager does not answer") || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || strings.Contains(stderr, "agents enable") || len(fakes.Calls("podman")) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}) {
					t.Fatal(calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestRunAgentReportsTheManagersNotEnabledMessage(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{ExitCode: 1, Stderr: "agent codex is not enabled; use sandboxed-agents agents enable agent01 codex\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "run", "agent01", "codex")
	if status != 1 || stdout != "" || stderr != "agent codex is not enabled; use sandboxed-agents agents enable agent01 codex\n" || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestRunAgentBoundsManagerQueriesAndKeepsRunsUnbounded(t *testing.T) {
	for _, failure := range []string{"", "start", "timeout", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			fakes.Script("podman", sandboxObjectResponses(&owned, true, nil, nil)...)
			queries, runs := 0, 0
			run := func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] != "exec" {
					return platform.Run(ctx, request)
				}
				if request.Args[len(request.Args)-1] == "version" {
					queries++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatalf("deadline=%v ok=%t", deadline, ok)
					}
					switch failure {
					case "start":
						return 0, errors.New("exec failed")
					case "timeout":
						return 0, context.DeadlineExceeded
					case "canceled":
						return 0, context.Canceled
					}
					io.WriteString(request.Streams.Stdout, "sandboxed-agents-manager test\n")
					return 0, nil
				}
				runs++
				if _, ok := ctx.Deadline(); ok {
					t.Fatal("agent run has a time limit")
				}
				return 7, nil
			}
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"agents", "run", "agent01", "codex"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: run})
			if queries != 1 || stdout.Len() != 0 {
				t.Fatalf("queries=%d stdout=%q", queries, stdout.String())
			}
			if failure == "" {
				if status != 7 || stderr.Len() != 0 || runs != 1 {
					t.Fatalf("status=%d stderr=%q runs=%d", status, stderr.String(), runs)
				}
			} else if status != 1 || !strings.Contains(stderr.String(), "manager does not answer") || runs != 0 {
				t.Fatalf("status=%d stderr=%q runs=%d", status, stderr.String(), runs)
			}
		})
	}
}

func TestWindowsRunAgentUsesOnlyTheSelectedTargetAndControllerGroup(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	for _, name := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
		t.Setenv(name, "unchecked")
	}
	fakes := testutil.NewFakePrograms(t)
	owned := "team-a"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	objects := sandboxObjectResponses(&owned, true, nil, nil)
	for i := range objects {
		objects[i].Stdout = strings.ReplaceAll(objects[i].Stdout, ".default.", ".team-a.")
	}
	responses = append(responses, objects...)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager test\n"}, testutil.Response{})
	for i := range responses {
		responses[i].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "agents", "run", "agent01", "codex")
	if status != 0 || stdout != "" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	operations := windowsOperationCalls(t, fakes.Calls("podman")[2:], "podman-machine-default")
	for _, call := range operations {
		if !strings.Contains(strings.Join(call.Args, " "), ".team-a.agent01") || strings.Contains(strings.Join(call.Args, " "), ".default.") {
			t.Fatalf("wrong controller group: %v", call)
		}
	}
	if len(fakes.Calls("podman")) != len(responses) || !strings.Contains(strings.Join(operations[len(operations)-1].Args, " "), "--user=1000:1000") {
		t.Fatal(operations)
	}
	assertNoSSH(t, fakes)
}
