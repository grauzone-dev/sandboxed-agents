package cli_test

import (
	"bytes"
	"context"
	"errors"
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

func TestEnableAgentInstallsThroughTheSandboxManager(t *testing.T) {
	for _, agent := range []string{"copilot", "claude", "codex", "opencode"} {
		for _, report := range []string{"Enabled " + agent + " 1.2.3.\n", "Agent " + agent + " is already enabled at 1.2.3.\n"} {
			t.Run(agent+"/"+report, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{Stdout: report})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "enable", "agent01", agent)
				if status != 0 || stderr != "" || stdout != report {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				want := []testutil.Call{
					{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
					{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", agent}},
				}
				if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2:], want) {
					t.Fatalf("calls=%v want manager calls=%v", calls, want)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestEnableAgentRejectsUnknownAgentsBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, fixture, "agents", "enable", "agent01", "unknown")
			if status == 0 || stdout != "" || !strings.Contains(stderr, `unknown agent "unknown"`) || !strings.Contains(stderr, "claude, codex, copilot, opencode") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestEnableAgentRejectsUndeliveredCatalogEntriesBeforePodman(t *testing.T) {
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
			status := cli.RunWithCatalog([]string{"agents", "enable", "agent01", "future"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
			if status == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `unknown agent "future"`) || strings.Contains(stderr.String(), "valid agents: future") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout.String(), stderr.String(), fakes.Calls("podman"))
			}
			if strings.Contains(data, "codex") {
				if !strings.Contains(stderr.String(), "valid agents: codex") {
					t.Fatal(stderr.String())
				}
			} else if !strings.Contains(stderr.String(), "no valid agent names are available yet") {
				t.Fatal(stderr.String())
			}
		})
	}
}

func TestEnableAgentCanUseAnAdditionalCatalogEntry(t *testing.T) {
	catalog, err := agentcatalog.Load([]byte(`{"schema_version":1,"entries":[{"name":"fifth","delivered":true,"command":"fifth","install":{"kind":"npm","package":"fifth-agent"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: "Enabled fifth 2.0.0.\n"})
	fakes.Script("podman", responses...)
	var stdout, stderr bytes.Buffer
	status := cli.RunWithCatalog([]string{"agents", "enable", "agent01", "fifth"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
	calls := fakes.Calls("podman")
	if status != 0 || stderr.Len() != 0 || stdout.String() != "Enabled fifth 2.0.0.\n" || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "fifth"}) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout.String(), stderr.String(), calls)
	}
}

func TestEnableAgentBoundsManagerQueriesAndStopsOnProcessFailure(t *testing.T) {
	for _, failure := range []string{"start", "timeout", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			fakes.Script("podman", responses...)
			queries := 0
			run := func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] != "exec" {
					return platform.Run(ctx, request)
				}
				queries++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
					t.Fatalf("deadline=%v ok=%t", deadline, ok)
				}
				if !reflect.DeepEqual(request.Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}) {
					t.Fatal(request.Args)
				}
				switch failure {
				case "timeout":
					return 0, context.DeadlineExceeded
				case "canceled":
					return 0, context.Canceled
				default:
					return 0, errors.New("exec start failed")
				}
			}
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"agents", "enable", "agent01", "codex"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: run})
			if status == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "manager does not answer") || queries != 1 {
				t.Fatalf("status=%d stdout=%q stderr=%q queries=%d", status, stdout.String(), stderr.String(), queries)
			}
		})
	}
}

func TestEnableAgentReportsTheEarliestSandboxFailure(t *testing.T) {
	owned, foreign, unlabeled := "default", "other", ""
	for _, test := range []struct {
		name    string
		owner   *string
		running bool
		volumes map[string]string
		backup  *string
		message string
	}{
		{"unknown", nil, false, nil, nil, "does not exist"},
		{"volumes only", nil, false, map[string]string{"home": "default"}, nil, "up agent01"},
		{"foreign volumes only", nil, false, map[string]string{"home": "other"}, nil, "owner conflict on sandboxed-agents.default.agent01.home"},
		{"foreign container before backup and stopped", &foreign, false, nil, &owned, "owner conflict on sandboxed-agents.default.agent01"},
		{"unlabeled container before stopped", &unlabeled, false, nil, nil, "owner conflict on sandboxed-agents.default.agent01"},
		{"foreign volume before backup and stopped", &owned, false, map[string]string{"home": "other"}, &owned, "owner conflict on sandboxed-agents.default.agent01.home"},
		{"unlabeled volume before stopped", &owned, false, map[string]string{"workspace": ""}, nil, "owner conflict on sandboxed-agents.default.agent01.workspace"},
		{"foreign backup before stopped", &owned, false, nil, &foreign, "owner conflict on sandboxed-agents-backup.default.agent01"},
		{"backup before stopped", &owned, false, nil, &owned, "update agent01"},
		{"backup without container", nil, false, nil, &owned, "update agent01"},
		{"stopped before manager", &owned, false, nil, nil, "start agent01"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := sandboxObjectResponses(test.owner, test.running, test.volumes, test.backup)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "enable", "agent01", "codex")
			if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if strings.Contains(test.message, "owner conflict") && !strings.Contains(stderr, "Podman") {
				t.Fatal(stderr)
			}
			assertLifecycleReadOnly(t, fakes)
		})
	}
}

func TestEnableAgentRejectsInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{
			{"agents"}, {"agents", "enable"}, {"agents", "enable", "agent01"},
			{"agents", "enable", ".bad", "codex"}, {"agents", "enable", "--bad", "codex"},
			{"agents", "enable", "agent01", "codex", "extra"},
			{"agents", "enable", "agent01", "codex", "--force"},
			{"agents", "enable", "agent01", "codex", "--version", "1.0.0"},
		} {
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

func TestEnableAgentStopsAfterAnInstallationFailure(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{ExitCode: 1, Stderr: "npm install failed\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "enable", "agent01", "codex")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "npm install failed") || !strings.Contains(stderr, "exit status 1") || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
	assertNoSSH(t, fakes)
}

func TestWindowsEnableAgentKeepsManagerCallsOnTheSelectedTarget(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	for _, name := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
		t.Setenv(name, "unchecked")
	}
	owned := "default"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	responses = append(responses, sandboxObjectResponses(&owned, true, nil, nil)...)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: "Enabled codex 1.2.3.\n"})
	for index := range responses {
		responses[index].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "agents", "enable", "agent01", "codex")
	if status != 0 || stderr != "" || stdout != "Enabled codex 1.2.3.\n" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) {
		t.Fatal(calls)
	}
	operations := windowsOperationCalls(t, calls[2:], "podman-machine-default")
	want := []testutil.Call{
		{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
		{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "codex"}},
	}
	if !reflect.DeepEqual(operations[len(operations)-2:], want) {
		t.Fatal(operations)
	}
	assertNoSSH(t, fakes)
}

func TestWindowsEnableAgentRefusesAnUnavailableTarget(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"})
	stdout, stderr, status := runCLI(t, "windows", "agents", "enable", "agent01", "codex")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "sandboxed-agents check") || !reflect.DeepEqual(fakes.Calls("podman"), []testutil.Call{{Args: []string{"machine", "list", "--format", "json"}}}) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
}

func TestEnableAgentRefusesWhenTheManagerDoesNotAnswer(t *testing.T) {
	for _, response := range []testutil.Response{
		{ExitCode: 127, Stderr: "manager missing\n"},
		{Stdout: ""},
		{Stdout: "sandboxed-agents-manager \n"},
		{Stdout: "sandboxed-agents-manager v1.2.3"},
		{Stdout: "sandboxed-agents-manager v1.2.3\nextra\n"},
		{Stdout: "another-program v1.2.3\n"},
	} {
		t.Run(response.Stdout+response.Stderr, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
			responses = append(responses, response)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "enable", "agent01", "codex")
			if status == 0 || stdout != "" || !strings.Contains(stderr, "manager does not answer") || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}
