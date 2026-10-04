package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func updateCommand(assetHash string, group *string, run process.Runner, check Handler) Command {
	ctx := context.Background()
	var update *sandbox.Update
	return Command{Name: "update", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(updateMissingNameMessage)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) != 1 {
				return errors.New(updateUsageMessage)
			}
			update = sandbox.NewUpdate(invocation.Args[0], *group, assetHash, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Preflight:         check,
		Sandbox:           func(*Invocation) error { return update.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return update.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return update.CheckInterruptedUpdate() },
	}, Prepare: func(*Invocation) error { return update.Prepare(ctx) },
		Action: func(*Invocation) error { return update.Apply(ctx) }}
}
