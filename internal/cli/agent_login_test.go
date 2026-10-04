package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
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

func TestAgentLoginReportsDisabledBeforeMissingTerminal(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "false\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "login", "agent01", "copilot")
	if status != 1 || stdout != "" || !strings.Contains(stderr, "agents enable agent01 copilot") || strings.Contains(stderr, "terminal") || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
}

func TestAgentLoginInvalidUsageAndNamesNeverCallPodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			args    []string
			message string
		}{
			{nil, "sandbox name"},
			{[]string{"agent01"}, "agent name"},
			{[]string{"a/b", "copilot"}, "invalid sandbox name"},
			{[]string{"agent01", "unknown"}, "valid agents: claude, codex, copilot, opencode"},
			{[]string{"agent01", "claude"}, "valid workflows: console, subscription"},
			{[]string{"agent01", "codex"}, "valid workflows: api-key, chatgpt"},
			{[]string{"agent01", "codex", "unknown"}, "valid workflows: api-key, chatgpt"},
			{[]string{"agent01", "copilot", "unknown"}, "valid workflows: github"},
			{[]string{"agent01", "copilot", ""}, "unknown workflow"},
			{[]string{"agent01", "copilot", "github", "extra"}, "unexpected argument"},
			{[]string{"agent01", "copilot", "--force"}, "valid workflows: github"},
		} {
			t.Run(fixture+"/"+strings.Join(test.args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, append([]string{"agents", "login"}, test.args...)...)
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestAgentLoginReportsTheEarliestFailureAndStartsNoWorkflow(t *testing.T) {
	owner, foreign, missing := "default", "other", ""
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name             string
			container        *string
			running          bool
			volumes          map[string]string
			backup           *string
			manager, enabled *testutil.Response
			message          string
		}{
			{"unknown", nil, false, nil, nil, nil, nil, "does not exist"},
			{"volumes only", nil, false, map[string]string{"home": owner}, nil, nil, nil, "up agent01"},
			{"foreign kept volume", nil, false, map[string]string{"home": foreign}, nil, nil, nil, "owner conflict"},
			{"foreign container", &foreign, false, nil, &owner, nil, nil, "owner conflict"},
			{"unlabelled container", &missing, false, nil, nil, nil, nil, "owner conflict"},
			{"foreign volume before update and stopped", &owner, false, map[string]string{"home": foreign}, &owner, nil, nil, "owner conflict"},
			{"foreign backup", &owner, false, nil, &foreign, nil, nil, "owner conflict"},
			{"update before stopped", &owner, false, nil, &owner, nil, nil, "update agent01"},
			{"stopped before disabled and terminal", &owner, false, nil, nil, nil, nil, "start agent01"},
			{"manager before disabled and terminal", &owner, true, nil, nil, &testutil.Response{ExitCode: 125}, nil, "manager does not answer"},
			{"malformed manager before terminal", &owner, true, nil, nil, &testutil.Response{Stdout: "not JSON"}, nil, "manager does not answer"},
			{"null manager", &owner, true, nil, nil, &testutil.Response{Stdout: "null"}, nil, "manager does not answer"},
			{"disabled before terminal", &owner, true, nil, nil, &testutil.Response{Stdout: "[]"}, &testutil.Response{Stdout: "false"}, "agents enable agent01 copilot"},
			{"terminal after enabled", &owner, true, nil, nil, &testutil.Response{Stdout: "[]"}, &testutil.Response{Stdout: "true"}, "interactive terminal"},
			{"invalid enabled response", &owner, true, nil, nil, &testutil.Response{Stdout: "[]"}, &testutil.Response{Stdout: "null"}, "manager does not answer"},
			{"failed enabled query", &owner, true, nil, nil, &testutil.Response{Stdout: "[]"}, &testutil.Response{ExitCode: 1}, "manager does not answer"},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
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
				stdout, stderr, status := runCLI(t, fixture, "agents", "login", "agent01", "copilot")
				calls := fakes.Calls("podman")
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || len(calls) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
				}
				if test.message == "manager does not answer" && (!strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || strings.Contains(stderr, "terminal")) {
					t.Fatal(stderr)
				}
				if fixture == "windows" {
					calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
				}
				if test.manager != nil {
					offset := 1
					if test.enabled != nil {
						offset = 2
					}
					if !reflect.DeepEqual(calls[len(calls)-offset].Args, sessionQueryArgs()) {
						t.Fatalf("manager query=%v", calls)
					}
				} else {
					for _, call := range calls {
						if call.Args[0] != "container" && call.Args[0] != "volume" {
							t.Fatalf("unexpected process: %v", call)
						}
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func withAgentLoginTerminal(t *testing.T, action func()) {
	t.Helper()
	input := shellTerminal(t)
	output := input
	if runtime.GOOS == "windows" {
		var err error
		output, err = os.OpenFile("CONOUT$", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { output.Close() })
	}
	originalInput, originalOutput := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = input, output
	defer func() { os.Stdin, os.Stdout = originalInput, originalOutput }()
	action()
}

func TestAgentLoginForwardsInteractiveWorkflowAndNormalizesExitStatus(t *testing.T) {
	for _, test := range []struct{ agent, workflow string }{
		{"copilot", ""}, {"copilot", "github"}, {"claude", "subscription"}, {"claude", "console"}, {"codex", "chatgpt"}, {"codex", "api-key"}, {"opencode", ""}, {"opencode", "provider"},
	} {
		for _, exitCode := range []int{0, 17} {
			t.Run(test.agent+"/"+test.workflow+"/"+fmt.Sprint(exitCode), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := sandboxObjectResponses(&owner, true, nil, nil)
				responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "true\n"}, testutil.Response{Stdout: "Login instructions\nworkflow output\n", Stderr: "workflow diagnostic\n", ExitCode: exitCode})
				fakes.Script("podman", responses...)
				args := []string{"agents", "login", "agent01", test.agent}
				if test.workflow != "" {
					args = append(args, test.workflow)
				}
				var stdout, stderr bytes.Buffer
				status := -1
				withAgentLoginTerminal(t, func() {
					run := func(ctx context.Context, request process.Request) (int, error) {
						if slices.Contains(request.Args, "login") {
							if request.Streams.Stdin != os.Stdin || request.Streams.Stdout != os.Stdout || request.Streams.Stderr != &stderr {
								t.Fatalf("workflow streams=%+v", request.Streams)
							}
							request.Streams.Stdout = &stdout
						}
						return platform.Run(ctx, request)
					}
					status = cli.RunWithHost(args, os.Stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: run})
				})
				wantStatus := 0
				if exitCode != 0 {
					wantStatus = 1
				}
				if status != wantStatus || stdout.String() != "Login instructions\nworkflow output\n" || !strings.Contains(stderr.String(), "workflow diagnostic") {
					t.Fatalf("status=%d stdout=%s stderr=%s", status, &stdout, &stderr)
				}
				workflow := test.workflow
				if workflow == "" {
					if test.agent == "copilot" {
						workflow = "github"
					} else {
						workflow = "provider"
					}
				}
				calls := fakes.Calls("podman")
				wantCheck := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "check-enabled", test.agent}
				wantWorkflow := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-it", "--env", "SANDBOXED_AGENTS_SANDBOX=agent01", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "login", test.agent, workflow}
				if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2].Args, wantCheck) || !reflect.DeepEqual(calls[len(calls)-1].Args, wantWorkflow) {
					t.Fatalf("calls=%v", calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestAgentLoginNeedsBothTerminalInputAndOutput(t *testing.T) {
	for _, missing := range []string{"stdin", "stdout"} {
		t.Run(missing, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "true"})
			fakes.Script("podman", responses...)
			var stdout, stderr bytes.Buffer
			status := -1
			withAgentLoginTerminal(t, func() {
				pipe, err := os.Open(os.DevNull)
				if err != nil {
					t.Fatal(err)
				}
				defer pipe.Close()
				if missing == "stdin" {
					os.Stdin = pipe
				} else {
					os.Stdout = pipe
				}
				status = cli.RunWithHost([]string{"agents", "login", "agent01", "copilot"}, os.Stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: platform.Run})
			})
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "interactive terminal") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%s stderr=%s calls=%v", status, &stdout, &stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentLoginUsesSelectedWindowsTargetAndControllerGroup(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	for _, key := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
		t.Setenv(key, "unchecked")
	}
	owner := "team-a"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	objects := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
	for i := range objects {
		objects[i].Stdout = strings.ReplaceAll(objects[i].Stdout, ".default.", ".team-a.")
	}
	responses = append(responses, objects...)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "true"}, testutil.Response{})
	for i := range responses {
		responses[i].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	var stderr bytes.Buffer
	status := -1
	withAgentLoginTerminal(t, func() {
		status = cli.RunWithWindowsHost([]string{"agents", "login", "agent01", "copilot"}, os.Stdout, &stderr, "test", "assets", platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true})
	})
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%s", status, &stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) {
		t.Fatalf("calls=%v", calls)
	}
	operations := windowsOperationCalls(t, calls[2:], "podman-machine-default")
	want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-it", "--env", "SANDBOXED_AGENTS_SANDBOX=agent01", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "login", "copilot", "github"}
	if !reflect.DeepEqual(operations[len(operations)-1].Args, want) {
		t.Fatalf("workflow=%v", operations[len(operations)-1])
	}
	assertNoSSH(t, fakes)
}

func TestAgentLoginHandlesAdditionalAndUndeliveredCatalogEntries(t *testing.T) {
	for _, test := range []struct {
		delivered bool
		workflows string
		message   string
	}{
		{false, `{"provider":["sign-in"]}`, "no valid agent names are available yet"},
		{true, `{}`, "no valid workflow names are available yet"},
		{true, `{"provider":["sign-in","--kind","example"]}`, ""},
	} {
		t.Run(fmt.Sprint(test.delivered)+test.workflows, func(t *testing.T) {
			catalog, err := agentcatalog.Load([]byte(fmt.Sprintf(`{"schema_version":1,"entries":[{"name":"fifth","delivered":%t,"command":"example-cli","install":{"kind":"npm","package":"example-cli"},"login_workflows":%s}]}`, test.delivered, test.workflows)))
			if err != nil {
				t.Fatal(err)
			}
			fakes := testutil.NewFakePrograms(t)
			var stdout, stderr bytes.Buffer
			responses := []testutil.Response{}
			if test.message == "" {
				owner := "default"
				responses = sandboxObjectResponses(&owner, true, nil, nil)
				responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "true"}, testutil.Response{})
				fakes.Script("podman", responses...)
			}
			status := -1
			withAgentLoginTerminal(t, func() {
				status = cli.RunWithCatalog([]string{"agents", "login", "agent01", "fifth"}, os.Stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
			})
			calls := fakes.Calls("podman")
			if test.message != "" {
				if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.message) || len(calls) != 0 {
					t.Fatalf("status=%d stdout=%s stderr=%s calls=%v", status, &stdout, &stderr, calls)
				}
			} else {
				want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-it", "--env", "SANDBOXED_AGENTS_SANDBOX=agent01", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "login", "fifth", "provider"}
				if status != 0 || stderr.Len() != 0 || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatalf("status=%d stderr=%s calls=%v", status, &stderr, calls)
				}
			}
		})
	}
}

func TestAgentLoginStopsWhenAnEnabledStateQueryCannotComplete(t *testing.T) {
	for _, failure := range []error{errors.New("cannot start"), context.Canceled, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"})
			fakes.Script("podman", responses...)
			queries := 0
			run := func(ctx context.Context, request process.Request) (int, error) {
				if slices.Contains(request.Args, "check-enabled") {
					queries++
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatalf("deadline=%v ok=%t", deadline, ok)
					}
					return 0, failure
				}
				return platform.Run(ctx, request)
			}
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"agents", "login", "agent01", "copilot"}, &stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: run})
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "manager does not answer") || !strings.Contains(stderr.String(), "check agent01") || !strings.Contains(stderr.String(), "restart agent01") || queries != 1 || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%s stderr=%s queries=%d calls=%v", status, &stdout, &stderr, queries, fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentLoginRejectsSuppliedOutputThatIsNotATerminal(t *testing.T) {
	for _, outputKind := range []string{"buffer", "file"} {
		t.Run(outputKind, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "true"})
			fakes.Script("podman", responses...)
			var stdout, stderr bytes.Buffer
			var output io.Writer = &stdout
			if outputKind == "file" {
				file, err := os.OpenFile(filepath.Join(t.TempDir(), "output"), os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				output = file
			}
			status := -1
			withAgentLoginTerminal(t, func() {
				status = cli.RunWithHost([]string{"agents", "login", "agent01", "copilot"}, output, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: platform.Run})
			})
			if status != 1 || !strings.Contains(stderr.String(), "interactive terminal") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stderr=%s calls=%v", status, &stderr, fakes.Calls("podman"))
			}
		})
	}
}
