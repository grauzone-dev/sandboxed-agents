package cli

import (
	"context"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func loginAgentCommand(group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var login *sandbox.AgentLogin
	return Command{Name: "login", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New(agentLoginMissingSandbox)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return errors.New(agentLoginMissingAgent)
			}
			if len(invocation.Args) > 3 {
				return unexpectedArgument(invocation.Args[3])
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			entry, _ := catalog.Find(invocation.Args[1])
			workflow, _, err := entry.LoginWorkflow(invocation.Args[2:]...)
			if err != nil {
				return err
			}
			login = sandbox.NewAgentLogin(invocation.Args[0], *group, entry.Name, workflow, run, process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return login.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return login.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return login.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return login.CheckRunning() },
		Preconditions:     func(*Invocation) error { return login.CheckReady(ctx) },
		Terminal: func(invocation *Invocation) error {
			if !platform.HasInteractiveTerminal(process.Streams{Stdin: invocation.Stdin, Stdout: invocation.Stdout}) {
				return errors.New(agentLoginNeedsTerminal)
			}
			return nil
		},
	}, Action: func(*Invocation) error { return login.Apply(ctx) }}
}
