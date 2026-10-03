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

func enableAgentCommand(group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var enable *sandbox.EnableAgent
	return Command{Name: "enable", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New("missing sandbox name; use sandboxed-agents agents enable NAME AGENT")
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) < 2 {
				return errors.New("missing agent name; use sandboxed-agents agents enable NAME AGENT")
			}
			if len(invocation.Args) > 2 {
				return unexpectedArgument(invocation.Args[2])
			}
			if _, ok := catalog.Find(invocation.Args[1]); !ok {
				names := catalog.Names()
				if len(names) == 0 {
					return fmt.Errorf("unknown agent %q; no valid agent names are available yet", invocation.Args[1])
				}
				return fmt.Errorf("unknown agent %q; valid agents: %s", invocation.Args[1], strings.Join(names, ", "))
			}
			enable = sandbox.NewEnableAgent(invocation.Args[0], *group, invocation.Args[1], run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return enable.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return enable.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return enable.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return enable.CheckRunning() },
		Preconditions:     func(*Invocation) error { return enable.CheckManager(ctx) },
	}, Action: func(*Invocation) error { return enable.Apply(ctx) }}
}
