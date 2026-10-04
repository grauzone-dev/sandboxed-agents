package manager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestRootSessionQueryReportsTheAgentUsersSessions(t *testing.T) {
	home := t.TempDir()
	workerCalls, rootCalls := 0, 0
	worker := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		workerCalls++
		if r.Name != "/usr/bin/tmux" || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
			t.Fatalf("query addresses the wrong user: %+v", r)
		}
		if !reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "list-sessions", "-F", "#{session_name}"}) {
			t.Fatalf("query=%+v", r)
		}
		fmt.Fprint(r.Streams.Stdout, "sandboxed-agents-codex\nsandboxed-agents-claude\nunmanaged\n")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	root := manager.NewWithOptions("test", func(ctx context.Context, r process.Request) (int, error) {
		rootCalls++
		if r.Name != manager.ExecutablePath || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) || !reflect.DeepEqual(r.Args, []string{"sessions", "list"}) || r.Dir != "/" {
			t.Fatalf("root query bypassed trusted worker: %+v", r)
		}
		return worker.Run(ctx, r.Args, r.Streams), nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{} }})
	var out, diagnostic bytes.Buffer
	code := root.Run(context.Background(), []string{"sessions", "list"}, process.Streams{Stdout: &out, Stderr: &diagnostic})
	var got []manager.Session
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []manager.Session{{Name: "sandboxed-agents-claude", Agent: "claude"}, {Name: "sandboxed-agents-codex", Agent: "codex"}}
	if code != 0 || diagnostic.Len() != 0 || !reflect.DeepEqual(got, want) || workerCalls != 1 || rootCalls != 1 {
		t.Fatalf("code=%d out=%s stderr=%s worker=%d root=%d", code, &out, &diagnostic, workerCalls, rootCalls)
	}
}

func TestAgentSessionSurvivesDetachReattachesAndEndsWithItsAgent(t *testing.T) {
	home := t.TempDir()
	writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
	writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
	terminal := sessionTerminal(t)
	running := false
	starts, attaches := 0, 0
	runner := func(_ context.Context, r process.Request) (int, error) {
		if r.Name != "/usr/bin/tmux" || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
			t.Fatalf("wrong session user: %+v", r)
		}
		if len(r.Args) < 5 || !reflect.DeepEqual(r.Args[:4], []string{"-L", "sandboxed-agents", "-f", "/dev/null"}) {
			t.Fatalf("wrong server: %+v", r)
		}
		switch r.Args[4] {
		case "list-sessions":
			if running {
				fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
			}
		case "new-session":
			starts++
			want := []string{"new-session", "-d", "-s", "sandboxed-agents-codex", "-c", "/workspace", manager.ExecutablePath, "agents", "session-worker", "agent01", "codex"}
			if !reflect.DeepEqual(r.Args[4:], want) {
				t.Fatalf("start=%v", r.Args)
			}
			if running {
				t.Fatal("started a duplicate agent")
			}
			running = true
		case "attach-session":
			attaches++
			if !running || !reflect.DeepEqual(r.Args[4:], []string{"attach-session", "-t", "=sandboxed-agents-codex"}) {
				t.Fatalf("attach=%v", r.Args)
			}
		default:
			t.Fatalf("unexpected tmux operation: %v", r.Args)
		}
		return 0, nil
	}
	app := manager.NewWithOptions("test", runner, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	start := func() {
		t.Helper()
		var err bytes.Buffer
		if code := app.Run(context.Background(), []string{"agents", "session", "agent01", "codex"}, process.Streams{Stdin: terminal, Stdout: terminal, Stderr: &err}); code != 0 {
			t.Fatalf("code=%d stderr=%s", code, &err)
		}
	}
	start()
	start()
	if starts != 1 || attaches != 2 || !running {
		t.Fatalf("starts=%d attaches=%d running=%t", starts, attaches, running)
	}
	query := func(want bool) {
		t.Helper()
		var out, err bytes.Buffer
		if code := app.Run(context.Background(), []string{"sessions", "list"}, process.Streams{Stdout: &out, Stderr: &err}); code != 0 {
			t.Fatalf("query=%d %s", code, &err)
		}
		var sessions []manager.Session
		if json.Unmarshal(out.Bytes(), &sessions) != nil || (len(sessions) > 0) != want {
			t.Fatalf("sessions=%s", &out)
		}
	}
	query(true)
	var out, err bytes.Buffer
	if code := app.Run(context.Background(), []string{"agents", "status", "codex"}, process.Streams{Stdout: &out, Stderr: &err}); code != 0 || !strings.Contains(out.String(), "Agent session: running.") {
		t.Fatalf("status=%d out=%s err=%s", code, &out, &err)
	}
	running = false
	query(false)
	out.Reset()
	if code := app.Run(context.Background(), []string{"agents", "status", "codex"}, process.Streams{Stdout: &out, Stderr: &err}); code != 0 || !strings.Contains(out.String(), "Agent session: not running.") {
		t.Fatalf("status=%d out=%s err=%s", code, &out, &err)
	}
	start()
	if starts != 2 || attaches != 3 {
		t.Fatalf("starts=%d attaches=%d", starts, attaches)
	}
}

func TestSessionStopWorksWithoutATerminalAndIsIdempotent(t *testing.T) {
	for _, test := range []struct {
		name             string
		enabled, running bool
	}{{"running", true, true}, {"absent", true, false}, {"disabled", false, false}} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.enabled {
				writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
			}
			running, stops := test.running, 0
			app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
				if r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("root tmux: %+v", r)
				}
				switch r.Args[4] {
				case "list-sessions":
					if running {
						fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
					}
				case "kill-session":
					if !reflect.DeepEqual(r.Args[4:], []string{"kill-session", "-t", "=sandboxed-agents-codex"}) {
						t.Fatal(r.Args)
					}
					stops++
					running = false
				default:
					t.Fatal(r.Args)
				}
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			for i := 0; i < 2; i++ {
				var out, err bytes.Buffer
				code := app.Run(context.Background(), []string{"agents", "session", "agent01", "codex", "--stop"}, process.Streams{Stdout: &out, Stderr: &err})
				if code != 0 || err.Len() != 0 || out.Len() == 0 || running {
					t.Fatalf("code=%d out=%s err=%s running=%t", code, &out, &err, running)
				}
				if i == 1 && !strings.Contains(out.String(), "nothing to do") {
					t.Fatal(out.String())
				}
			}
			want := 0
			if test.running {
				want = 1
			}
			if stops != want {
				t.Fatalf("stops=%d", stops)
			}
		})
	}
}

func TestNotEnabledSessionStopDoesNotClaimTheSessionIsAbsent(t *testing.T) {
	home := t.TempDir()
	queries := 0
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.Name != "/usr/bin/tmux" || r.Args[4] != "list-sessions" {
			t.Fatalf("disabled stop acted on the session: %+v", r)
		}
		queries++
		fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	status := func() {
		t.Helper()
		var out, diagnostic bytes.Buffer
		if code := app.Run(context.Background(), []string{"agents", "status", "codex"}, process.Streams{Stdout: &out, Stderr: &diagnostic}); code != 0 || !strings.Contains(out.String(), "Agent session: running.") || diagnostic.Len() != 0 {
			t.Fatalf("status=%d out=%s stderr=%s", code, &out, &diagnostic)
		}
	}
	status()
	var out, diagnostic bytes.Buffer
	if code := app.Run(context.Background(), []string{"agents", "session", "agent01", "codex", "--stop"}, process.Streams{Stdout: &out, Stderr: &diagnostic}); code != 0 || diagnostic.Len() != 0 || !strings.Contains(out.String(), "not enabled; nothing to do") || strings.Contains(out.String(), "No agent session") {
		t.Fatalf("stop=%d out=%s stderr=%s", code, &out, &diagnostic)
	}
	if queries != 1 {
		t.Fatalf("disabled stop queried tmux: queries=%d", queries)
	}
	status()
}

func TestSessionRefusalsDoNotStartTmux(t *testing.T) {
	for _, test := range []struct {
		name     string
		args     []string
		identity process.Identity
		enabled  bool
		message  string
	}{
		{"worker root", []string{"agents", "session-worker", "agent01", "codex"}, process.Identity{}, true, "UID and GID 1000"},
		{"worker wrong group", []string{"agents", "session-worker", "agent01", "codex"}, process.Identity{UID: 1000, GID: 0}, true, "UID and GID 1000"},
		{"root", []string{"agents", "session", "agent01", "codex"}, process.Identity{}, true, "UID and GID 1000"},
		{"wrong group", []string{"agents", "session", "agent01", "codex"}, process.Identity{UID: 1000, GID: 0}, true, "UID and GID 1000"},
		{"disabled before terminal", []string{"agents", "session", "agent01", "codex"}, process.Identity{UID: 1000, GID: 1000}, false, "agents enable agent01 codex"},
		{"terminal", []string{"agents", "session", "agent01", "codex"}, process.Identity{UID: 1000, GID: 1000}, true, "interactive terminal"},
		{"unknown", []string{"agents", "session", "agent01", "unknown"}, process.Identity{UID: 1000, GID: 1000}, true, "valid agents:"},
		{"usage", []string{"agents", "session", "agent01", "codex", "--invalid"}, process.Identity{UID: 1000, GID: 1000}, true, "usage:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if test.enabled {
				writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) { t.Fatal("refusal invoked tmux"); return 0, nil }, manager.Options{Home: home, User: func() process.Identity { return test.identity }})
			var out, err bytes.Buffer
			if code := app.Run(context.Background(), test.args, process.Streams{Stdout: &out, Stderr: &err}); code == 0 || !strings.Contains(err.String(), test.message) || out.Len() != 0 {
				t.Fatalf("code=%d out=%s err=%s", code, &out, &err)
			}
		})
	}
}

func TestSessionQueryDistinguishesNoServerFromAnUnavailableQuery(t *testing.T) {
	for _, test := range []struct {
		name, out, diagnostic string
		code                  int
		err                   error
		wantOK                bool
	}{
		{name: "no server", diagnostic: "no server running on /tmp/tmux-1000/sandboxed-agents\n", code: 1, wantOK: true},
		{name: "no socket", diagnostic: "error connecting to /tmp/tmux-1000/sandboxed-agents (No such file or directory)\n", code: 1, wantOK: true},
		{name: "permission", diagnostic: "error connecting to /tmp/tmux-1000/sandboxed-agents (Permission denied)\n", code: 1},
		{name: "unexplained failure", code: 1},
		{name: "launch failure", err: errors.New("tmux unavailable")},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "unknown managed session", out: "sandboxed-agents-unknown\n"},
		{name: "duplicate", out: "sandboxed-agents-codex\nsandboxed-agents-codex\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := manager.NewWithOptions("test", func(ctx context.Context, r process.Request) (int, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded query")
				}
				fmt.Fprint(r.Streams.Stdout, test.out)
				fmt.Fprint(r.Streams.Stderr, test.diagnostic)
				return test.code, test.err
			}, manager.Options{Home: t.TempDir(), User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var out, err bytes.Buffer
			code := app.Run(context.Background(), []string{"sessions", "list"}, process.Streams{Stdout: &out, Stderr: &err})
			if (code == 0) != test.wantOK {
				t.Fatalf("code=%d out=%s err=%s", code, &out, &err)
			}
			if test.wantOK && out.String() != "[]\n" {
				t.Fatal(out.String())
			}
			if !test.wantOK && out.Len() != 0 {
				t.Fatal(out.String())
			}
		})
	}
}

func TestSessionChangesWaitForAnInstallationToReleaseTheManagerLock(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			home := t.TempDir()
			writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
			terminal := sessionTerminal(t)
			installationStarted, releaseInstallation := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseInstallation) }) }
			defer release()
			changes := make(chan string, 4)
			runner := func(_ context.Context, r process.Request) (int, error) {
				if r.Name == "/usr/bin/npm" {
					close(installationStarted)
					<-releaseInstallation
					writeInstalledPackage(t, home, "@github/copilot", "1.2.3")
					return 0, nil
				}
				switch r.Args[4] {
				case "list-sessions":
					if stop {
						fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
					}
				case "new-session", "kill-session":
					changes <- r.Args[4]
				case "attach-session":
				default:
					t.Error(r.Args)
				}
				return 0, nil
			}
			app := manager.NewWithOptions("test", runner, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			installed := make(chan int, 1)
			go func() {
				installed <- app.Run(context.Background(), []string{"agents", "enable", "copilot"}, process.Streams{})
			}()
			select {
			case <-installationStarted:
			case <-time.After(time.Second):
				t.Fatal("installation did not start")
			}
			args := []string{"agents", "session", "agent01", "codex"}
			streams := process.Streams{Stdin: terminal, Stdout: terminal}
			if stop {
				args = append(args, "--stop")
				streams = process.Streams{}
			}
			completed := make(chan int, 1)
			go func() { completed <- app.Run(context.Background(), args, streams) }()
			select {
			case operation := <-changes:
				t.Fatalf("%s ran before the manager lock was released", operation)
			case code := <-completed:
				t.Fatalf("session exited %d before lock was released", code)
			case <-time.After(50 * time.Millisecond):
			}
			release()
			select {
			case code := <-installed:
				if code != 0 {
					t.Fatalf("install=%d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("install did not finish")
			}
			select {
			case code := <-completed:
				if code != 0 {
					t.Fatalf("session=%d", code)
				}
			case <-time.After(time.Second):
				t.Fatal("session did not finish")
			}
			select {
			case <-changes:
			default:
				t.Fatal("session did not change")
			}
		})
	}
}

func TestSessionFailuresAreReportedWithoutAnAgentExitStatus(t *testing.T) {
	for _, operation := range []string{"new-session", "attach-session", "kill-session"} {
		for _, launchFailure := range []bool{false, true} {
			t.Run(operation+fmt.Sprint(launchFailure), func(t *testing.T) {
				home := t.TempDir()
				writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
				terminal := sessionTerminal(t)
				app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
					if r.Args[4] == "list-sessions" && operation != "new-session" {
						fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
					}
					if r.Args[4] == operation {
						if launchFailure {
							return 0, errors.New("cannot launch tmux")
						}
						return 17, nil
					}
					return 0, nil
				}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
				args := []string{"agents", "session", "agent01", "codex"}
				streams := process.Streams{Stdin: terminal, Stdout: terminal}
				var diagnostic bytes.Buffer
				streams.Stderr = &diagnostic
				if operation == "kill-session" {
					args = append(args, "--stop")
				}
				if code := app.Run(context.Background(), args, streams); code != 1 || diagnostic.Len() == 0 {
					t.Fatalf("code=%d diagnostic=%s", code, &diagnostic)
				}
			})
		}
	}
}

func TestStatusReportsASessionEvenWhenTheAgentWasDisabled(t *testing.T) {
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.Name != "/usr/bin/tmux" {
			t.Fatalf("disabled agent command ran: %+v", r)
		}
		fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
		return 0, nil
	}, manager.Options{Home: t.TempDir(), User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	var out, err bytes.Buffer
	if code := app.Run(context.Background(), []string{"agents", "status", "codex"}, process.Streams{Stdout: &out, Stderr: &err}); code != 0 || out.String() != "Agent codex is not enabled.\nAgent session: running.\n" || err.Len() != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &err)
	}
}

func TestAgentExitStopsItsSessionByExactName(t *testing.T) {
	for _, test := range []struct {
		name     string
		exit     int
		startErr error
		canceled bool
		wantCode int
	}{
		{name: "success"}, {name: "nonzero", exit: 7}, {name: "start failure", startErr: errors.New("agent executable missing"), wantCode: 1}, {name: "canceled", canceled: true, wantCode: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
			t.Setenv("TERM", "xterm-256color")
			stops, agents := 0, 0
			app := manager.NewWithOptions("test", func(ctx context.Context, r process.Request) (int, error) {
				if r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) {
					t.Fatalf("root process: %+v", r)
				}
				if r.Name == filepath.Join(home, ".local", "bin", "codex") {
					agents++
					if r.Dir != "/workspace" || len(r.Args) != 0 || !slices.Contains(r.Env, "TERM=xterm-256color") {
						t.Fatalf("agent=%+v", r)
					}
					if test.canceled {
						return 1, context.Canceled
					}
					return test.exit, test.startErr
				}
				if r.Name != "/usr/bin/tmux" || !reflect.DeepEqual(r.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "kill-session", "-t", "=sandboxed-agents-codex"}) {
					t.Fatalf("cleanup=%+v", r)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second || ctx.Err() != nil {
					t.Fatalf("cleanup context deadline=%v err=%v", deadline, ctx.Err())
				}
				stops++
				return 0, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			ctx := context.Background()
			if test.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			var diagnostic bytes.Buffer
			if code := app.Run(ctx, []string{"agents", "session-worker", "agent01", "codex"}, process.Streams{Stderr: &diagnostic}); code != test.wantCode || stops != 1 || agents != 1 {
				t.Fatalf("code=%d cleanup calls=%d agents=%d err=%s", code, stops, agents, &diagnostic)
			}
		})
	}
}

func TestStopSucceedsWhenTheAgentExitsBetweenTheQueryAndStop(t *testing.T) {
	home := t.TempDir()
	writeRunSelection(t, home, `{"codex":{"version":"1.2.3"}}`)
	running := true
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		switch r.Args[4] {
		case "list-sessions":
			if running {
				fmt.Fprintln(r.Streams.Stdout, "sandboxed-agents-codex")
			}
		case "kill-session":
			running = false
			fmt.Fprintln(r.Streams.Stderr, "no server running on /tmp/tmux-1000/sandboxed-agents")
			return 1, nil
		default:
			t.Fatal(r.Args)
		}
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	var out, err bytes.Buffer
	if code := app.Run(context.Background(), []string{"agents", "session", "agent01", "codex", "--stop"}, process.Streams{Stdout: &out, Stderr: &err}); code != 0 || !strings.Contains(out.String(), "nothing to do") || err.Len() != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, &out, &err)
	}
}
