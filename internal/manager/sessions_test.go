package manager_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSessionsListReportsOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	app := manager.NewWithOptions("test-version", func(_ context.Context, request process.Request) (int, error) {
		if request.Name != "/usr/bin/tmux" {
			t.Fatalf("unexpected session query: %+v", request)
		}
		return 0, nil
	}, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
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
