package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func TestSessionQueryRejectsAnAnswerAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessions, err := sandbox.RunningSessions(ctx, "sandboxed-agents.default.agent01", func(_ context.Context, request process.Request) (int, error) {
		cancel()
		fmt.Fprintln(request.Streams.Stdout, "[]")
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) || sessions != nil {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
}
