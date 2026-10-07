package manager_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestEnableExactVersionPinsAndReportsTheInstalledAgent(t *testing.T) {
	home := t.TempDir()
	installs := 0
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if isSessionListRequest(r) {
			return 0, nil
		}
		installs++
		if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != "@openai/codex@1.2.3" || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
			t.Fatalf("install=%+v", r)
		}
		writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
		return 0, nil
	}, agentOptions(home))
	var out, diagnostic string
	var status int
	status, out, diagnostic = runAgentCommand(app, "enable", "codex", "--version", "1.2.3")
	if status != 0 || diagnostic != "" || !strings.Contains(out, "version 1.2.3") || !strings.Contains(out, "Pin: 1.2.3.") {
		t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
	}
	app = manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if !isSessionListRequest(r) {
			t.Errorf("status started unexpected process: %+v", r)
		}
		return 0, nil
	}, agentOptions(home))
	status, out, diagnostic = runAgentCommand(app, "status", "codex")
	if status != 0 || diagnostic != "" || !strings.Contains(out, "version 1.2.3") || !strings.Contains(out, "Pin: 1.2.3.") || installs != 1 {
		t.Fatalf("status=%d out=%q diagnostic=%q installs=%d", status, out, diagnostic, installs)
	}
}

func TestEnableVersionReplacesInstallationsAndSetsPinsWithoutReinstallingMatches(t *testing.T) {
	for _, test := range []struct{ name, installed, pin, wantTarget string }{
		{"unselected", "", "", "@openai/codex@2.3.4"},
		{"unpinned different version", "1.2.3", "", "@openai/codex@2.3.4"},
		{"pinned different version", "1.2.3", "1.2.3", "@openai/codex@2.3.4"},
		{"same version without pin", "2.3.4", "", ""},
		{"same version different pin", "2.3.4", "1.2.3", ""},
		{"same version same pin", "2.3.4", "2.3.4", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.installed != "" {
				writeInstalledPackage(t, home, "@openai/codex", test.installed)
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", fmt.Sprintf(`{"codex":{"version":%q,"pin":%q,"future":{"keep":true}},"copilot":{"version":"9.8.7","pin":"9.8.7","future":42}}`, test.installed, test.pin), 0600)
			}
			installs := 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				installs++
				if r.Args[len(r.Args)-1] != test.wantTarget || test.wantTarget == "" {
					t.Fatalf("unexpected install: %+v", r)
				}
				writeInstalledPackage(t, home, "@openai/codex", "2.3.4")
				return 0, nil
			}, agentOptions(home))
			status, out, diagnostic := runAgentCommand(app, "enable", "codex", "--version=2.3.4")
			if status != 0 || diagnostic != "" || !strings.Contains(out, "version 2.3.4") || !strings.Contains(out, "Pin: 2.3.4.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if (test.wantTarget == "" && installs != 0) || (test.wantTarget != "" && installs != 1) {
				t.Fatalf("installs=%d", installs)
			}
			status, out, diagnostic = runAgentCommand(app, "enable", "codex")
			if status != 0 || diagnostic != "" || !strings.Contains(out, "Pin: 2.3.4.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			status, out, diagnostic = runAgentCommand(app, "status", "codex")
			if status != 0 || diagnostic != "" || !strings.Contains(out, "version 2.3.4") || !strings.Contains(out, "Pin: 2.3.4.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if test.installed != "" {
				data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
				if err != nil {
					t.Fatal(err)
				}
				var entries map[string]json.RawMessage
				if err := json.Unmarshal(data, &entries); err != nil {
					t.Fatal(err)
				}
				var fields map[string]any
				if err := json.Unmarshal(entries["codex"], &fields); err != nil {
					t.Fatal(err)
				}
				if fields["future"] == nil || string(entries["copilot"]) != `{"version":"9.8.7","pin":"9.8.7","future":42}` {
					t.Fatalf("other fields changed: %s", data)
				}
			}
		})
	}
}

func TestUpdateReinstallsPinsAndUnpinInstallsTheNewestVersion(t *testing.T) {
	for _, test := range []struct {
		name, pin, target, wantVersion, wantPin string
		unpin                                   bool
	}{
		{"pinned", "1.2.3", "@openai/codex@1.2.3", "1.2.3", "1.2.3", false},
		{"unpinned", "", "@openai/codex@latest", "2.3.4", "none", false},
		{"remove pin", "1.2.3", "@openai/codex@latest", "2.3.4", "none", true},
		{"unpin without pin", "", "@openai/codex@latest", "2.3.4", "none", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", fmt.Sprintf(`{"codex":{"version":"1.2.3","pin":%q,"future":true},"copilot":{"version":"7.8.9","pin":"7.8.9"}}`, test.pin), 0600)
			installs := 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				installs++
				if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != test.target || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("install=%+v", r)
				}
				writeInstalledPackage(t, home, "@openai/codex", test.wantVersion)
				return 0, nil
			}, agentOptions(home))
			options := []string{}
			if test.unpin {
				options = append(options, "--unpin")
			}
			status, out, diagnostic := runAgentCommand(app, "update", "agent01", append([]string{"codex"}, options...)...)
			if status != 0 || diagnostic != "" || installs != 1 || !strings.Contains(out, "version "+test.wantVersion) || !strings.Contains(out, "Pin: "+test.wantPin+".") {
				t.Fatalf("status=%d out=%q diagnostic=%q installs=%d", status, out, diagnostic, installs)
			}
			app = manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if !isSessionListRequest(r) {
					t.Errorf("unexpected process: %+v", r)
				}
				return 0, nil
			}, agentOptions(home))
			status, out, diagnostic = runAgentCommand(app, "status", "codex")
			if status != 0 || diagnostic != "" || !strings.Contains(out, "version "+test.wantVersion) || !strings.Contains(out, "Pin: "+test.wantPin+".") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"future":true`) || !strings.Contains(string(data), `"copilot":{"version":"7.8.9","pin":"7.8.9"}`) {
				t.Fatalf("other fields changed: %s", data)
			}
			var selection map[string]map[string]json.RawMessage
			if err := json.Unmarshal(data, &selection); err != nil {
				t.Fatal(err)
			}
			if _, pinned := selection["codex"]["pin"]; test.wantPin == "none" && pinned {
				t.Fatalf("pin remains: %s", data)
			}
		})
	}
}

func TestDisableRemovesThePinAndReenableGetsTheCurrentVersion(t *testing.T) {
	for _, entry := range []struct{ name, pkg string }{
		{"codex", "@openai/codex"}, {"copilot", "@github/copilot"}, {"claude", "@anthropic-ai/claude-code"}, {"opencode", "opencode-ai"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			home := t.TempDir()
			targets := []string{}
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				if r.Name == filepath.Join(home, ".local", "bin", "claude") {
					return 0, nil
				}
				if r.Name != "/usr/bin/npm" {
					t.Fatalf("unexpected process: %+v", r)
				}
				target := r.Args[len(r.Args)-1]
				targets = append(targets, target)
				version := "1.2.3"
				if target == entry.pkg+"@latest" {
					version = "4.5.6"
				}
				writeInstalledPackage(t, home, entry.pkg, version)
				writeHomeFile(t, home, ".local/bin/"+entry.name, "managed command", 0700)
				return 0, nil
			}, agentOptions(home))
			if status, _, diagnostic := runAgentCommand(app, "enable", entry.name, "--version", "1.2.3"); status != 0 {
				t.Fatal(diagnostic)
			}
			writeHomeFile(t, home, ".config/credentials", "keep credentials", 0600)
			writeHomeFile(t, home, ".cache/agent/data", "keep cache", 0600)
			status, out, diagnostic := runAgentCommand(app, "disable", entry.name)
			if status != 0 || diagnostic != "" || out != "Agent "+entry.name+" is disabled (removed pin 1.2.3).\n" {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if status, out, diagnostic = runAgentCommand(app, "status", entry.name); status != 0 || !strings.Contains(out, "is not enabled") || strings.Contains(out, "Pin:") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if status, out, diagnostic = runAgentCommand(app, "enable", entry.name); status != 0 || !strings.Contains(out, "version 4.5.6") || !strings.Contains(out, "Pin: none.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			status, out, diagnostic = runAgentCommand(app, "status", entry.name)
			if status != 0 || !strings.Contains(out, "version 4.5.6") || !strings.Contains(out, "Pin: none.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			if !reflect.DeepEqual(targets, []string{entry.pkg + "@1.2.3", entry.pkg + "@latest"}) {
				t.Fatal(targets)
			}
			for path, want := range map[string]string{".config/credentials": "keep credentials", ".cache/agent/data": "keep cache"} {
				data, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(path)))
				if err != nil || string(data) != want {
					t.Fatalf("path=%s content=%q err=%v", path, data, err)
				}
			}
		})
	}
}

func TestUpdateOfAnAgentNotEnabledNamesTheEnableCommandAndInstallsNothing(t *testing.T) {
	for _, setup := range []string{"pristine", "other selected agent"} {
		t.Run(setup, func(t *testing.T) {
			home := t.TempDir()
			if setup != "pristine" {
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"copilot":{"version":"1.2.3"}}`, 0600)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Error("disabled update ran a process")
				return 1, nil
			}, agentOptions(home))
			status, out, diagnostic := runAgentCommand(app, "update", "agent01", "codex", "--unpin")
			if status == 0 || out != "" || !strings.Contains(diagnostic, "agents enable agent01 codex") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
		})
	}
}

func TestFailedAgentInstallationsPreserveTheSelectionAndPin(t *testing.T) {
	for _, operation := range []string{"enable", "update", "unpin"} {
		for _, failure := range []string{"exit status", "start", "missing metadata", "invalid metadata", "wrong version"} {
			if operation == "unpin" && failure == "wrong version" {
				continue
			}
			t.Run(operation+"/"+failure, func(t *testing.T) {
				home := t.TempDir()
				writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
				original := `{"codex":{"version":"1.2.3","pin":"1.2.3","future":42},"copilot":{"version":"4.5.6"}}` + "\n"
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", original, 0600)
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					if isSessionListRequest(r) {
						return 0, nil
					}
					if r.Name != "/usr/bin/npm" {
						t.Fatalf("unexpected installation: %+v", r)
					}
					switch failure {
					case "exit status":
						return 17, nil
					case "start":
						return 0, fmt.Errorf("cannot start npm")
					case "missing metadata":
						return 0, os.Remove(filepath.Join(home, ".local/lib/node_modules/@openai/codex/package.json"))
					case "invalid metadata":
						writeHomeFile(t, home, ".local/lib/node_modules/@openai/codex/package.json", `{}`, 0600)
					case "wrong version":
						writeInstalledPackage(t, home, "@openai/codex", "9.8.7")
					}
					return 0, nil
				}, agentOptions(home))
				action, args := "enable", []string{"codex", "--version", "2.3.4"}
				if operation != "enable" {
					action, args = "update", []string{"agent01", "codex"}
				}
				if operation == "unpin" {
					args = append(args, "--unpin")
				}
				status, out, diagnostic := runAgentCommand(app, action, args[0], args[1:]...)
				if status == 0 || strings.Contains(out, "Pin:") || diagnostic == "" {
					t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
				}
				data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
				if err != nil || string(data) != original {
					t.Fatalf("selection changed: %s error=%v", data, err)
				}
			})
		}
	}
}

func TestPinChangesAndAgentUpdatesWaitForTheManagerLock(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"pin-only enable", []string{"agents", "enable", "codex", "--version", "1.2.3"}},
		{"replace and pin", []string{"agents", "enable", "codex", "--version", "2.3.4"}},
		{"pinned update", []string{"agents", "update", "agent01", "codex"}},
		{"unpin update", []string{"agents", "update", "agent01", "codex", "--unpin"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"1.2.3"}}`, 0600)
			held, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if isSessionListRequest(r) {
					return 0, nil
				}
				if r.Name != "/usr/bin/npm" {
					t.Errorf("unexpected installation: %+v", r)
					return 1, nil
				}
				target := r.Args[len(r.Args)-1]
				if target == "@github/copilot@latest" {
					close(held)
					<-release
					writeInstalledPackage(t, home, "@github/copilot", "4.5.6")
				} else {
					version := "1.2.3"
					if target == "@openai/codex@latest" || target == "@openai/codex@2.3.4" {
						version = "2.3.4"
					}
					writeInstalledPackage(t, home, "@openai/codex", version)
				}
				return 0, nil
			}, agentOptions(home))
			firstDone := make(chan int, 1)
			go func() {
				firstDone <- app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{})
			}()
			select {
			case <-held:
			case <-time.After(5 * time.Second):
				t.Fatal("installation never acquired lock")
			}
			done := make(chan int, 1)
			go func() { done <- app.Run(context.Background(), test.args, process.Streams{}) }()
			select {
			case status := <-done:
				t.Fatalf("mutation completed while another installation held the lock: %d", status)
			case <-time.After(100 * time.Millisecond):
			}
			unblock()
			for _, ch := range []chan int{firstDone, done} {
				select {
				case status := <-ch:
					if status != 0 {
						t.Fatalf("mutation failed: %d", status)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("mutation did not complete")
				}
			}
			data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
			if err != nil || !strings.Contains(string(data), `"copilot"`) || !strings.Contains(string(data), `"codex"`) {
				t.Fatalf("selection lost an agent: %s error=%v", data, err)
			}
		})
	}
}

func TestAgentVersionUsageErrorsStartNoProcessAndChangeNoHomeData(t *testing.T) {
	for _, args := range [][]string{
		{"enable", "codex", "--version"},
		{"enable", "codex", "--version", ""},
		{"enable", "codex", "--version", "latest"},
		{"enable", "codex", "--version", "^1.2.3"},
		{"enable", "codex", "--version", "1.2.3", "--version", "2.3.4"},
		{"enable", "codex", "--version", "1.2.3", "--unpin"},
		{"update", "agent01", "codex", "--unpin", "--unpin"},
		{"update", "agent01", "codex", "--unpin=true"},
		{"update", "agent01", "codex", "--version", "1.2.3"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			for _, identity := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 1000}} {
				app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
					t.Error("invalid usage started a process")
					return 1, nil
				}, manager.Options{Home: home, User: func() process.Identity { return identity }})
				status, _, diagnostic := runAgentCommand(app, args[0], args[1], args[2:]...)
				if status == 0 || diagnostic == "" {
					t.Fatalf("status=%d diagnostic=%q", status, diagnostic)
				}
				files, err := os.ReadDir(home)
				if err != nil || len(files) != 0 {
					t.Fatalf("usage wrote home: %v error=%v", files, err)
				}
			}
		})
	}
}

func TestVersionedAgentMutationsRunOnlyInTheTrustedAgentWorker(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "enable", "codex", "--version", "1.2.3"},
		{"agents", "update", "agent01", "codex", "--unpin"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			calls := 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				calls++
				if r.Name != manager.ExecutablePath || !reflect.DeepEqual(r.Args, args) || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) || r.Dir != "/" {
					t.Fatalf("unsafe root process: %+v", r)
				}
				return 0, nil
			}, manager.Options{Home: filepath.Join(t.TempDir(), "missing-home"), User: func() process.Identity { return process.Identity{UID: 0, GID: 0} }})
			if status := app.Run(context.Background(), args, process.Streams{}); status != 0 || calls != 1 {
				t.Fatalf("status=%d calls=%d", status, calls)
			}
			calls = 0
			app = manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) { calls++; return 0, nil }, manager.Options{Home: t.TempDir(), User: func() process.Identity { return process.Identity{UID: 1000, GID: 0} }})
			if status := app.Run(context.Background(), args, process.Streams{}); status == 0 || calls != 0 {
				t.Fatalf("unexpected identity accepted: status=%d calls=%d", status, calls)
			}
		})
	}
}

func TestCanceledVersionChangesLeaveTheSelectionAndPinUnchanged(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "enable", "codex", "--version", "1.2.3"},
		{"agents", "enable", "codex", "--version", "2.3.4"},
		{"agents", "update", "agent01", "codex", "--unpin"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			original := `{"codex":{"version":"1.2.3","pin":"1.2.3"}}` + "\n"
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", original, 0600)
			writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
			before := homeFiles(t, home)
			held, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != "@github/copilot@latest" {
					t.Errorf("canceled mutation started process: %+v", r)
					return 17, nil
				}
				close(held)
				<-release
				return 17, nil
			}, agentOptions(home))
			firstDone := make(chan int, 1)
			go func() {
				firstDone <- app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{})
			}()
			select {
			case <-held:
			case <-time.After(5 * time.Second):
				t.Fatal("installation never acquired lock")
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan int, 1)
			go func() { done <- app.Run(ctx, args, process.Streams{}) }()
			select {
			case status := <-done:
				t.Fatalf("mutation bypassed manager lock: %d", status)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case status := <-done:
				if status == 0 {
					t.Fatal("canceled mutation succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("mutation ignored cancellation")
			}
			unblock()
			select {
			case status := <-firstDone:
				if status == 0 {
					t.Fatal("failed installation succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("holder did not finish")
			}
			if after := homeFiles(t, home); !reflect.DeepEqual(before, after) {
				t.Fatalf("canceled mutation changed home: before=%v after=%v", before, after)
			}
		})
	}
}

func TestUpdateRefusesAnInvalidStoredPinUntilUnpinIsRequested(t *testing.T) {
	home := t.TempDir()
	writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
	original := `{"codex":{"version":"1.2.3","pin":"file:/workspace/package"}}` + "\n"
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", original, 0600)
	installs := 0
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if isSessionListRequest(r) {
			return 0, nil
		}
		installs++
		if r.Name != "/usr/bin/npm" || r.Args[len(r.Args)-1] != "@openai/codex@latest" {
			t.Fatalf("unsafe installation: %+v", r)
		}
		writeInstalledPackage(t, home, "@openai/codex", "2.3.4")
		return 0, nil
	}, agentOptions(home))
	status, out, diagnostic := runAgentCommand(app, "update", "agent01", "codex")
	if status == 0 || out != "" || !strings.Contains(diagnostic, "invalid agent version") || installs != 0 {
		t.Fatalf("status=%d out=%q diagnostic=%q installs=%d", status, out, diagnostic, installs)
	}
	data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
	if err != nil || string(data) != original {
		t.Fatalf("selection changed: %s error=%v", data, err)
	}
	status, out, diagnostic = runAgentCommand(app, "update", "agent01", "codex", "--unpin")
	if status != 0 || diagnostic != "" || !strings.Contains(out, "Pin: none.") || installs != 1 {
		t.Fatalf("status=%d out=%q diagnostic=%q installs=%d", status, out, diagnostic, installs)
	}
}
