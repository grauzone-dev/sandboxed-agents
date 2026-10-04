package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func sshConfigCommand(hostOS string, group *string, run process.Runner) Command {
	ctx := context.Background()
	var setup *sandbox.SSHSetup
	var install bool
	return Command{Name: "ssh-config", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(sshMissingName)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			for _, arg := range invocation.Args[1:] {
				if arg != "--install" {
					return unexpectedArgument(arg)
				}
				if install {
					return errors.New(sshDuplicateInstall)
				}
				install = true
			}
			setup = sandbox.NewSSHSetup(invocation.Args[0], *group, hostOS, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return setup.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return setup.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return setup.CheckInterruptedUpdate() },
		Running: func(*Invocation) error {
			if install {
				return setup.CheckRunning()
			}
			return nil
		},
		Preconditions: func(*Invocation) error {
			if install {
				return setup.CheckManager(ctx)
			}
			return nil
		},
	}, Action: func(*Invocation) error {
		if install {
			return setup.Install(ctx)
		}
		return setup.Print()
	}}
}

func parseSSHConfigFlag(args []string) ([]string, bool, error) {
	remaining := make([]string, 0, len(args))
	install := false
	for _, arg := range args {
		if arg == "--ssh-config" {
			if install {
				return nil, false, errors.New(sshDuplicateFlag)
			}
			install = true
		} else {
			remaining = append(remaining, arg)
		}
	}
	return remaining, install, nil
}
