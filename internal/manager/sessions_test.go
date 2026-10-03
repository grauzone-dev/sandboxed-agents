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
