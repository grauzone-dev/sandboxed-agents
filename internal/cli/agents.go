package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func agentCommand(subcommand string, group *string, run process.Runner, catalog agentcatalog.Catalog) Command {
	ctx := context.Background()
	var request *sandbox.AgentCommand
	var help Handler
	if subcommand == "enable" || subcommand == "update" {
		help = func(invocation *Invocation) error {
			text := agentEnableHelp
			if subcommand == "update" {
				text = agentUpdateHelp
			}
			_, err := fmt.Fprint(invocation.Stdout, text)
			return err
		}
	}
	return Command{Name: subcommand, Help: help, Checks: Checks{
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
			options, err := agentOptions(subcommand, invocation.Args[2:])
			if err != nil {
				return err
			}
			if err := validateAgent(invocation.Args[1], catalog); err != nil {
				return err
			}
			request = sandbox.NewAgentCommand(invocation.Args[0], *group, invocation.Args[1], subcommand, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr}, options...)
			return nil
		},
		Sandbox:           func(*Invocation) error { return request.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return request.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return request.CheckInterruptedUpdate() },
		Running:           func(*Invocation) error { return request.CheckRunning() },
		Preconditions:     func(*Invocation) error { return request.CheckManager(ctx) },
	}, Action: func(*Invocation) error { return request.Execute(ctx) }}
}

func agentOptions(subcommand string, args []string) ([]string, error) {
	var options []string
	versionGiven, unpinGiven, forceGiven := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case subcommand == "enable" && (arg == "--version" || strings.HasPrefix(arg, "--version=")):
			if versionGiven {
				return nil, fmt.Errorf(agentVersionDuplicateMessage)
			}
			version := strings.TrimPrefix(arg, "--version=")
			if arg == "--version" {
				if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
					return nil, fmt.Errorf(agentVersionMissingMessage)
				}
				i++
				version = args[i]
			}
			if version == "" {
				return nil, fmt.Errorf(agentVersionMissingMessage)
			}
			if !agentcatalog.IsExactVersion(version) {
				return nil, fmt.Errorf(agentVersionInvalidFormat, version)
			}
			versionGiven = true
			options = append(options, "--version", version)
		case subcommand == "update" && arg == "--unpin":
			if unpinGiven {
				return nil, fmt.Errorf(agentUnpinDuplicateMessage)
			}
			unpinGiven = true
			options = append(options, "--unpin")
		case (subcommand == "enable" || subcommand == "disable" || subcommand == "update") && arg == "--force":
			if forceGiven {
				return nil, fmt.Errorf(agentForceDuplicateMessage)
			}
			forceGiven = true
			options = append(options, "--force")
		default:
			return nil, unexpectedArgument(arg)
		}
	}
	return options, nil
}
