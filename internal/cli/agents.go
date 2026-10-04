package cli

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func agentCommand(operation string, group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var agent *sandbox.AgentOperation
	return Command{Name: operation, Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return fmt.Errorf("missing sandbox name; use sandboxed-agents agents %s NAME AGENT", operation)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return fmt.Errorf("missing agent name; use sandboxed-agents agents %s NAME AGENT", operation)
			}
			if len(invocation.Args) > 2 {
				return unexpectedArgument(invocation.Args[2])
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			agent = sandbox.NewAgentOperation(invocation.Args[0], *group, invocation.Args[1], operation, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return agent.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return agent.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return agent.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return agent.CheckRunning() },
		Preconditions:     func(*Invocation) error { return agent.CheckManager(ctx) },
	}, Action: func(*Invocation) error { return agent.Apply(ctx) }}
}
