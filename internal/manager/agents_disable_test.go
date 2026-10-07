package manager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestDisableRemovesOnlyManagedCommandAndWholeSelectionEntry(t *testing.T) {
	for _, test := range []struct{ name, pkg, command string }{
		{"copilot", "@github/copilot", "copilot"},
		{"claude", "@anthropic-ai/claude-code", "claude"},
		{"codex", "@openai/codex", "codex"},
		{"opencode", "opencode-ai", "opencode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			options := agentOptions(home)
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != test.pkg+"@latest" {
					t.Fatalf("installation=%+v", r)
				}
				writeInstalledPackage(t, home, test.pkg, "1.2.3")
				writeHomeFile(t, home, ".local/bin/"+test.command, "managed command", 0700)
				return 0, nil
			}, options)
			if status, _, diagnostic := runAgentCommand(app, "enable", test.name); status != 0 {
				t.Fatal(diagnostic)
			}
			selectionPath := filepath.Join(home, ".local/state/sandboxed-agents/selection.json")
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"`+test.name+`":{"version":"1.2.3","future":{"setting":true}},"another":{"version":"9.8.7","future":{"keep":true}}}`, 0600)
			for _, path := range []string{".config/credentials", ".cache/agent/cache", ".local/cache/sandboxed-agents/npm/cache", ".local/bin/unmanaged"} {
				writeHomeFile(t, home, path, "preserved "+path, 0600)
			}
			commandPath := filepath.Join(home, ".local/bin", test.command)
			before := homeFiles(t, home, commandPath, selectionPath)
			app = manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				t.Error("disable started a package command")
				return 1, nil
			}, options)
			if status, out, diagnostic := runAgentCommand(app, "disable", test.name); status != 0 || out != "Agent "+test.name+" is disabled.\n" {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if _, err := os.Lstat(commandPath); !os.IsNotExist(err) {
				t.Fatalf("managed command remains: %v", err)
			}
			data, err := os.ReadFile(selectionPath)
			if err != nil {
				t.Fatal(err)
			}
			var selection map[string]json.RawMessage
			if err := json.Unmarshal(data, &selection); err != nil || len(selection) != 1 || string(selection["another"]) != `{"version":"9.8.7","future":{"keep":true}}` {
				t.Fatalf("selection=%s error=%v", data, err)
			}
			if after := homeFiles(t, home, commandPath, selectionPath); !reflect.DeepEqual(before, after) {
				t.Fatalf("other home files changed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestDisableNotEnabledLeavesHomeUnchanged(t *testing.T) {
	for _, setup := range []string{"pristine", "command without selection", "selection without lock", "existing lock"} {
		t.Run(setup, func(t *testing.T) {
			home := t.TempDir()
			if setup != "pristine" {
				writeHomeFile(t, home, ".local/bin/codex", "unmanaged command", 0700)
			}
			if setup == "selection without lock" || setup == "existing lock" {
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"claude":{"version":"1.2.3"}}`, 0600)
			}
			if setup == "existing lock" {
				writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
			}
			before := homeFiles(t, home)
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Error("no-op started a process")
				return 1, nil
			}, agentOptions(home))
			if status, out, diagnostic := runAgentCommand(app, "disable", "codex"); status != 0 || !strings.Contains(out, "nothing to do") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if after := homeFiles(t, home); !reflect.DeepEqual(before, after) {
				t.Fatalf("no-op changed home: before=%v after=%v", before, after)
			}
			if setup == "pristine" {
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 0 {
					t.Fatalf("no-op created home entries=%v error=%v", entries, err)
				}
			}
		})
	}
}

func TestEnableAfterDisableInstallsThenCurrentVersion(t *testing.T) {
	for _, test := range []struct{ name, pkg string }{
		{"copilot", "@github/copilot"}, {"claude", "@anthropic-ai/claude-code"},
		{"codex", "@openai/codex"}, {"opencode", "opencode-ai"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			version := "1.2.3"
			installs := 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != test.pkg+"@latest" {
					t.Fatalf("installation=%+v", r)
				}
				installs++
				writeInstalledPackage(t, home, test.pkg, version)
				writeHomeFile(t, home, ".local/bin/"+test.name, version, 0700)
				return 0, nil
			}, agentOptions(home))
			for _, action := range []string{"enable", "disable", "enable"} {
				if action == "disable" {
					version = "4.5.6"
				}
				if status, out, diagnostic := runAgentCommand(app, action, test.name); status != 0 || (action == "enable" && !strings.Contains(out, version)) {
					t.Fatalf("action=%s status=%d out=%q diagnostic=%q", action, status, out, diagnostic)
				}
			}
			if installs != 2 {
				t.Fatalf("installations=%d", installs)
			}
			data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
			var selection map[string]map[string]any
			if err != nil || json.Unmarshal(data, &selection) != nil || len(selection[test.name]) != 1 || selection[test.name]["version"] != "4.5.6" {
				t.Fatalf("selection=%s error=%v", data, err)
			}
		})
	}
}

func TestRootDisableLaunchesTrustedWorkerBeforeHomeAccess(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","command":"/home/agent/evil","args":["from-workspace"]}}`, 0600)
	writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
	writeHomeFile(t, home, ".local/bin/codex", "managed", 0700)
	writeHomeFile(t, home, "evil", "preserved", 0700)
	before := homeFiles(t, home)
	worker := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if isSessionListRequest(r) {
			return 0, nil
		}
		t.Error("disable worker started a process")
		return 1, nil
	}, agentOptions(home))
	rootCalls := 0
	root := manager.NewWithOptions("test", func(ctx context.Context, r process.Request) (int, error) {
		rootCalls++
		if r.Name != "/usr/local/bin/sandboxed-agents-manager" || !reflect.DeepEqual(r.Args, []string{"agents", "disable", "codex"}) || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) || r.Dir != "/" {
			t.Fatalf("unsafe administrative process=%+v", r)
		}
		wantEnv := []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local/bin") + ":/usr/local/bin:/usr/bin:/bin"}
		if !reflect.DeepEqual(r.Env, wantEnv) || !reflect.DeepEqual(before, homeFiles(t, home)) {
			t.Fatalf("root home work or unsafe environment=%v", r.Env)
		}
		return worker.Run(ctx, r.Args, r.Streams), nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{} }})
	if status, _, diagnostic := runAgentCommand(root, "disable", "codex"); status != 0 || rootCalls != 1 {
		t.Fatalf("status=%d calls=%d diagnostic=%q", status, rootCalls, diagnostic)
	}
	if data, err := os.ReadFile(filepath.Join(home, "evil")); err != nil || string(data) != "preserved" {
		t.Fatalf("selection supplied command changed: content=%q error=%v", data, err)
	}
	root = manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) { return 0, nil }, manager.Options{Home: filepath.Join(home, "evil"), User: func() process.Identity { return process.Identity{} }})
	if status, _, diagnostic := runAgentCommand(root, "disable", "codex"); status != 0 {
		t.Fatalf("root inspected invalid home before worker: %s", diagnostic)
	}
}

func TestDisableWaitsForConcurrentEnableBeforeReadingSelection(t *testing.T) {
	home := t.TempDir()
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if isSessionListRequest(r) {
			return 0, nil
		}
		if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != "@openai/codex@latest" {
			t.Errorf("unexpected installation: %+v", r)
			return 1, nil
		}
		close(started)
		<-release
		writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
		writeHomeFile(t, home, ".local/bin/codex", "managed", 0700)
		return 0, nil
	}, agentOptions(home))
	enabled := make(chan string, 1)
	go func() {
		status, _, diagnostic := runAgentCommand(app, "enable", "codex")
		if status == 0 {
			diagnostic = ""
		}
		enabled <- diagnostic
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("enable did not start")
	}
	disabled := make(chan string, 1)
	go func() {
		status, out, diagnostic := runAgentCommand(app, "disable", "codex")
		if status == 0 && strings.Contains(out, "is disabled") {
			diagnostic = ""
		} else {
			diagnostic = out + diagnostic
		}
		disabled <- diagnostic
	}()
	select {
	case result := <-disabled:
		t.Fatalf("disable completed while enable held lock: %q", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	for _, done := range []chan string{enabled, disabled} {
		select {
		case diagnostic := <-done:
			if diagnostic != "" {
				t.Fatal(diagnostic)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("installation change did not finish")
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".local/bin/codex")); !os.IsNotExist(err) {
		t.Fatalf("command survives serialized disable: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
	if err != nil || strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("selection=%s error=%v", data, err)
	}
}

func TestDisableFailuresKeepHomeDataAndRejectUnexpectedIdentity(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity process.Identity
		code     int
		err      error
	}{
		{"root worker exit", process.Identity{}, 17, nil},
		{"root worker launch", process.Identity{}, 0, errors.New("launch failed")},
		{"wrong user", process.Identity{UID: 1001, GID: 1000}, 0, nil},
		{"wrong group", process.Identity{UID: 1000, GID: 1001}, 0, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeHomeFile(t, home, ".local/bin/codex", "preserved", 0700)
			before := homeFiles(t, home)
			calls := 0
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				calls++
				return test.code, test.err
			}, manager.Options{Home: home, User: func() process.Identity { return test.identity }})
			if status, _, diagnostic := runAgentCommand(app, "disable", "codex"); status == 0 || diagnostic == "" {
				t.Fatalf("failure succeeded: status=%d diagnostic=%q", status, diagnostic)
			}
			if test.identity.UID != 0 && calls != 0 {
				t.Fatal("unexpected identity started a process")
			}
			if !reflect.DeepEqual(before, homeFiles(t, home)) {
				t.Fatal("failed disable changed home")
			}
		})
	}
}

func TestDisableInvalidSelectionAndFilesystemErrorsPreserveHomeData(t *testing.T) {
	for _, setup := range []string{"invalid JSON", "null selection", "array selection", "invalid entry", "selection directory", "lock directory", "command directory"} {
		t.Run(setup, func(t *testing.T) {
			home := t.TempDir()
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"}}`, 0600)
			writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
			writeHomeFile(t, home, ".local/bin/codex", "managed", 0700)
			selectionPath := filepath.Join(home, ".local/state/sandboxed-agents/selection.json")
			invalid := map[string]string{"invalid JSON": "{", "null selection": "null", "array selection": "[]", "invalid entry": `{"codex":{"version":false}}`}
			if content, ok := invalid[setup]; ok {
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", content, 0600)
			}
			var directoryPath string
			switch setup {
			case "selection directory":
				directoryPath = selectionPath
			case "lock directory":
				directoryPath = filepath.Join(home, ".local/state/sandboxed-agents/manager.lock")
			case "command directory":
				directoryPath = filepath.Join(home, ".local/bin/codex")
			}
			if directoryPath != "" {
				if err := os.Remove(directoryPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directoryPath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directoryPath, "preserved"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := homeFiles(t, home)
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if setup == "command directory" && isSessionListRequest(r) {
					return 0, nil
				}
				t.Error("disable started a process")
				return 1, nil
			}, agentOptions(home))
			if status, _, diagnostic := runAgentCommand(app, "disable", "codex"); status == 0 || diagnostic == "" {
				t.Fatalf("filesystem error succeeded: status=%d diagnostic=%q", status, diagnostic)
			}
			if !reflect.DeepEqual(before, homeFiles(t, home)) {
				t.Fatal("failed disable changed home data")
			}
		})
	}
}

func TestDisableUnlinksManagedSymlinkAndPreservesItsTarget(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"}}`, 0600)
	writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
	writeHomeFile(t, home, ".local/lib/node_modules/@openai/codex/command", "package contents", 0700)
	commandPath := filepath.Join(home, ".local/bin/codex")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/node_modules/@openai/codex/command", commandPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	before := homeFiles(t, home, commandPath, filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
	app := manager.NewWithOptions("test", emptyAgentSessions(t), agentOptions(home))
	if status, _, diagnostic := runAgentCommand(app, "disable", "codex"); status != 0 {
		t.Fatal(diagnostic)
	}
	if _, err := os.Lstat(commandPath); !os.IsNotExist(err) {
		t.Fatalf("command symlink remains: %v", err)
	}
	if after := homeFiles(t, home, commandPath, filepath.Join(home, ".local/state/sandboxed-agents/selection.json")); !reflect.DeepEqual(before, after) {
		t.Fatal("symlink target or other home files changed")
	}
}

func TestDisableMissingManagedCommandStillRemovesSelectionEntry(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"}}`, 0600)
	app := manager.NewWithOptions("test", emptyAgentSessions(t), agentOptions(home))
	if status, _, diagnostic := runAgentCommand(app, "disable", "codex"); status != 0 {
		t.Fatal(diagnostic)
	}
	data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
	if err != nil || strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("selection=%s error=%v", data, err)
	}
}

func TestDisableCanceledWhileWaitingForManagerLockLeavesHomeUnchanged(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"}}`, 0600)
	writeHomeFile(t, home, ".local/bin/codex", "managed", 0700)
	writeHomeFile(t, home, ".config/credentials", "preserved", 0600)
	before := homeFiles(t, home)
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		close(started)
		<-release
		return 1, nil
	}, agentOptions(home))
	done := make(chan struct{})
	go func() {
		runAgentCommand(app, "enable", "claude")
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("enable did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var diagnostic bytes.Buffer
	status := app.Run(ctx, []string{"agents", "disable", "codex"}, process.Streams{Stderr: &diagnostic})
	if status == 0 || !strings.Contains(diagnostic.String(), "context canceled") {
		t.Fatalf("status=%d diagnostic=%q", status, diagnostic.String())
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("enable did not finish")
	}
	if !reflect.DeepEqual(before, homeFiles(t, home)) {
		t.Fatal("canceled disable changed home")
	}
}

func isSessionListRequest(r process.Request) bool {
	return r.Name == "/usr/bin/tmux" && reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "list-sessions", "-F", "#{session_name}"})
}

func emptyAgentSessions(t *testing.T) process.Runner {
	t.Helper()
	return func(_ context.Context, r process.Request) (int, error) {
		if !isSessionListRequest(r) {
			t.Errorf("unexpected process: %+v", r)
			return 1, nil
		}
		return 0, nil
	}
}

func agentOptions(home string) manager.Options {
	return manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }}
}

func runAgentCommand(app *manager.Manager, action, name string, options ...string) (int, string, string) {
	var out, diagnostic bytes.Buffer
	status := app.Run(context.Background(), append([]string{"agents", action, name}, options...), process.Streams{Stdout: &out, Stderr: &diagnostic})
	return status, out.String(), diagnostic.String()
}

func writeHomeFile(t *testing.T, home, path, content string, mode os.FileMode) {
	t.Helper()
	fullPath := filepath.Join(home, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

type homeFile struct {
	Content  string
	Mode     os.FileMode
	Modified time.Time
	Target   string
}

func homeFiles(t *testing.T, home string, excluded ...string) map[string]homeFile {
	t.Helper()
	files := map[string]homeFile{}
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		for _, ignore := range excluded {
			if path == ignore {
				return nil
			}
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file := homeFile{Mode: info.Mode(), Modified: info.ModTime()}
		if info.Mode()&os.ModeSymlink != 0 {
			file.Target, err = os.Readlink(path)
		} else {
			var data []byte
			data, err = os.ReadFile(path)
			file.Content = string(data)
		}
		if err != nil {
			return err
		}
		files[path] = file
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
