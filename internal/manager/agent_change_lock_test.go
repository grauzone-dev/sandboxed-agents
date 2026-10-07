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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAgentSessionAttachesWhileAnotherInstallationHoldsTheManagerLock(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"1.2.3"}}`, 0600)
	writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
	terminal := sessionTerminal(t)
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var attaches atomic.Int32
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.Name == "/usr/bin/npm" && r.Args[len(r.Args)-1] == "@github/copilot@latest" {
			close(held)
			<-release
			writeInstalledPackage(t, home, "@github/copilot", "4.5.6")
			return 0, nil
		}
		if isSessionListRequest(r) {
			fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
			return 0, nil
		}
		if r.Name == "/usr/bin/tmux" && reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "attach-session", "-t", "=sandboxed-agents-codex"}) {
			if r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
				t.Errorf("attachment uses wrong identity: %+v", r)
				return 1, nil
			}
			attaches.Add(1)
			return 0, nil
		}
		t.Errorf("attachment changed a session or installation: %+v", r)
		return 1, nil
	}, agentOptions(home))
	installed := make(chan struct{})
	var installStatus int
	go func() {
		installStatus = app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{})
		close(installed)
	}()
	defer func() { unblock(); <-installed }()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("installation never acquired the manager lock")
	}
	attached := make(chan int, 1)
	var diagnostic bytes.Buffer
	go func() {
		attached <- app.Run(context.Background(), []string{"agents", "session", "agent01", "codex"}, process.Streams{Stdin: terminal, Stdout: terminal, Stderr: &diagnostic})
	}()
	select {
	case status := <-attached:
		if status != 0 || diagnostic.Len() != 0 || attaches.Load() != 1 {
			t.Fatalf("status=%d diagnostic=%q attachments=%d", status, diagnostic.String(), attaches.Load())
		}
	case <-time.After(5 * time.Second):
		unblock()
		<-attached
		t.Fatal("attachment waited for another installation's manager lock")
	}
	select {
	case <-installed:
		t.Fatal("installation finished before its release")
	default:
	}
	unblock()
	<-installed
	if installStatus != 0 {
		t.Fatalf("installation failed: %d", installStatus)
	}
	status, out, err := runAgentCommand(app, "status", "codex")
	if status != 0 || err != "" || !strings.Contains(out, "Agent session: running.") || !strings.Contains(out, "Pin: 1.2.3.") {
		t.Fatalf("status=%d out=%q diagnostic=%q", status, out, err)
	}
}

func TestAgentMutationGuardsWaitForInstallationsAndSessionCreation(t *testing.T) {
	for _, holder := range []string{"installation", "session creation"} {
		for _, args := range [][]string{{"disable", "codex"}, {"update", "agent01", "codex"}, {"update", "agent01", "codex", "--unpin"}, {"enable", "codex", "--version", "2.3.4"}} {
			t.Run(holder+"/"+strings.Join(args, " "), func(t *testing.T) {
				home := pinnedAgentChangeHome(t)
				terminal := sessionTerminal(t)
				held, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				var running, holderStarted atomic.Bool
				running.Store(holder == "installation")
				guardQueries := make(chan struct{}, 4)
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					if isSessionListRequest(r) {
						if holderStarted.Load() {
							guardQueries <- struct{}{}
						}
						if running.Load() {
							fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
						}
						return 0, nil
					}
					if holder == "installation" && r.Name == "/usr/bin/npm" && r.Args[len(r.Args)-1] == "@github/copilot@latest" {
						holderStarted.Store(true)
						close(held)
						<-release
						writeInstalledPackage(t, home, "@github/copilot", "4.5.6")
						return 0, nil
					}
					if holder == "session creation" && r.Name == "/usr/bin/tmux" && reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "new-session", "-d", "-s", "sandboxed-agents-codex", "-c", "/workspace", manager.ExecutablePath, "agents", "session-worker", "agent01", "codex"}) {
						holderStarted.Store(true)
						close(held)
						<-release
						running.Store(true)
						return 0, nil
					}
					if holder == "session creation" && r.Name == "/usr/bin/tmux" && reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "attach-session", "-t", "=sandboxed-agents-codex"}) && running.Load() {
						return 0, nil
					}
					t.Errorf("guard allowed a mutation: %+v", r)
					return 1, nil
				}, agentOptions(home))
				firstDone := make(chan struct{})
				var firstStatus int
				go func() {
					if holder == "installation" {
						firstStatus = app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{})
					} else {
						firstStatus = app.Run(context.Background(), []string{"agents", "session", "agent01", "codex"}, process.Streams{Stdin: terminal, Stdout: terminal})
					}
					close(firstDone)
				}()
				defer func() { unblock(); <-firstDone }()
				select {
				case <-held:
				case <-time.After(5 * time.Second):
					t.Fatal("holder never acquired the manager lock")
				}
				done := make(chan agentChangeResult, 1)
				mutationFinished := make(chan struct{})
				go func() {
					done <- runAgentChange(app, args)
					close(mutationFinished)
				}()
				defer func() { unblock(); <-mutationFinished }()
				select {
				case <-guardQueries:
					t.Fatal("mutation queried sessions before the manager lock was released")
				case result := <-done:
					t.Fatalf("mutation completed before the manager lock was released: %+v", result)
				case <-time.After(100 * time.Millisecond):
				}
				unblock()
				<-firstDone
				if firstStatus != 0 {
					t.Fatalf("holder failed: %d", firstStatus)
				}
				select {
				case result := <-done:
					if result.status == 0 || result.output != "" || !strings.Contains(result.diagnostic, "sandboxed-agents-codex") || !running.Load() {
						t.Fatalf("guard missed the running session after acquiring the lock: %+v", result)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("mutation never completed after release")
				}
				if len(guardQueries) != 1 {
					t.Fatalf("guard queries after release=%d", len(guardQueries))
				}
				status, output, diagnostic := runAgentCommand(app, "status", "codex")
				if status != 0 || diagnostic != "" || !strings.Contains(output, "version 1.2.3") || !strings.Contains(output, "Pin: 1.2.3.") || !strings.Contains(output, "Agent session: running.") {
					t.Fatalf("status=%d output=%q diagnostic=%q", status, output, diagnostic)
				}
			})
		}
	}
}

type agentChangeResult struct {
	status             int
	output, diagnostic string
}

func runAgentChange(app *manager.Manager, args []string) agentChangeResult {
	status, output, diagnostic := runAgentCommand(app, args[0], args[1], args[2:]...)
	return agentChangeResult{status: status, output: output, diagnostic: diagnostic}
}

func pinnedAgentChangeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"1.2.3"}}`, 0600)
	writeHomeFile(t, home, ".local/state/sandboxed-agents/manager.lock", "", 0600)
	writeHomeFile(t, home, ".local/bin/codex", "installed command", 0700)
	writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
	return home
}

func TestForcedAgentChangeFailuresPreserveInstallationSelectionAndPin(t *testing.T) {
	for _, args := range [][]string{{"disable", "codex"}, {"update", "agent01", "codex"}, {"update", "agent01", "codex", "--unpin"}, {"enable", "codex", "--version", "2.3.4"}} {
		for _, failure := range []struct {
			name, diagnostic string
			queries, stops   int
		}{
			{"query launch", "query unavailable", 1, 0},
			{"query status", "agent session query failed with exit status 17", 1, 0},
			{"query invalid list", "invalid session list", 1, 0},
			{"kill launch", "kill unavailable", 1, 1},
			{"kill status", "ending the agent session failed with exit status 17", 1, 1},
			{"kill reports absence while session runs", "ending the agent session failed with exit status 1", 2, 1},
			{"kill result cannot be verified", "ending the agent session failed with exit status 1", 2, 1},
		} {
			t.Run(strings.Join(args, " ")+"/"+failure.name, func(t *testing.T) {
				home := pinnedAgentChangeHome(t)
				before := homeFiles(t, home)
				queries, stops := 0, 0
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					if isSessionListRequest(r) {
						queries++
						switch failure.name {
						case "query launch":
							return 0, errors.New("query unavailable")
						case "query status":
							fmt.Fprintln(r.Streams.Stderr, "query denied")
							return 17, nil
						case "query invalid list":
							fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-unknown")
							return 0, nil
						case "kill result cannot be verified":
							if queries == 2 {
								return 0, errors.New("verification unavailable")
							}
						}
						fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
						return 0, nil
					}
					if r.Name == "/usr/bin/tmux" && reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "kill-session", "-t", "=sandboxed-agents-codex"}) {
						stops++
						switch failure.name {
						case "kill launch":
							return 0, errors.New("kill unavailable")
						case "kill status":
							return 17, nil
						case "kill reports absence while session runs", "kill result cannot be verified":
							return 1, nil
						}
					}
					t.Errorf("failed session guard changed a process: %+v", r)
					return 1, errors.New("unexpected process")
				}, agentOptions(home))
				forcedArgs := append(append([]string{}, args...), "--force")
				result := runAgentChange(app, forcedArgs)
				if result.status == 0 || result.output != "" || !strings.Contains(result.diagnostic, failure.diagnostic) || queries != failure.queries || stops != failure.stops {
					t.Fatalf("result=%+v queries=%d stops=%d", result, queries, stops)
				}
				if !reflect.DeepEqual(before, homeFiles(t, home)) {
					t.Fatal("failed session guard changed installation, selection, pin, or other home data")
				}
			})
		}
	}
}

func TestAgentChangePreconditionsFailBeforeSessionQueries(t *testing.T) {
	for _, args := range [][]string{{"disable", "codex"}, {"update", "agent01", "codex"}, {"enable", "codex", "--version", "2.3.4"}} {
		for _, setup := range []struct{ name, diagnostic string }{
			{"invalid selection", "agent selection is not a valid JSON object"},
			{"invalid selection entry", "agent selection is not a valid JSON object"},
			{"selection directory", "read agent selection"},
			{"lock directory", "manager.lock"},
			{"missing installed metadata", "read installed version"},
			{"invalid installed metadata", "invalid installed package version"},
			{"invalid stored pin", "invalid agent version"},
		} {
			if strings.Contains(setup.name, "metadata") && args[0] != "enable" || setup.name == "invalid stored pin" && args[0] != "update" {
				continue
			}
			t.Run(strings.Join(args, " ")+"/"+setup.name, func(t *testing.T) {
				home := pinnedAgentChangeHome(t)
				switch setup.name {
				case "invalid selection":
					writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", "{", 0600)
				case "invalid selection entry":
					writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":false}}`, 0600)
				case "selection directory", "lock directory":
					name := "selection.json"
					if setup.name == "lock directory" {
						name = "manager.lock"
					}
					path := filepath.Join(home, ".local/state/sandboxed-agents", name)
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
					writeHomeFile(t, home, ".local/state/sandboxed-agents/"+name+"/preserved", "keep", 0600)
				case "missing installed metadata":
					if err := os.Remove(filepath.Join(home, ".local/lib/node_modules/@openai/codex/package.json")); err != nil {
						t.Fatal(err)
					}
				case "invalid installed metadata":
					writeHomeFile(t, home, ".local/lib/node_modules/@openai/codex/package.json", `{}`, 0600)
				case "invalid stored pin":
					writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3","pin":"file:/workspace/package"}}`, 0600)
				}
				before := homeFiles(t, home)
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					t.Errorf("invalid precondition queried or stopped a session or changed an installation: %+v", r)
					return 1, errors.New("unexpected process")
				}, agentOptions(home))
				forcedArgs := append(append([]string{}, args...), "--force")
				result := runAgentChange(app, forcedArgs)
				if result.status == 0 || result.output != "" || !strings.Contains(result.diagnostic, setup.diagnostic) {
					t.Fatalf("precondition lost its error: %+v", result)
				}
				if !reflect.DeepEqual(before, homeFiles(t, home)) {
					t.Fatal("failed precondition changed home data")
				}
			})
		}
	}
}
