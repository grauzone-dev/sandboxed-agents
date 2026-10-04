package cli

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func agentCommand(subcommand string, group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var request *sandbox.AgentCommand
	return Command{Name: subcommand, Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return fmt.Errorf("missing sandbox name; use sandboxed-agents agents %s NAME AGENT", subcommand)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return fmt.Errorf("missing agent name; use sandboxed-agents agents %s NAME AGENT", subcommand)
			}
			if len(invocation.Args) > 2 {
				return unexpectedArgument(invocation.Args[2])
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			request = sandbox.NewAgentCommand(invocation.Args[0], *group, invocation.Args[1], subcommand, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return request.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return request.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return request.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return request.CheckRunning() },
		Preconditions:     func(*Invocation) error { return request.CheckManager(ctx) },
	}, Action: func(*Invocation) error { return request.Execute(ctx) }}
}
