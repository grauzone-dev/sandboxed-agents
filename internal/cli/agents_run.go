package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func runAgentCommand(group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var operation *sandbox.RunAgent
	return Command{Name: "run", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(agentRunMissingSandbox)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return errors.New(agentRunMissingAgent)
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			operation = sandbox.NewRunAgent(invocation.Args[0], *group, invocation.Args[1], invocation.Args[2:], run, process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return operation.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return operation.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return operation.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return operation.CheckRunning() },
		Preconditions:     func(*Invocation) error { return operation.CheckManager(ctx) },
	}, Action: func(*Invocation) error {
		status, err := operation.Run(ctx)
		if err != nil {
			return err
		}
		if status != 0 {
			return exitStatus(status)
		}
		return nil
	}}
}
