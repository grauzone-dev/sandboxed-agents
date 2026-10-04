package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestRegisteredCommandCanUseInjectedProcesses(t *testing.T) {
	var requests []process.Request
	var stdout, stderr bytes.Buffer
	runner := func(ctx context.Context, request process.Request) (int, error) {
		requests = append(requests, request)
		request.Streams.Stdout.Write([]byte("scripted output\n"))
		request.Streams.Stderr.Write([]byte("scripted error\n"))
		return 17, nil
	}
	app := manager.New("test-version", runner)
	app.Register("stand-in", func(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
		code, err := run(ctx, process.Request{Name: "stand-in-process", Args: args, Streams: streams})
		if err != nil {
			return err
		}
		if code != 0 {
			return errors.New("stand-in failed")
		}
		return nil
	})
	code := app.Run(context.Background(), []string{"stand-in", "arg with spaces", "--option"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if code == 0 {
		t.Fatal("command failure returned zero")
	}
	if stdout.String() != "scripted output\n" || !strings.Contains(stderr.String(), "scripted error\n") || !strings.Contains(stderr.String(), "stand-in failed") {
		t.Fatalf("streams = %q, %q", stdout.String(), stderr.String())
	}
	if len(requests) != 1 || requests[0].Name != "stand-in-process" || !reflect.DeepEqual(requests[0].Args, []string{"arg with spaces", "--option"}) {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestVersionPrintsManagerVersionWithoutRunningProcesses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := manager.New("v0.1.2", func(context.Context, process.Request) (int, error) {
		t.Fatal("version invoked a process")
		return 1, nil
	})
	code := app.Run(context.Background(), []string{"version"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := stdout.String(); got != "sandboxed-agents-manager v0.1.2\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestInvalidUsageFailsWithoutRunningProcesses(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "missing command"},
		{name: "unknown command", args: []string{"unknown"}},
		{name: "unknown global option", args: []string{"--unknown"}},
		{name: "unknown version option", args: []string{"version", "--unknown"}},
		{name: "extra version argument", args: []string{"version", "extra"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := manager.New("v0.1.2", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid usage invoked a process")
				return 1, nil
			})
			code := app.Run(context.Background(), test.args, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if code == 0 {
				t.Fatal("invalid usage returned zero")
			}
			if got := stderr.String(); got == "" {
				t.Fatal("invalid usage produced no standard error message")
			}
			if got := stdout.String(); got != "" {
				t.Fatalf("stdout = %q, want empty", got)
			}
		})
	}
}

func TestSessionQueryReturnsAnEmptyListWhenNoTmuxServerIsRunning(t *testing.T) {
	var stdout, stderr bytes.Buffer
	calls := 0
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		calls++
		if request.Name != "/usr/bin/tmux" || !reflect.DeepEqual(request.Args, []string{"-L", "sandboxed-agents", "-f", "/dev/null", "list-sessions", "-F", "#{session_name}"}) || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) {
			t.Fatalf("unexpected session query: %+v", request)
		}
		fmt.Fprintln(request.Streams.Stderr, "no server running on /tmp/tmux-1000/sandboxed-agents")
		return 1, nil
	}, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	status := app.Run(context.Background(), []string{"sessions", "list"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 0 || stdout.String() != "[]\n" || stderr.Len() != 0 || calls != 1 {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%d", status, stdout.String(), stderr.String(), calls)
	}
}

func TestSessionQueryRejectsInvalidUsageWithoutRunningProcesses(t *testing.T) {
	for _, args := range [][]string{{"sessions"}, {"sessions", "other"}, {"sessions", "--unknown"}, {"sessions", "list", "extra"}, {"sessions", "list", "--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := manager.New("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid usage invoked a process")
				return 1, nil
			})
			status := app.Run(context.Background(), args, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status == 0 || stdout.Len() != 0 || stderr.String() != "sandboxed-agents-manager sessions list\n" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}
