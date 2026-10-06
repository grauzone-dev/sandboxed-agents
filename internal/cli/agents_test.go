package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestEnableAgentInstallsThroughTheSandboxManager(t *testing.T) {
	for _, agent := range []string{"copilot", "claude", "codex", "opencode"} {
		for _, report := range []string{"Agent " + agent + " is enabled (version 1.2.3).\nPin: none.\n"} {
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

func TestAgentCommandsRejectUnknownAgentsBeforePodman(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
			for _, fixture := range []string{"sandbox-host", "windows"} {
				t.Run(fixture, func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					owned := "default"
					fakes.Script("podman", sandboxObjectResponses(&owned, false, nil, nil)...)
					stdout, stderr, status := runCLI(t, fixture, "agents", operation, "agent01", "unknown")
					if status == 0 || stdout != "" || !strings.Contains(stderr, `unknown agent "unknown"`) || !strings.Contains(stderr, "claude, codex, copilot, opencode") || len(fakes.Calls("podman")) != 0 {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
					}
					assertNoSSH(t, fakes)
				})
			}
		})
	}
}

func TestAgentCommandsRejectUndeliveredCatalogEntriesBeforePodman(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
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
					status := cli.RunWithCatalog([]string{"agents", operation, "agent01", "future"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
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
		})
	}
}

func TestEnableAgentCanUseAnAdditionalCatalogEntry(t *testing.T) {
	entries := []agentcatalog.Entry{}
	embedded := agentcatalog.Embedded()
	for _, name := range embedded.Names() {
		entry, _ := embedded.Find(name)
		entries = append(entries, entry)
	}
	entries = append(entries, agentcatalog.Entry{Name: "fifth", Delivered: true, Command: "fifth", Install: agentcatalog.Install{Kind: "npm", Package: "@example/fifth"}})
	data, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schema_version"`
		Entries       []agentcatalog.Entry `json:"entries"`
	}{1, entries})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := agentcatalog.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: "Agent fifth is enabled (version 2.0.0).\nPin: none.\n"})
	fakes.Script("podman", responses...)
	var stdout, stderr bytes.Buffer
	status := cli.RunWithCatalog([]string{"agents", "enable", "agent01", "fifth"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: platform.Run}, catalog)
	calls := fakes.Calls("podman")
	if status != 0 || stderr.Len() != 0 || stdout.String() != "Agent fifth is enabled (version 2.0.0).\nPin: none.\n" || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "fifth"}) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout.String(), stderr.String(), calls)
	}
}

func TestAgentCommandsBoundManagerQueriesAndStopOnProcessFailure(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
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
					status := cli.RunWithHost([]string{"agents", operation, "agent01", "codex"}, &stdout, &stderr, "fixture", "assets", preflight.Host{Platform: "linux", Run: run})
					if status == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "manager does not answer") || queries != 1 {
						t.Fatalf("status=%d stdout=%q stderr=%q queries=%d", status, stdout.String(), stderr.String(), queries)
					}
				})
			}
		})
	}
}

func TestAgentCommandsReportTheEarliestSandboxFailure(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
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
					stdout, stderr, status := runCLI(t, "sandbox-host", "agents", operation, "agent01", "codex")
					if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if strings.Contains(test.message, "owner conflict") && !strings.Contains(stderr, "Podman") {
						t.Fatal(stderr)
					}
					assertLifecycleReadOnly(t, fakes)
				})
			}
		})
	}
}

func TestEnableAndDisableAgentsRejectInvalidUsageBeforePodman(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "update"} {
		t.Run(operation, func(t *testing.T) {
			for _, fixture := range []string{"sandbox-host", "windows"} {
				for _, args := range [][]string{
					{"agents"}, {"agents", operation}, {"agents", operation, "agent01"},
					{"agents", operation, ".bad", "codex"}, {"agents", operation, "--bad", "codex"},
					{"agents", operation, "agent01", "codex", "extra"},
					{"agents", operation, "agent01", "codex", "--force"},
					{"agents", operation, "agent01", "codex", "--bad"},
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
		})
	}
}

func TestEnableAndDisableAgentsStopAfterAManagerRequestFailure(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "update"} {
		t.Run(operation, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{ExitCode: 1, Stderr: "manager request failed\n"})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", operation, "agent01", "codex")
			if status == 0 || stdout != "" || !strings.Contains(stderr, "manager request failed") || !strings.Contains(stderr, "exit status 1") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsAgentCommandsKeepManagerCallsOnTheSelectedTarget(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			for _, name := range []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
				t.Setenv(name, "unchecked")
			}
			owned := "team-a"
			responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
			objects := sandboxObjectResponses(&owned, true, nil, nil)
			for i := range objects {
				objects[i].Stdout = strings.ReplaceAll(objects[i].Stdout, ".default.", ".team-a.")
			}
			responses = append(responses, objects...)
			report := "Agent codex is enabled (version 1.2.3).\nPin: none.\n"
			if operation == "disable" {
				report = "Agent codex is disabled.\n"
			} else if operation == "update" {
				report = "Agent codex is updated (version 1.2.3).\nPin: none.\n"
			} else if operation == "status" {
				report = "Agent codex is enabled (version 1.2.3).\nPin: none.\nSign-in state: unknown.\n"
			}
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
			for index := range responses {
				responses[index].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "windows", "agents", operation, "agent01", "codex")
			if status != 0 || stderr != "" || stdout != report {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != len(responses) {
				t.Fatal(calls)
			}
			operations := windowsOperationCalls(t, calls[2:], "podman-machine-default")
			want := []testutil.Call{
				{Args: []string{"exec", "--user=0:0", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
				{Args: []string{"exec", "--user=0:0", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", operation, "codex"}},
			}
			if operation == "update" {
				want[1].Args = []string{"exec", "--user=0:0", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "update", "agent01", "codex"}
			}
			if !reflect.DeepEqual(operations[len(operations)-2:], want) {
				t.Fatal(operations)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsAgentCommandsRefuseAnUnavailableTarget(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			fakes.Script("podman", testutil.Response{Stdout: "[]"})
			stdout, stderr, status := runCLI(t, "windows", "agents", operation, "agent01", "codex")
			if status == 0 || stdout != "" || !strings.Contains(stderr, "sandboxed-agents check") || !reflect.DeepEqual(fakes.Calls("podman"), []testutil.Call{{Args: []string{"machine", "list", "--format", "json"}}}) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentCommandsRefuseWhenTheManagerDoesNotAnswer(t *testing.T) {
	for _, operation := range []string{"enable", "disable", "status", "update"} {
		t.Run(operation, func(t *testing.T) {
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
					stdout, stderr, status := runCLI(t, "sandbox-host", "agents", operation, "agent01", "codex")
					if status == 0 || stdout != "" || !strings.Contains(stderr, "manager does not answer") || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || len(fakes.Calls("podman")) != len(responses) {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
					}
					assertNoSSH(t, fakes)
				})
			}
		})
	}
}

func TestEnabledAgentSelectionSurvivesStopAndStart(t *testing.T) {
	home := t.TempDir()
	installs := 0
	runner := func(_ context.Context, r process.Request) (int, error) {
		installs++
		path := filepath.Join(home, ".local", "lib", "node_modules", "@openai", "codex", "package.json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return 1, err
		}
		return 0, os.WriteFile(path, []byte(`{"version":"1.2.3"}`), 0600)
	}
	options := manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }}
	enable := func(arguments ...string) {
		var stdout, stderr bytes.Buffer
		app := manager.NewWithOptions("test", runner, options)
		if status := app.Run(context.Background(), append([]string{"agents", "enable", "codex"}, arguments...), process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 || stdout.String() != "Agent codex is enabled (version 1.2.3).\nPin: 1.2.3.\n" {
			t.Fatalf("enable status=%d out=%s err=%s", status, &stdout, &stderr)
		}
	}
	enable("--version", "1.2.3")
	selectionPath := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
	before, err := os.ReadFile(selectionPath)
	if err != nil {
		t.Fatal(err)
	}
	var selection map[string]struct {
		Pin string `json:"pin"`
	}
	if err := json.Unmarshal(before, &selection); err != nil || selection["codex"].Pin != "1.2.3" {
		t.Fatalf("selection=%s err=%v", before, err)
	}
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, map[string]string{"home": "default"}, nil)
	responses = append(responses, testutil.Response{Stdout: "[]\n"}, testutil.Response{})
	responses = append(responses, sandboxObjectResponses(&owned, false, map[string]string{"home": "default"}, nil)...)
	responses = append(responses, testutil.Response{})
	fakes.Script("podman", responses...)
	for _, command := range []string{"stop", "start"} {
		_, stderr, status := runCLI(t, "sandbox-host", command, "agent01")
		if status != 0 || stderr != "" {
			t.Fatalf("%s status=%d err=%s", command, status, stderr)
		}
	}
	changes := []string{}
	for _, call := range fakes.Calls("podman") {
		switch call.Args[0] {
		case "container", "volume":
			if call.Args[1] != "exists" && call.Args[1] != "inspect" {
				t.Fatal("lifecycle changed home objects", call)
			}
		case "exec":
			if !reflect.DeepEqual(call.Args, sessionQueryArgs()) {
				t.Fatal("unexpected home operation", call)
			}
		case "stop", "start":
			changes = append(changes, call.Args[0])
		default:
			t.Fatal(fmt.Sprint("unexpected lifecycle operation: ", call))
		}
	}
	if !reflect.DeepEqual(changes, []string{"stop", "start"}) {
		t.Fatal(changes)
	}
	enable()
	after, err := os.ReadFile(selectionPath)
	if err != nil || !bytes.Equal(before, after) || installs != 1 {
		t.Fatalf("selection changed=%t installs=%d err=%v", !bytes.Equal(before, after), installs, err)
	}
	assertNoSSH(t, fakes)
}
