package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

type exitStatus int

func (status exitStatus) Error() string {
	return fmt.Sprint(int(status))
}

func shellCommand(group *string, run process.Runner) Command {
	ctx := context.Background()
	var shell *sandbox.Shell
	return Command{Name: "shell", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(shellMissingName)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) > 1 {
				return unexpectedArgument(invocation.Args[1])
			}
			shell = sandbox.NewShell(invocation.Args[0], *group, run, process.Streams{Stdin: os.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return shell.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return shell.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return shell.CheckInterruptedUpdate() },
		Running: func(invocation *Invocation) error {
			if !shell.Running() {
				return fmt.Errorf(shellStoppedFormat, invocation.Args[0])
			}
			return nil
		},
	}, Action: func(*Invocation) error {
		status, err := shell.Open(ctx, platform.IsTerminal(os.Stdin))
		if err != nil {
			return err
		}
		if status != 0 {
			return exitStatus(status)
		}
		return nil
	}}
}
