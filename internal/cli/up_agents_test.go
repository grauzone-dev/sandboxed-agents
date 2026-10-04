package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpEnablesAgentsAfterCreatingAndStartingTheSandbox(t *testing.T) {
	fakes := linuxHost(t)
	responses := upObjectResponses(nil, false, nil, nil)
	responses = append(responses, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{Stdout: "container-id\n"}, testutil.Response{})
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: "Agent codex is enabled (version 1.2.3).\n"}, testutil.Response{Stdout: "Agent claude is enabled (version 2.3.4).\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--port", "2300", "--agents", "codex,claude")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") || !strings.Contains(stdout, "Agent codex is enabled") || !strings.Contains(stdout, "Agent claude is enabled") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	want := []testutil.Call{
		{Args: []string{"start", "sandboxed-agents.default.agent01"}},
		{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
		{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "codex"}},
		{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "claude"}},
	}
	if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-4:], want) {
		t.Fatalf("calls=%v want ending=%v", calls, want)
	}
	for _, call := range calls {
		if call.Args[0] == "create" && strings.Contains(strings.Join(call.Args, " "), "--agents") {
			t.Fatal("agent selection was passed to container creation", call)
		}
	}
	assertNoSSH(t, fakes)
}

func TestUpEnablesMissingAgentsOnAnExistingSandboxWithoutReinstalling(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			fakes := linuxHost(t)
			owned := "default"
			responses := upObjectResponses(&owned, running, map[string]string{"home": "default"}, nil)
			if !running {
				responses = append(responses, testutil.Response{})
			}
			fakes.Script("podman", responses...)
			home := t.TempDir()
			var installed []string
			app := manager.NewWithOptions("dev", func(_ context.Context, request process.Request) (int, error) {
				pkg := strings.TrimSuffix(request.Args[len(request.Args)-1], "@latest")
				installed = append(installed, pkg)
				path := filepath.Join(home, ".local", "lib", "node_modules", filepath.FromSlash(pkg), "package.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				return 0, os.WriteFile(path, []byte(`{"version":"1.2.3"}`), 0600)
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			if app.Run(context.Background(), []string{"agents", "enable", "codex"}, process.Streams{}) != 0 {
				t.Fatal("seed enable failed")
			}
			run := func(ctx context.Context, request process.Request) (int, error) {
				if len(request.Args) > 0 && request.Args[0] == "exec" {
					if request.Args[1] != "--user=0:0" || request.Args[2] != "sandboxed-agents.default.agent01" || request.Args[3] != "/usr/local/bin/sandboxed-agents-manager" {
						t.Fatal(request.Args)
					}
					return app.Run(ctx, request.Args[4:], request.Streams), nil
				}
				return platform.Run(ctx, request)
			}
			var stdout, stderr bytes.Buffer
			host := sshPortHostFixture("ssh-ports-free")
			host.Run = run
			status := cli.RunWithHost([]string{"up", "agent01", "--agents=codex,claude,codex"}, &stdout, &stderr, "test", "assets", host)
			if status != 0 || stderr.Len() != 0 || !reflect.DeepEqual(installed, []string{"@openai/codex", "@anthropic-ai/claude-code"}) || strings.Count(stdout.String(), "Agent codex is enabled") != 1 || !strings.Contains(stdout.String(), "Agent claude is enabled") {
				t.Fatalf("status=%d stdout=%q stderr=%q installs=%v", status, stdout.String(), stderr.String(), installed)
			}
			calls := fakes.Calls("podman")
			if len(calls) != len(responses) {
				t.Fatal(calls)
			}
			for _, call := range calls {
				if call.Args[0] == "create" || call.Args[0] == "stop" || call.Args[0] == "rm" {
					t.Fatal("existing sandbox changed", call)
				}
			}
		})
	}
}

func TestUpRejectsInvalidAgentSelectionsBeforeAnyPodmanCall(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, args := range [][]string{
			{"--agents", "codex,unknown"}, {"--agents=unknown,codex"},
			{"--agents"}, {"--agents="}, {"--agents", ""},
			{"--agents", ",codex"}, {"--agents", "codex,"}, {"--agents", "codex,,claude"},
			{"--agents", "codex", "--agents=claude"}, {"--agents", "--port", "2300"},
			{"--agents", "codex", "misplaced-workspace"},
		} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, append([]string{"up", "agent01"}, args...)...)
				if status == 0 || stdout != "" || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				if strings.Contains(strings.Join(args, " "), "unknown") && (!strings.Contains(stderr, `unknown agent "unknown"`) || !strings.Contains(stderr, "claude, codex, copilot, opencode")) {
					t.Fatal(stderr)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpChecksDeliveredAgentsInTheProvidedCatalog(t *testing.T) {
	catalog, err := agentcatalog.Load([]byte(`{"schema_version":1,"entries":[{"name":"future","delivered":false,"command":"future","install":{"kind":"npm","package":"future"}},{"name":"fifth","delivered":true,"command":"fifth","install":{"kind":"npm","package":"fifth"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	fakes := testutil.NewFakePrograms(t)
	var stdout, stderr bytes.Buffer
	status := cli.RunWithCatalog([]string{"up", "agent01", "--agents=fifth,future"}, &stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
	if status == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `unknown agent "future"; valid agents: fifth`) || len(fakes.Calls("podman")) != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestUpRejectsAWorkspaceAfterTheAgentOptionBeforePodman(t *testing.T) {
	fakes := linuxHost(t)
	workspace := workspaceFixture(t)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--agents", "codex", workspace)
	if status == 0 || stdout != "" || !strings.Contains(stderr, "unexpected argument") || len(fakes.Calls("podman")) != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
}

func TestUpKeepsSuccessfulAgentsAndAttemptsTheRestAfterInstallFailures(t *testing.T) {
	fakes := linuxHost(t)
	owned := "default"
	responses := upObjectResponses(&owned, true, nil, nil)
	responses = append(responses,
		testutil.Response{Stdout: "sandboxed-agents-manager dev\n"},
		testutil.Response{ExitCode: 1, Stderr: "npm install failed\n"},
		testutil.Response{Stdout: "sandboxed-agents-manager dev\n"},
		testutil.Response{Stdout: "Agent claude is enabled (version 1.2.3).\n"},
		testutil.Response{ExitCode: 1, Stderr: "npm install failed\n"},
		testutil.Response{Stdout: "sandboxed-agents-manager dev\n"},
	)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--agents=codex,claude,copilot")
	if status == 0 || !strings.Contains(stdout, "Sandbox agent01 is running") || !strings.Contains(stdout, "Agent claude is enabled") || strings.Contains(stdout, "Agent codex is enabled") || !strings.Contains(stderr, "codex, copilot") || !strings.Contains(stderr, "agents enable agent01 codex") || !strings.Contains(stderr, "agents enable agent01 copilot") || strings.Contains(stderr, "agents enable agent01 claude") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "copilot"}) {
		t.Fatal(calls)
	}
}

func TestUpReportsAnUnavailableManagerOnceAndLeavesTheSandboxRunning(t *testing.T) {
	for _, managerResponse := range []testutil.Response{{ExitCode: 127, Stderr: "manager missing\n"}, {}, {Stdout: "bad response\n"}} {
		t.Run(fmt.Sprint(managerResponse), func(t *testing.T) {
			fakes := linuxHost(t)
			owned := "default"
			responses := upObjectResponses(&owned, false, nil, nil)
			responses = append(responses, testutil.Response{}, managerResponse)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--agents", "codex,claude")
			if status == 0 || !strings.Contains(stdout, "Sandbox agent01 is running") || strings.Contains(stdout, "Agent ") || strings.Count(stderr, "manager does not answer") != 1 || strings.Count(strings.TrimSpace(stderr), "\n") != 0 || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "agents enable agent01 codex") || !strings.Contains(stderr, "agents enable agent01 claude") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2].Args, []string{"start", "sandboxed-agents.default.agent01"}) || calls[len(calls)-1].Args[len(calls[len(calls)-1].Args)-1] != "version" {
				t.Fatal(calls)
			}
		})
	}
}

func TestWindowsUpEnablesAgentsOnTheCheckedPodmanTarget(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
		t.Setenv(key, "ambient-other-target")
	}
	owned := "default"
	responses := append(healthyWindowsPodman(), upObjectResponses(&owned, false, nil, nil)[1:]...)
	responses = append(responses, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: "Agent codex is enabled (version 1.2.3).\n"})
	for index := range responses {
		responses[index].AbsentEnv = []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", "--agents=codex")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Agent codex is enabled") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) {
		t.Fatal(calls)
	}
	operations := windowsOperationCalls(t, calls[len(healthyWindowsPodman()):], "podman-machine-default")
	if !reflect.DeepEqual(operations[len(operations)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "codex"}) {
		t.Fatal(operations)
	}
}

func TestUpReportsAManagerThatStopsAnsweringDuringAgentInstallationOnce(t *testing.T) {
	fakes := linuxHost(t)
	owned := "default"
	responses := upObjectResponses(&owned, true, nil, nil)
	responses = append(responses,
		testutil.Response{Stdout: "sandboxed-agents-manager dev\n"},
		testutil.Response{Stdout: "Agent codex is enabled (version 1.2.3).\n"},
		testutil.Response{ExitCode: 127, Stderr: "manager disappeared\n"},
		testutil.Response{ExitCode: 127},
	)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--agents=codex,claude,copilot")
	if status == 0 || !strings.Contains(stdout, "Sandbox agent01 is running") || strings.Contains(stdout, "Agent ") || strings.Count(strings.TrimSpace(stderr), "\n") != 0 || !strings.Contains(stderr, "manager does not answer") || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "agents enable agent01 codex") || !strings.Contains(stderr, "agents enable agent01 claude") || !strings.Contains(stderr, "agents enable agent01 copilot") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if calls := fakes.Calls("podman"); len(calls) != len(responses) || calls[len(calls)-1].Args[len(calls[len(calls)-1].Args)-1] != "version" {
		t.Fatal(calls)
	}
}

func TestUpBoundsEachAgentInstallAndRechecksTheManagerWithAFreshContext(t *testing.T) {
	for _, reachable := range []bool{false, true} {
		t.Run(fmt.Sprint(reachable), func(t *testing.T) {
			fakes := linuxHost(t)
			owned := "default"
			fakes.Script("podman", upObjectResponses(&owned, true, nil, nil)...)
			probes := 0
			var attempted []string
			var previousAttempt context.Context
			run := func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] != "exec" {
					return platform.Run(ctx, request)
				}
				deadline, ok := ctx.Deadline()
				if !ok || ctx.Err() != nil {
					t.Fatalf("manager operation lacks a fresh deadline: %v", ctx.Err())
				}
				if request.Args[4] == "version" {
					probes++
					if time.Until(deadline) > 30*time.Second {
						t.Fatalf("manager probe deadline=%v", deadline)
					}
					if probes > 1 {
						if previousAttempt.Err() == nil {
							t.Fatal("attempt context was not canceled before the recheck")
						}
						if !reachable {
							return 127, nil
						}
					}
					fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager dev")
					return 0, nil
				}
				if time.Until(deadline) < 14*time.Minute || time.Until(deadline) > 15*time.Minute {
					t.Fatalf("installation deadline=%v", deadline)
				}
				agent := request.Args[6]
				attempted = append(attempted, agent)
				previousAttempt = ctx
				if agent == "codex" {
					return 0, context.DeadlineExceeded
				}
				fmt.Fprintln(request.Streams.Stdout, "Agent claude is enabled (version 1.2.3).")
				return 0, nil
			}
			host := sshPortHostFixture("ssh-ports-free")
			host.Run = run
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"up", "agent01", "--agents=codex,claude"}, &stdout, &stderr, "test", "assets", host)
			if status == 0 || probes != 2 || previousAttempt.Err() == nil || !strings.Contains(stdout.String(), "Sandbox agent01 is running") {
				t.Fatalf("status=%d probes=%d stdout=%q stderr=%q", status, probes, stdout.String(), stderr.String())
			}
			if reachable {
				if !reflect.DeepEqual(attempted, []string{"codex", "claude"}) || !strings.Contains(stdout.String(), "Agent claude is enabled") || !strings.Contains(stderr.String(), "agents enable agent01 codex") || strings.Contains(stderr.String(), "manager does not answer") {
					t.Fatalf("attempts=%v stdout=%q stderr=%q", attempted, stdout.String(), stderr.String())
				}
			} else if !reflect.DeepEqual(attempted, []string{"codex"}) || strings.Contains(stdout.String(), "Agent ") || !strings.Contains(stderr.String(), "manager does not answer") || !strings.Contains(stderr.String(), "check agent01") || !strings.Contains(stderr.String(), "agents enable agent01 claude") {
				t.Fatalf("attempts=%v stdout=%q stderr=%q", attempted, stdout.String(), stderr.String())
			}
		})
	}
}
