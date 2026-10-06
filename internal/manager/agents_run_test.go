package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestRunAgentPreservesArgumentsStreamsAndExitStatusAsAgent(t *testing.T) {
	for _, code := range []int{0, 7, 125, 127} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			home := t.TempDir()
			options := manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }}
			enable := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
				return 0, nil
			}, options)
			if status := enable.Run(context.Background(), []string{"agents", "enable", "codex"}, process.Streams{}); status != 0 {
				t.Fatalf("enable status=%d", status)
			}
			calls := 0
			app := manager.NewWithOptions("test", func(ctx context.Context, request process.Request) (int, error) {
				calls++
				if request.Name != filepath.Join(home, ".local", "bin", "codex") || request.Dir != "/workspace" || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("agent request=%+v", request)
				}
				want := []string{"--help", "-x", "value", "--", "--y", "", "two words", "$(touch /tmp/evil)"}
				if !reflect.DeepEqual(request.Args, want) {
					t.Fatalf("args=%q want=%q", request.Args, want)
				}
				wantEnv := []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}
				if !reflect.DeepEqual(request.Env, wantEnv) {
					t.Fatalf("environment=%q want=%q", request.Env, wantEnv)
				}
				if _, deadline := ctx.Deadline(); deadline {
					t.Fatal("agent run has a time limit")
				}
				input, err := io.ReadAll(request.Streams.Stdin)
				if err != nil || string(input) != "input\x00\r\n" {
					t.Fatalf("input=%q err=%v", input, err)
				}
				io.WriteString(request.Streams.Stdout, "output\x00\r\n")
				io.WriteString(request.Streams.Stderr, "agent diagnostic\n")
				return code, nil
			}, options)
			var stdout, stderr bytes.Buffer
			args := []string{"agents", "run", "agent01", "codex", "--help", "-x", "value", "--", "--y", "", "two words", "$(touch /tmp/evil)"}
			status := app.Run(context.Background(), args, process.Streams{Stdin: strings.NewReader("input\x00\r\n"), Stdout: &stdout, Stderr: &stderr})
			if status != code || stdout.String() != "output\x00\r\n" || stderr.String() != "agent diagnostic\n" || calls != 1 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%d", status, stdout.String(), stderr.String(), calls)
			}
		})
	}
}

func TestAgentsUseTheImagePlaywrightBrowsersInRunsAndSessions(t *testing.T) {
	for _, operation := range []string{"run", "session-worker"} {
		for _, browserPath := range []string{"", "/opt/playwright-browsers", "/home/agent/other-browsers"} {
			t.Run(operation+"/"+browserPath, func(t *testing.T) {
				t.Setenv("PLAYWRIGHT_BROWSERS_PATH", browserPath)
				t.Setenv("TERM", "")
				home := t.TempDir()
				writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
				wantEnv := []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}
				if browserPath == "/opt/playwright-browsers" {
					wantEnv = append(wantEnv, "PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers")
				}
				calls := 0
				app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
					if request.Name == "/usr/bin/tmux" {
						return 0, nil
					}
					calls++
					if request.Name != filepath.Join(home, ".local", "bin", "codex") || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || !reflect.DeepEqual(request.Env, wantEnv) {
						t.Fatalf("agent request=%+v want environment=%q", request, wantEnv)
					}
					return 0, nil
				}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
				var stderr bytes.Buffer
				status := app.Run(context.Background(), []string{"agents", operation, "agent01", "codex"}, process.Streams{Stderr: &stderr})
				if status != 0 || stderr.Len() != 0 || calls != 1 {
					t.Fatalf("status=%d stderr=%q calls=%d", status, stderr.String(), calls)
				}
			})
		}
	}
}

func TestRunAgentRefusesRootAndOtherIdentitiesBeforeHomeWork(t *testing.T) {
	for _, identity := range []process.Identity{{UID: 0, GID: 0}, {UID: 0, GID: 1000}, {UID: 1000, GID: 0}, {UID: 1001, GID: 1000}} {
		t.Run(fmt.Sprint(identity), func(t *testing.T) {
			home := t.TempDir()
			writeRunSelection(t, home, "invalid JSON")
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("refused identity started a process")
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return identity }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "run", "agent01", "codex"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "must run as UID and GID 1000") || strings.Contains(stderr.String(), "JSON") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunAgentRefusesBeforeStartingWhenSelectionIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name, selection, message string
	}{
		{"missing", "", "agents enable agent01 codex"},
		{"not enabled", `{"claude":{"version":"1.0.0"}}`, "agents enable agent01 codex"},
		{"invalid JSON", "broken", "not a valid JSON object"},
		{"null", "null", "not a valid JSON object"},
		{"array", "[]", "not a valid JSON object"},
		{"directory", "directory", "read agent selection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.selection != "" {
				writeRunSelection(t, home, test.selection)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("unavailable selection started a process")
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "run", "agent01", "codex"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.message) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}

func writeRunSelection(t *testing.T, home, content string) {
	t.Helper()
	path := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if content == "directory" {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunAgentStartsTheCatalogCommandWithNoArguments(t *testing.T) {
	for _, test := range []struct{ name, command string }{
		{"claude", "claude"}, {"codex", "codex"}, {"copilot", "copilot"}, {"opencode", "opencode"}, {"fifth", "other-command"},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := agentcatalog.Embedded()
			if test.name == "fifth" {
				var err error
				catalog, err = agentcatalog.Load([]byte(`{"schema_version":1,"entries":[{"name":"fifth","delivered":true,"command":"other-command","install":{"kind":"npm","package":"@example/fifth"}}]}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			home := t.TempDir()
			writeRunSelection(t, home, fmt.Sprintf(`{%q:{"version":"1.2.3"}}`, test.name))
			calls := 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				calls++
				if request.Name != filepath.Join(home, ".local", "bin", test.command) || len(request.Args) != 0 || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("request=%+v", request)
				}
				return 0, nil
			}, manager.Options{Home: home, Catalog: &catalog, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "run", "agent01", test.name}, process.Streams{Stderr: &stderr}); status != 0 || stderr.Len() != 0 || calls != 1 {
				t.Fatalf("status=%d stderr=%q calls=%d", status, stderr.String(), calls)
			}
		})
	}
}

func TestRunAgentReportsAStartFailure(t *testing.T) {
	home := t.TempDir()
	writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		return 0, errors.New("command is missing")
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	var stdout, stderr bytes.Buffer
	status := app.Run(context.Background(), []string{"agents", "run", "agent01", "codex"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 1 || stdout.Len() != 0 || stderr.String() != "start agent codex: command is missing\n" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestRunAgentRejectsInvalidUsageAndUnknownAgents(t *testing.T) {
	for _, args := range [][]string{{"agents", "run"}, {"agents", "run", "agent01"}, {"agents", "run", "agent01", "unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid request started a process")
				return 0, nil
			}, manager.Options{Home: t.TempDir()})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), args, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}
