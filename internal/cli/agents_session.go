package cli

import (
	"context"
	"errors"
	"io"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func sessionAgentCommand(group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var operation *sandbox.AgentSession
	var stop bool
	return Command{Name: "session", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(agentSessionMissingSandbox)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return errors.New(agentSessionMissingAgent)
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			stop = false
			if len(invocation.Args) > 2 {
				if invocation.Args[2] != "--stop" {
					return unexpectedArgument(invocation.Args[2])
				}
				stop = true
			}
			if len(invocation.Args) > 3 {
				return unexpectedArgument(invocation.Args[3])
			}
			operation = sandbox.NewAgentSession(invocation.Args[0], *group, invocation.Args[1], stop, run, process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return operation.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return operation.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return operation.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return operation.CheckRunning() },
		Preconditions:     func(*Invocation) error { return operation.CheckReady(ctx) },
		Terminal: func(invocation *Invocation) error {
			if stop {
				return nil
			}
			if !platform.HasInteractiveTerminal(process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout}) {
				return errors.New(agentSessionNeedsTerminal)
			}
			return nil
		},
	}, Help: func(invocation *Invocation) error {
		_, err := io.WriteString(invocation.Stdout, agentSessionHelp)
		return err
	}, Action: func(*Invocation) error { return operation.Apply(ctx) }}
}
