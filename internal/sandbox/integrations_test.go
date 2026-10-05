package sandbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func TestNoninteractiveIntegrationExecHasABoundedContext(t *testing.T) {
	for _, args := range [][]string{{"git", "identity", "--name=N", "--email=E"}, {"git", "credentials"}} {
		t.Run(args[1], func(t *testing.T) {
			request, err := integrations.Parse("config", args)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			var execution context.Context
			workflow := sandbox.NewIntegrationWorkflow("agent01", "default", request, func(ctx context.Context, _ process.Request) (int, error) {
				execution = ctx
				deadline, ok := ctx.Deadline()
				if !ok || deadline.Before(started) || deadline.After(time.Now().Add(30*time.Second)) {
					t.Errorf("exec deadline=%v bounded=%v", deadline, ok)
				}
				return 0, nil
			}, process.Streams{})
			if err := workflow.Apply(context.Background()); err != nil {
				t.Fatal(err)
			}
			if execution == nil || !errors.Is(execution.Err(), context.Canceled) {
				t.Fatalf("exec context was not canceled after completion: %v", execution)
			}
		})
	}
}

func TestInteractiveIntegrationExecKeepsTheCallerContext(t *testing.T) {
	for _, test := range []struct {
		kind string
		args []string
	}{
		{"config", []string{"git", "identity", "--email=E"}},
		{"login", []string{"github"}},
		{"login", []string{"azure"}},
	} {
		t.Run(test.args[0], func(t *testing.T) {
			request, err := integrations.Parse(test.kind, test.args)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			workflow := sandbox.NewIntegrationWorkflow("agent01", "default", request, func(execution context.Context, _ process.Request) (int, error) {
				if execution != ctx {
					t.Error("interactive exec replaced the caller context")
				}
				return 0, nil
			}, process.Streams{})
			if err := workflow.Apply(ctx); err != nil {
				t.Fatal(err)
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("interactive exec canceled the caller context: %v", err)
			}
		})
	}
}
