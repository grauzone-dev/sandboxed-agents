package cli_test

import (
	"bytes"
	"context"
	"errors"
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

func TestListShowsTheAgentSelectionOfARunningSandbox(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := listOneSandboxResponses("default", "agent01", &owned, true, nil, nil)
	responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
	responses = append(responses, testutil.Response{Stdout: "[\"codex\",\"claude\"]\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 running volume - - claude,codex -"
	if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "list"}) {
		t.Fatal(calls)
	}
	assertListReadOnly(t, fakes, false)
}

func TestListShowsAnAgentAfterEnableUsingTheSameCatalogAndHomeSelection(t *testing.T) {
	for _, outdated := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "outdated"}[outdated], func(t *testing.T) {
			catalog, err := agentcatalog.Load([]byte(`{"schema_version":1,"entries":[{"name":"fifth","delivered":true,"command":"fifth","install":{"kind":"npm","package":"@example/fifth"}}]}`))
			if err != nil {
				t.Fatal(err)
			}
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, map[string]string{"home": "default"}, nil)
			listResponses := listOneSandboxResponses("default", "agent01", &owned, true, map[string]string{"home": "default"}, nil)
			if outdated {
				listResponses[2].Stdout = strings.ReplaceAll(listResponses[2].Stdout, `"Image":"current-base"`, `"Image":"old-base"`)
			}
			listResponses = append(listResponses, listCurrentImageResponses("", "current-base", "current-base")...)
			responses = append(responses, listResponses...)
			fakes.Script("podman", responses...)
			home := t.TempDir()
			installs := 0
			app := manager.NewWithOptions("dev", func(_ context.Context, request process.Request) (int, error) {
				installs++
				if request.Name != "/usr/bin/npm" || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatal(request)
				}
				path := filepath.Join(home, ".local", "lib", "node_modules", "@example", "fifth", "package.json")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				return 0, os.WriteFile(path, []byte(`{"version":"1.2.3"}`), 0600)
			}, manager.Options{Home: home, Catalog: &catalog, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			run := func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] == "exec" {
					if request.Args[1] != "--user=0:0" || request.Args[2] != "sandboxed-agents.default.agent01" || request.Args[3] != "/usr/local/bin/sandboxed-agents-manager" {
						t.Fatal(request.Args)
					}
					return app.Run(ctx, request.Args[4:], request.Streams), nil
				}
				return platform.Run(ctx, request)
			}
			host := preflight.Host{Platform: "linux", Run: run}
			var stdout, stderr bytes.Buffer
			if status := cli.RunWithCatalog([]string{"agents", "enable", "agent01", "fifth"}, &stdout, &stderr, "test", "assets", host, catalog); status != 0 || stderr.Len() != 0 {
				t.Fatalf("enable status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			stdout.Reset()
			state := "running"
			if outdated {
				state += " (outdated)"
			}
			if status := cli.RunWithCatalog([]string{"list"}, &stdout, &stderr, "test", "assets", host, catalog); status != 0 || stderr.Len() != 0 || installs != 1 || !strings.Contains(strings.Join(strings.Fields(stdout.String()), " "), "agent01 "+state+" volume - - fifth ") {
				t.Fatalf("list status=%d stdout=%q stderr=%q installs=%d", status, stdout.String(), stderr.String(), installs)
			}

		})
	}
}

func TestListKeepsTheAgentSelectionUnavailableWhenTheManagerDoesNotAnswer(t *testing.T) {
	for _, response := range []testutil.Response{
		{ExitCode: 127, Stderr: "manager missing\n"},
		{}, {Stdout: `null`}, {Stdout: `{}`}, {Stdout: `not-json`},
		{Stdout: `["codex"]`, ExitCode: 1}, {Stdout: `["unknown"]`},
		{Stdout: `["codex\nspoofed"]`}, {Stdout: `["codex",1]`},
	} {
		t.Run(response.Stdout+response.Stderr, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := listOneSandboxResponses("default", "agent01", &owned, true, nil, nil)
			responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
			responses = append(responses, response)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 running volume - - - -"
			if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestListDistinguishesNoEnabledAgentsFromAnUnavailableSelection(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := listOneSandboxResponses("default", "agent01", &owned, running, nil, nil)
			responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
			want := "agent01 stopped volume - - - -"
			if running {
				responses = append(responses, testutil.Response{Stdout: "[]\n"})
				want = "agent01 running volume - - none -"
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), want) || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestWindowsListQueriesAgentsOnlyInTheSelectedGroupAndTarget(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
		t.Setenv(key, "ambient-other-target")
	}
	owned := "team-a"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	responses = append(responses, listOneSandboxResponses("team-a", "agent01", &owned, true, nil, nil)...)
	responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
	responses = append(responses, testutil.Response{Stdout: `["codex"]`})
	for index := 2; index < len(responses); index++ {
		responses[index].AbsentEnv = []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "list")
	if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 running volume - - codex -") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := windowsOperationCalls(t, fakes.Calls("podman")[2:], "podman-machine-default")
	if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"exec", "--user=0:0", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "list"}) {
		t.Fatal(calls)
	}
	assertListReadOnly(t, fakes, true)
}

func TestListBoundsTheManagerQueryAndToleratesProcessFailure(t *testing.T) {
	for _, failure := range []error{errors.New("start failed"), context.DeadlineExceeded, context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := listOneSandboxResponses("default", "agent01", &owned, true, nil, nil)
			responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
			fakes.Script("podman", responses...)
			queries := 0
			run := func(ctx context.Context, request process.Request) (int, error) {
				if request.Args[0] != "exec" {
					return platform.Run(ctx, request)
				}
				queries++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
					t.Fatalf("deadline=%v ok=%t", deadline, ok)
				}
				return 0, failure
			}
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"list"}, &stdout, &stderr, "test", "assets", preflight.Host{Platform: "linux", Run: run})
			if status != 0 || stderr.Len() != 0 || queries != 1 || !strings.Contains(strings.Join(strings.Fields(stdout.String()), " "), "agent01 running volume - - - -") {
				t.Fatalf("status=%d stdout=%q stderr=%q queries=%d", status, stdout.String(), stderr.String(), queries)
			}
		})
	}
}
