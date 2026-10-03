package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func removeCommand(run process.Runner) Command {
	ctx := context.Background()
	var remove *sandbox.Remove
	return Command{Name: "remove", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New("missing sandbox name; use sandboxed-agents remove NAME")
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			var volumes, force bool
			for _, arg := range invocation.Args[1:] {
				switch {
				case arg == "--volumes" && !volumes:
					volumes = true
				case arg == "--force" && !force:
					force = true
				default:
					return unexpectedArgument(arg)
				}
			}
			remove = sandbox.NewRemove(invocation.Args[0], volumes, force, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return remove.CheckSandbox(ctx) },
		Owner:             func(*Invocation) error { return remove.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return remove.CheckInterruptedUpdate() },
		Preconditions:     func(*Invocation) error { return remove.CheckManager(ctx) },
		SessionGuard:      func(*Invocation) error { return remove.CheckSessions() },
	}, Action: func(*Invocation) error { return remove.Apply(ctx) }}
}
