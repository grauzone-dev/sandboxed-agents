package manager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAgentStatusReportsInstalledVersionAfterEnable(t *testing.T) {
	for _, test := range []struct{ agent, pkg, state string }{
		{"copilot", "@github/copilot", "unknown"},
		{"claude", "@anthropic-ai/claude-code", "signed in"},
		{"codex", "@openai/codex", "unknown"},
		{"opencode", "opencode-ai", "unknown"},
	} {
		t.Run(test.agent, func(t *testing.T) {
			home := t.TempDir()
			installs, probes := 0, 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "/usr/bin/npm" {
					installs++
					writeInstalledPackage(t, home, test.pkg, "1.2.3")
					return 0, nil
				}
				probes++
				if request.Name != filepath.Join(home, ".local", "bin", "claude") || !reflect.DeepEqual(request.Args, []string{"auth", "status"}) {
					t.Fatalf("unexpected process: %+v", request)
				}
				fmt.Fprint(request.Streams.Stdout, `{"loggedIn":true}`)
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "enable", test.agent}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 {
				t.Fatalf("enable status=%d stderr=%s", status, &stderr)
			}
			selectionPath := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
			before, err := os.ReadFile(selectionPath)
			if err != nil {
				t.Fatal(err)
			}
			writeInstalledPackage(t, home, test.pkg, "9.8.7")
			stdout.Reset()
			status := app.Run(context.Background(), []string{"agents", "status", test.agent}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			want := "Agent " + test.agent + " is enabled (version 9.8.7).\nSign-in state: " + test.state + ".\n"
			if status != 0 || stdout.String() != want || stderr.Len() != 0 || installs != 1 {
				t.Fatalf("status=%d stdout=%q stderr=%q installs=%d", status, stdout.String(), stderr.String(), installs)
			}
			if (test.agent == "claude" && probes != 1) || (test.agent != "claude" && probes != 0) {
				t.Fatalf("unexpected probes=%d", probes)
			}
			after, err := os.ReadFile(selectionPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("selection changed: before=%s after=%s err=%v", before, after, err)
			}
		})
	}
}

func TestAgentStatusInterpretsOnlyTheCatalogBooleanField(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		code   int
		err    error
		want   string
	}{
		{"signed in", `{"authenticated":true}`, 0, nil, "signed in"},
		{"output drain expired with boolean", `{"authenticated":true}`, 1, exec.ErrWaitDelay, "signed in"},
		{"output drain expired without boolean", `{"authenticated":null}`, 1, exec.ErrWaitDelay, "unknown"},
		{"not signed in", `{"authenticated":false}`, 0, nil, "not signed in"},
		{"boolean on failure exit", `{"authenticated":false}`, 1, nil, "not signed in"},
		{"boolean on nonzero signed in", `{"authenticated":true}`, 2, nil, "signed in"},
		{"missing field", `{"loggedIn":true}`, 0, nil, "unknown"},
		{"string", `{"authenticated":"true"}`, 0, nil, "unknown"},
		{"number", `{"authenticated":1}`, 0, nil, "unknown"},
		{"null field", `{"authenticated":null}`, 0, nil, "unknown"},
		{"object field", `{"authenticated":{}}`, 0, nil, "unknown"},
		{"array field", `{"authenticated":[]}`, 0, nil, "unknown"},
		{"invalid JSON", `signed in`, 0, nil, "unknown"},
		{"trailing JSON", `{"authenticated":true}{}`, 0, nil, "unknown"},
		{"null document", `null`, 0, nil, "unknown"},
		{"array document", `[true]`, 0, nil, "unknown"},
		{"no output", "", 1, nil, "unknown"},
		{"launch failure", "", 0, fmt.Errorf("cannot start probe"), "unknown"},
		{"timeout", `{"authenticated":true}`, 0, context.DeadlineExceeded, "unknown"},
		{"canceled", `{"authenticated":true}`, 0, context.Canceled, "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			entry := agentcatalog.Entry{Name: "fifth", Delivered: true, Command: "fifth-cli", Install: agentcatalog.Install{Kind: "npm", Package: "@example/fifth"}, StatusProbe: &agentcatalog.Probe{Args: []string{"auth", "status", "--json"}, BooleanField: "authenticated"}}
			data, err := json.Marshal(struct {
				SchemaVersion int                  `json:"schema_version"`
				Entries       []agentcatalog.Entry `json:"entries"`
			}{1, []agentcatalog.Entry{entry}})
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := agentcatalog.Load(data)
			if err != nil {
				t.Fatal(err)
			}
			probes := 0
			app := manager.NewWithOptions("test", func(ctx context.Context, request process.Request) (int, error) {
				if request.Name == "/usr/bin/npm" {
					writeInstalledPackage(t, home, "@example/fifth", "4.5.6")
					return 0, nil
				}
				probes++
				if request.Name != filepath.Join(home, ".local", "bin", "fifth-cli") || !reflect.DeepEqual(request.Args, []string{"auth", "status", "--json"}) || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || request.Dir != home || request.Streams.Stdin != nil || !request.CleanupGroup || !reflect.DeepEqual(request.Env, []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}) {
					t.Fatalf("unexpected probe: %+v", request)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
					t.Fatalf("unbounded probe: deadline=%v ok=%t", deadline, ok)
				}
				fmt.Fprint(request.Streams.Stdout, test.output)
				if request.Streams.Stderr != nil {
					fmt.Fprintln(request.Streams.Stderr, "private probe diagnostic")
				}
				return test.code, test.err
			}, manager.Options{Home: home, Catalog: &catalog, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			if status := app.Run(context.Background(), []string{"agents", "enable", "fifth"}, process.Streams{}); status != 0 {
				t.Fatal("enable failed")
			}
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "status", "fifth"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			want := "Agent fifth is enabled (version 4.5.6).\nSign-in state: " + test.want + ".\n"
			if status != 0 || stdout.String() != want || stderr.Len() != 0 || probes != 1 {
				t.Fatalf("status=%d stdout=%q stderr=%q probes=%d", status, stdout.String(), stderr.String(), probes)
			}
		})
	}
}

func TestAgentStatusReportsNotEnabledWithoutProcessesOrHomeWrites(t *testing.T) {
	for _, agent := range []string{"copilot", "claude", "codex", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("not-enabled status started a process")
				return 1, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "status", agent}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status != 0 || stdout.String() != "Agent "+agent+" is not enabled.\n" || stderr.Len() != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			files, err := os.ReadDir(home)
			if err != nil || len(files) != 0 {
				t.Fatalf("status wrote home: files=%v err=%v", files, err)
			}
		})
	}
}

func TestRootAgentStatusStartsOnlyTheTrustedWorker(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "sandboxed-agents")
	if err := os.MkdirAll(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "selection.json"), []byte("hostile invalid selection"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		calls++
		if request.Name != "/usr/local/bin/sandboxed-agents-manager" || !reflect.DeepEqual(request.Args, []string{"agents", "status", "claude"}) || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || request.Dir != "/" || !reflect.DeepEqual(request.Env, []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}) {
			t.Fatalf("unsafe root process: %+v", request)
		}
		fmt.Fprint(request.Streams.Stdout, "Agent claude is not enabled.\n")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 0, GID: 0} }})
	var stdout, stderr bytes.Buffer
	status := app.Run(context.Background(), []string{"agents", "status", "claude"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 0 || calls != 1 || stdout.String() != "Agent claude is not enabled.\n" || stderr.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%d", status, stdout.String(), stderr.String(), calls)
	}
}

func TestAgentStatusRefusesUnreportableSelectionOrVersion(t *testing.T) {
	for _, test := range []struct{ name, selection, pkg, message string }{
		{"invalid selection", "invalid", "", "agent selection is not a valid JSON object"},
		{"null selection", "null", "", "agent selection is not a valid JSON object"},
		{"missing package", `{"claude":{"version":"1.0.0"}}`, "", "read installed version"},
		{"invalid package", `{"claude":{"version":"1.0.0"}}`, "invalid", "invalid installed package version"},
		{"missing version", `{"claude":{"version":"1.0.0"}}`, `{}`, "invalid installed package version"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			state := filepath.Join(home, ".local", "state", "sandboxed-agents")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "selection.json"), []byte(test.selection), 0600); err != nil {
				t.Fatal(err)
			}
			if test.pkg != "" {
				writeInstalledPackage(t, home, "@anthropic-ai/claude-code", "1.0.0")
				if err := os.WriteFile(filepath.Join(home, ".local", "lib", "node_modules", "@anthropic-ai", "claude-code", "package.json"), []byte(test.pkg), 0600); err != nil {
					t.Fatal(err)
				}
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("unreportable status started probe")
				return 1, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "status", "claude"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.message) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}
