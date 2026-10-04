package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func updateCommand(assetHash string, host sandboxHost, group *string, run process.Runner, check Handler) Command {
	ctx := context.Background()
	var update *sandbox.Update
	var all bool
	command := Command{Name: "update", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(updateMissingNameMessage)
			}
			all = invocation.Args[0] == "--all"
			if len(invocation.Args) != 1 {
				return errors.New(updateUsageMessage)
			}
			if all {
				return nil
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			update = sandbox.NewUpdate(invocation.Args[0], *group, assetHash, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Preflight: check,
		Sandbox: func(*Invocation) error {
			if all {
				return nil
			}
			return update.CheckContainer(ctx)
		},
		Owner: func(*Invocation) error {
			if all {
				return nil
			}
			return update.CheckOwner(ctx)
		},
		InterruptedUpdate: func(*Invocation) error {
			if all {
				return nil
			}
			return update.CheckInterruptedUpdate()
		},
	}, Prepare: func(*Invocation) error {
		if all {
			return nil
		}
		return update.Prepare(ctx)
	}, Action: func(invocation *Invocation) error {
		if all {
			return sandbox.UpdateAll(ctx, host.workspace.OS, *group, assetHash, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
		}
		return update.Apply(ctx)
	}}
	command = withLifecycleLock(command, host, group)
	lock := command.LockSandbox
	command.LockSandbox = func(invocation *Invocation) (func(), error) {
		if all {
			return func() {}, nil
		}
		return lock(invocation)
	}
	return command
}
