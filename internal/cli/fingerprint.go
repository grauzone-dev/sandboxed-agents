package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func fingerprintCommand(group *string, run process.Runner) Command {
	ctx := context.Background()
	var fingerprint *sandbox.Fingerprint
	return Command{Name: "fingerprint", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(fingerprintMissingName)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) > 1 {
				return unexpectedArgument(invocation.Args[1])
			}
			fingerprint = sandbox.NewFingerprint(invocation.Args[0], *group, run)
			return nil
		},
		Sandbox:           func(*Invocation) error { return fingerprint.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return fingerprint.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return fingerprint.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return fingerprint.CheckRunning() },
	}, Action: func(invocation *Invocation) error {
		return fingerprint.Print(ctx, invocation.Stdout)
	}}
}
