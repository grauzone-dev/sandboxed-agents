package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func integrationCommand(kind string, group *string, run process.Runner) Command {
	ctx := context.Background()
	var state *sandbox.Integration
	var request integrations.Request
	return Command{Name: kind, Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return sandbox.ValidateName("")
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			var err error
			request, err = integrations.Parse(kind, invocation.Args[1:])
			if err != nil {
				return err
			}
			state = sandbox.NewIntegration(invocation.Args[0], *group, request, run, process.Streams{Stdin: os.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return state.CheckSandbox(ctx) },
		Owner:             func(*Invocation) error { return state.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return state.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return state.CheckRunning() },
		Preconditions:     func(*Invocation) error { return state.CheckManager(ctx) },
		Terminal: func(*Invocation) error {
			if request.NeedsTerminal() && (!platform.IsTerminal(os.Stdin) || !platform.IsTerminal(os.Stdout)) {
				return fmt.Errorf("%s", integrations.NeedsTerminal)
			}
			return nil
		},
	}, Action: func(*Invocation) error { return state.Apply(ctx) }}
}
