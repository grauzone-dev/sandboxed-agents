package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func updateCommand(assetHash string, host sandboxHost, group *string, run process.Runner, check Handler) Command {
	ctx := context.Background()
	var update *sandbox.Update
	var all, force bool
	forNamedTarget := func(check Handler) Handler {
		return func(invocation *Invocation) error {
			if all {
				return nil
			}
			return check(invocation)
		}
	}
	command := Command{Name: "update", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(updateMissingNameMessage)
			}
			all = invocation.Args[0] == "--all"
			var options sandbox.UpdateOptions
			var remaining []string
			var err error
			options.Toolchains, options.WithProvided, remaining, err = parseToolchains(invocation.Args[1:])
			if err != nil {
				return err
			}
			if all && options.WithProvided {
				return errors.New(updateAllWithMessage)
			}
			for _, arg := range remaining {
				if arg != "--force" {
					return errors.New(updateUsageMessage)
				}
				if options.Force {
					return fmt.Errorf("duplicate option %q", arg)
				}
				options.Force = true
			}
			force = options.Force
			if all {
				return nil
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			update = sandbox.NewUpdate(invocation.Args[0], *group, assetHash, options, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Preflight:         check,
		Sandbox:           forNamedTarget(func(*Invocation) error { return update.CheckContainer(ctx) }),
		Owner:             forNamedTarget(func(*Invocation) error { return update.CheckOwner(ctx) }),
		InterruptedUpdate: forNamedTarget(func(*Invocation) error { return update.CheckInterruptedUpdate() }),
		SessionGuard:      forNamedTarget(func(*Invocation) error { return update.CheckSessions(ctx) }),
	}, Prepare: forNamedTarget(func(*Invocation) error { return update.Prepare(ctx) }), Action: func(invocation *Invocation) error {
		if all {
			return sandbox.UpdateAll(ctx, host.workspace.OS, *group, assetHash, force, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
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
