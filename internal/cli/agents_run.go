package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func runAgentCommand(group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var agent *sandbox.RunAgent
	return Command{Name: "run", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(agentcatalog.RunMissingSandbox)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return errors.New(agentcatalog.RunMissingAgent)
			}
			if _, ok := catalog.Find(invocation.Args[1]); !ok {
				names := catalog.Names()
				if len(names) == 0 {
					return fmt.Errorf(agentcatalog.RunNoAgentsFormat, invocation.Args[1])
				}
				return fmt.Errorf(agentcatalog.RunUnknownAgentFormat, invocation.Args[1], strings.Join(names, ", "))
			}
			agent = sandbox.NewRunAgent(invocation.Args[0], *group, invocation.Args[1], invocation.Args[2:], run, process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return agent.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return agent.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return agent.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return agent.CheckRunning() },
		Preconditions:     func(*Invocation) error { return agent.CheckManager(ctx) },
	}, Action: func(*Invocation) error {
		status, err := agent.Run(ctx)
		if err != nil {
			return err
		}
		if status != 0 {
			return exitStatus(status)
		}
		return nil
	}}
}
