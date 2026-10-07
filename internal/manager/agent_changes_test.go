package manager_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAgentChangesRefuseWhileTheAffectedSessionRuns(t *testing.T) {
	for _, args := range agentChangeCommands() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := pinnedAgentChangeHome(t)
			before := homeFiles(t, home)
			queries := 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				if request.Name != "/usr/bin/tmux" || request.Args[4] != "list-sessions" {
					t.Fatalf("refusal changed a process: %+v", request)
				}
				queries++
				fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-codex")
				return 0, nil
			}, agentOptions(home))
			status, output, diagnostic := runAgentCommand(app, args[0], args[1], args[2:]...)
			if status == 0 || output != "" || !strings.Contains(diagnostic, "sandboxed-agents-codex") || queries != 1 {
				t.Fatalf("status=%d output=%q diagnostic=%q queries=%d", status, output, diagnostic, queries)
			}
			if !reflect.DeepEqual(before, homeFiles(t, home)) {
				t.Fatal("refusal changed the installation, selection, or pin")
			}

		})
	}
}

func TestForcedAgentChangesStopTheAffectedSessionBeforeChangingInstallation(t *testing.T) {
	for _, args := range agentChangeCommands() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"1.2.3"}}`, 0600)
			writeHomeFile(t, home, ".local/bin/codex", "installed command", 0700)
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			running := true
			stops, installs := 0, 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				if request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("identity=%+v", request)
				}
				if request.Name == "/usr/bin/npm" {
					if running {
						t.Fatal("installation ran before session stopped")
					}
					installs++
					version := "2.3.4"
					if args[0] == "update" && len(args) == 3 {
						version = "1.2.3"
					}
					writeInstalledPackage(t, home, "@openai/codex", version)
					return 0, nil
				}
				if request.Name != "/usr/bin/tmux" {
					t.Fatalf("process=%+v", request)
				}
				switch request.Args[4] {
				case "list-sessions":
					if running {
						fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-codex")
					}
				case "kill-session":
					if !reflect.DeepEqual(request.Args[4:], []string{"kill-session", "-t", "=sandboxed-agents-codex"}) {
						t.Fatal(request.Args)
					}
					stops++
					running = false
				default:
					t.Fatal(request.Args)
				}
				return 0, nil
			}, agentOptions(home))
			status, output, diagnostic := runAgentCommand(app, args[0], args[1], append(args[2:], "--force")...)
			if status != 0 || diagnostic != "" || !strings.Contains(output, "sandboxed-agents-codex") || running || stops != 1 {
				t.Fatalf("status=%d out=%q diagnostic=%q running=%t stops=%d", status, output, diagnostic, running, stops)
			}
			wantInstalls := 1
			if args[0] == "disable" {
				wantInstalls = 0
			}
			if installs != wantInstalls {
				t.Fatalf("installs=%d", installs)
			}
			status, output, diagnostic = runAgentCommand(app, "status", "codex")
			if status != 0 || diagnostic != "" || !strings.Contains(output, "Agent session: not running.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, output, diagnostic)
			}
		})
	}
}

func TestAgentEnableNoOpsPreserveRunningSessions(t *testing.T) {
	for _, args := range [][]string{nil, {"--force"}, {"--version", "1.2.3"}, {"--force", "--version=1.2.3"}, {"--version", "1.2.3", "--force"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"1.2.3"}}`, 0600)
			writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			before := homeFiles(t, home)
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("no-op queried or stopped a session or installed an agent")
				return 1, nil
			}, agentOptions(home))
			status, out, diagnostic := runAgentCommand(app, "enable", "codex", args...)
			if status != 0 || diagnostic != "" || !strings.Contains(out, "version 1.2.3") || !reflect.DeepEqual(before, homeFiles(t, home)) {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
		})
	}
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint("new installation/", force), func(t *testing.T) {
			home := t.TempDir()
			installs := 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if r.Name != "/usr/bin/npm" {
					t.Fatalf("new installation queried or stopped a session: %+v", r)
				}
				installs++
				writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
				return 0, nil
			}, agentOptions(home))
			args := []string{}
			if force {
				args = append(args, "--force")
			}
			status, out, diagnostic := runAgentCommand(app, "enable", "codex", args...)
			if status != 0 || diagnostic != "" || installs != 1 || !strings.Contains(out, "Pin: none.") {
				t.Fatalf("status=%d out=%q diagnostic=%q installs=%d", status, out, diagnostic, installs)
			}
		})
	}
}

func TestAgentChangesPreserveOtherAgentsSessions(t *testing.T) {
	for _, args := range [][]string{{"disable", "codex"}, {"update", "agent01", "codex"}, {"enable", "codex", "--version", "2.3.4"}} {
		for _, force := range []bool{false, true} {
			t.Run(strings.Join(args, " ")+fmt.Sprint(force), func(t *testing.T) {
				home := t.TempDir()
				writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"},"copilot":{"version":"4.5.6","pin":"4.5.6"}}`, 0600)
				writeHomeFile(t, home, ".local/bin/codex", "installed command", 0700)
				writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
				writeInstalledPackage(t, home, "@github/copilot", "4.5.6")
				queries := 0
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					switch r.Name {
					case "/usr/bin/tmux":
						if r.Args[4] != "list-sessions" {
							t.Fatalf("other session changed: %+v", r)
						}
						queries++
						fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-copilot")
					case "/usr/bin/npm":
						writeInstalledPackage(t, home, "@openai/codex", "2.3.4")
					default:
						t.Fatal(r.Name)
					}
					return 0, nil
				}, agentOptions(home))
				options := append([]string{}, args[2:]...)
				if force {
					options = append(options, "--force")
				}
				status, out, diagnostic := runAgentCommand(app, args[0], args[1], options...)
				if status != 0 || diagnostic != "" || strings.Contains(out, "sandboxed-agents-copilot") || queries != 1 {
					t.Fatalf("status=%d out=%q diagnostic=%q queries=%d", status, out, diagnostic, queries)
				}
				status, out, diagnostic = runAgentCommand(app, "status", "copilot")
				if status != 0 || diagnostic != "" || !strings.Contains(out, "Agent session: running.") || !strings.Contains(out, "Pin: 4.5.6.") {
					t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
				}
			})
		}
	}
}

func TestEnableInstalledVersionSetsAPinWithoutTouchingItsRunningSession(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			home := t.TempDir()
			writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"}}`, 0600)
			writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("setting a pin queried/stopped a session or installed an agent")
				return 1, nil
			}, agentOptions(home))
			args := []string{"--version", "1.2.3"}
			if force {
				args = append(args, "--force")
			}
			status, out, diagnostic := runAgentCommand(app, "enable", "codex", args...)
			if status != 0 || diagnostic != "" || !strings.Contains(out, "Pin: 1.2.3.") {
				t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
			}
			data, err := os.ReadFile(filepath.Join(home, ".local/state/sandboxed-agents/selection.json"))
			if err != nil || !strings.Contains(string(data), `"pin":"1.2.3"`) {
				t.Fatalf("selection=%s error=%v", data, err)
			}
		})
	}
}

func TestManagerRejectsForceUsedAsAVersionValueWithoutChanges(t *testing.T) {
	home := t.TempDir()
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("invalid version started a process")
		return 1, nil
	}, agentOptions(home))
	status, out, diagnostic := runAgentCommand(app, "enable", "codex", "--version", "--force", "1.2.3")
	if status == 0 || out != "" || diagnostic == "" || len(homeFiles(t, home)) != 0 {
		t.Fatalf("status=%d out=%q diagnostic=%q", status, out, diagnostic)
	}
}

func agentChangeCommands() [][]string {
	return [][]string{{"disable", "codex"}, {"update", "agent01", "codex"}, {"update", "agent01", "codex", "--unpin"}, {"enable", "codex", "--version", "2.3.4"}}
}
