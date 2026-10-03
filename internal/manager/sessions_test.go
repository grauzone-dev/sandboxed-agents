package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSessionsListReturnsEmptyArrayWithoutRunningProcesses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := manager.New("test-version", func(context.Context, process.Request) (int, error) {
		t.Fatal("sessions list invoked a process")
		return 1, nil
	})
	code := app.Run(context.Background(), []string{"sessions", "list"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != "[]\n" {
		t.Fatalf("stdout = %q, want empty JSON array", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestInvalidSessionsUsageFailsWithoutRunningProcesses(t *testing.T) {
	for _, args := range [][]string{
		{"sessions"},
		{"sessions", "unknown"},
		{"sessions", "--unknown"},
		{"sessions", "list", "extra"},
		{"sessions", "list", "--unknown"},
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := manager.New("test-version", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid sessions usage invoked a process")
				return 1, nil
			})
			code := app.Run(context.Background(), args, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if code == 0 {
				t.Fatal("invalid sessions usage returned zero")
			}
			if got := stdout.String(); got != "" {
				t.Fatalf("stdout = %q, want empty", got)
			}
			if got := stderr.String(); got != "usage: sandboxed-agents-manager sessions list\n" {
				t.Fatalf("stderr = %q", got)
			}
		})
	}
}

func TestSessionsListReportsOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	app := manager.New("test-version", func(context.Context, process.Request) (int, error) {
		t.Fatal("sessions list invoked a process")
		return 1, nil
	})
	code := app.Run(context.Background(), []string{"sessions", "list"}, process.Streams{
		Stdout: sessionsFailingWriter{err: errors.New("session output unavailable")},
		Stderr: &stderr,
	})
	if code == 0 {
		t.Fatal("failed output returned zero")
	}
	if got := stderr.String(); got != "session output unavailable\n" {
		t.Fatalf("stderr = %q", got)
	}
}

type sessionsFailingWriter struct {
	err error
}

func (w sessionsFailingWriter) Write([]byte) (int, error) {
	return 0, w.err
}
