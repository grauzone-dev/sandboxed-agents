package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAgentLoginRunsTheCatalogWorkflowAsAgent(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("device code\n")
	requests := []process.Request{}
	run := func(_ context.Context, request process.Request) (int, error) {
		requests = append(requests, request)
		if request.Name == "/usr/bin/npm" {
			writeInstalledPackage(t, home, "@github/copilot", "1.2.3")
			return 0, nil
		}
		if stdout.Len() == 0 {
			t.Fatal("workflow started before login message")
		}
		return 0, nil
	}
	app := manager.NewWithOptions("test", run, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	if status := app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{}); status != 0 {
		t.Fatal(status)
	}
	requests = nil
	if status := app.Run(context.Background(), []string{"agents", "login", "copilot"}, process.Streams{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if len(requests) != 1 {
		t.Fatalf("requests=%v", requests)
	}
	request := requests[0]
	if request.Name != home+"/.local/bin/copilot" || !reflect.DeepEqual(request.Args, []string{"login", "--device-code"}) || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || request.Dir != home || request.Streams.Stdin != stdin || request.Streams.Stdout != &stdout || request.Streams.Stderr != &stderr {
		t.Fatalf("request=%+v", request)
	}
	if !strings.Contains(stdout.String(), "GitHub Copilot CLI signs in") {
		t.Fatal(stdout.String())
	}
}

func TestAgentLoginSelectsEachCatalogWorkflow(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, test := range []struct {
		agent, workflow, command string
		args                     []string
	}{
		{"copilot", "", "copilot", []string{"login", "--device-code"}},
		{"copilot", "github", "copilot", []string{"login", "--device-code"}},
		{"claude", "subscription", "claude", []string{"auth", "login"}},
		{"claude", "console", "claude", []string{"auth", "login", "--console"}},
		{"codex", "chatgpt", "codex", []string{"login", "--device-auth"}},
		{"codex", "api-key", "codex", []string{"login", "--with-api-key"}},
		{"opencode", "", "opencode", []string{"auth", "login"}},
		{"opencode", "provider", "opencode", []string{"auth", "login"}},
	} {
		t.Run(test.agent+"/"+test.workflow, func(t *testing.T) {
			home := t.TempDir()
			catalog := agentcatalog.Embedded()
			entry, _ := catalog.Find(test.agent)
			requests := []process.Request{}
			run := func(_ context.Context, r process.Request) (int, error) {
				if r.Name == "/usr/bin/npm" {
					writeInstalledPackage(t, home, entry.Install.Package, "1.2.3")
					return 0, nil
				}
				requests = append(requests, r)
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			if status := app.Run(context.Background(), []string{"agents", "enable", test.agent}, process.Streams{}); status != 0 {
				t.Fatal(status)
			}
			args := []string{"agents", "login", test.agent}
			if test.workflow != "" {
				args = append(args, test.workflow)
			}
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), args, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 {
				t.Fatalf("status=%d stderr=%s", status, &stderr)
			}
			if len(requests) != 1 || requests[0].Name != filepath.Join(home, ".local", "bin", test.command) || !reflect.DeepEqual(requests[0].Args, test.args) || stdout.String() != entry.LoginMessage+"\n" {
				t.Fatalf("requests=%v stdout=%q", requests, stdout.String())
			}
			if requests[0].User == nil || *requests[0].User != (process.Identity{UID: 1000, GID: 1000}) {
				t.Fatalf("identity=%v", requests[0].User)
			}
			wantEnv := []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin", "TERM=xterm-256color"}
			if !reflect.DeepEqual(requests[0].Env, wantEnv) {
				t.Fatalf("environment=%v", requests[0].Env)
			}
		})
	}
}

func TestAgentLoginRejectsInvalidNamesWithoutProcessesOrHomeWrites(t *testing.T) {
	for _, test := range []struct {
		args    []string
		message string
	}{
		{[]string{"agents", "login"}, "usage:"},
		{[]string{"agents", "login", "unknown"}, "valid agents: claude, codex, copilot, opencode"},
		{[]string{"agents", "login", "claude"}, "valid workflows: console, subscription"},
		{[]string{"agents", "login", "codex", "unknown"}, "valid workflows: api-key, chatgpt"},
		{[]string{"agents", "login", "copilot", "unknown"}, "valid workflows: github"},
		{[]string{"agents", "login", "copilot", ""}, "unknown workflow"},
		{[]string{"agents", "login", "copilot", "github", "extra"}, "usage:"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			home := t.TempDir()
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid login started a process")
				return 0, nil
			}, manager.Options{Home: home})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), test.args, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.message) {
				t.Fatalf("status=%d stdout=%s stderr=%s", status, &stdout, &stderr)
			}
			files, err := os.ReadDir(home)
			if err != nil || len(files) != 0 {
				t.Fatalf("files=%v err=%v", files, err)
			}
		})
	}
}

func TestAgentLoginNeverReadsHomeOrStartsAWorkflowAsRoot(t *testing.T) {
	for _, identity := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 0}, {UID: 0, GID: 1000}, {UID: 2000, GID: 2000}} {
		for _, operation := range []string{"login", "check-enabled"} {
			t.Run(fmt.Sprint(identity)+"/"+operation, func(t *testing.T) {
				home := t.TempDir()
				state := filepath.Join(home, ".local", "state", "sandboxed-agents")
				if err := os.MkdirAll(state, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(state, "selection.json"), []byte("invalid JSON"), 0600); err != nil {
					t.Fatal(err)
				}
				app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
					t.Fatal("workflow started under wrong identity")
					return 0, nil
				}, manager.Options{Home: home, User: func() process.Identity { return identity }})
				var stdout, stderr bytes.Buffer
				if status := app.Run(context.Background(), []string{"agents", operation, "copilot"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "UID and GID 1000") || strings.Contains(stderr.String(), "JSON") {
					t.Fatalf("status=%d stdout=%s stderr=%s", status, &stdout, &stderr)
				}
			})
		}
	}
}

func TestAgentLoginReadsEnabledStateAndLeavesSelectionUnchanged(t *testing.T) {
	home := t.TempDir()
	run := func(_ context.Context, r process.Request) (int, error) {
		if r.Name != "/usr/bin/npm" {
			t.Fatal("unexpected process", r)
		}
		writeInstalledPackage(t, home, "@github/copilot", "1.2.3")
		return 0, nil
	}
	app := manager.NewWithOptions("test", run, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	for _, enabled := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		if enabled {
			if status := app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{}); status != 0 {
				t.Fatal(status)
			}
		}
		if status := app.Run(context.Background(), []string{"agents", "check-enabled", "copilot"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 || stdout.String() != fmt.Sprintln(enabled) || stderr.Len() != 0 {
			t.Fatalf("status=%d stdout=%s stderr=%s", status, &stdout, &stderr)
		}
	}
	before, err := os.ReadFile(filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	app = manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) { return 0, nil }, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	if status := app.Run(context.Background(), []string{"agents", "login", "copilot"}, process.Streams{}); status != 0 {
		t.Fatal(status)
	}
	after, err := os.ReadFile(filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("selection changed: %s err=%v", after, err)
	}
}

func TestAgentLoginFailsForDisabledOrUnreadableSelection(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_SANDBOX", "dev")
	for _, selection := range []string{"", `null`, `[]`, `broken`} {
		t.Run(selection, func(t *testing.T) {
			home := t.TempDir()
			if selection != "" {
				state := filepath.Join(home, ".local", "state", "sandboxed-agents")
				if err := os.MkdirAll(state, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(state, "selection.json"), []byte(selection), 0600); err != nil {
					t.Fatal(err)
				}
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("failed precondition started workflow")
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "login", "copilot"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stdout=%s stderr=%s", status, &stdout, &stderr)
			}
			if selection == "" && !strings.Contains(stderr.String(), "agents enable dev copilot") {
				t.Fatal(stderr.String())
			}
		})
	}
}

func TestAgentLoginNormalizesWorkflowFailures(t *testing.T) {
	for _, failure := range []struct {
		code int
		err  error
	}{{0, nil}, {17, nil}, {125, nil}, {0, errors.New("cannot launch")}} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			home := t.TempDir()
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if r.Name == "/usr/bin/npm" {
					writeInstalledPackage(t, home, "@github/copilot", "1.2.3")
					return 0, nil
				}
				return failure.code, failure.err
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			if status := app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{}); status != 0 {
				t.Fatal(status)
			}
			want := 0
			if failure.code != 0 || failure.err != nil {
				want = 1
			}
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "login", "copilot"}, process.Streams{Stderr: &stderr}); status != want {
				t.Fatalf("status=%d want=%d stderr=%s", status, want, &stderr)
			}
		})
	}
}

func TestAgentLoginUsesTheCommandOfAnAdditionalCatalogEntry(t *testing.T) {
	catalog, err := agentcatalog.Load([]byte(`{"schema_version":1,"entries":[{"name":"fifth","delivered":true,"command":"example-cli","install":{"kind":"npm","package":"example-cli"},"login_workflows":{"example":["sign-in","--kind","example account"]},"login_message":"Sign in to your example account."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	calls := []process.Request{}
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.Name == "/usr/bin/npm" {
			writeInstalledPackage(t, home, "example-cli", "1.2.3")
			return 0, nil
		}
		calls = append(calls, r)
		return 0, nil
	}, manager.Options{Home: home, Catalog: &catalog, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	if status := app.Run(context.Background(), []string{"agents", "enable", "fifth"}, process.Streams{}); status != 0 {
		t.Fatal(status)
	}
	var stdout, stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"agents", "login", "fifth"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 {
		t.Fatalf("status=%d stderr=%s", status, &stderr)
	}
	if len(calls) != 1 || calls[0].Name != filepath.Join(home, ".local", "bin", "example-cli") || !reflect.DeepEqual(calls[0].Args, []string{"sign-in", "--kind", "example account"}) || stdout.String() != "Sign in to your example account.\n" {
		t.Fatalf("calls=%v stdout=%s", calls, &stdout)
	}
}
