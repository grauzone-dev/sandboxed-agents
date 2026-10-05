package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func checkCommand(host sandboxHost, group *string, runCheck Handler, run process.Runner) Command {
	return Command{Name: "check", Checks: Checks{Usage: func(invocation *Invocation) error {
		if len(invocation.Args) > 1 {
			return errors.New(checkUsage)
		}
		if len(invocation.Args) == 1 {
			if strings.HasPrefix(invocation.Args[0], "-") {
				return unexpectedArgument(invocation.Args[0])
			}
			return sandbox.ValidateName(invocation.Args[0])
		}
		return nil
	}}, Action: func(invocation *Invocation) error {
		if len(invocation.Args) == 0 {
			return runCheck(invocation)
		}
		return sandbox.Check(context.Background(), invocation.Args[0], *group, host.workspace.OS, run, invocation.Stdout)
	}}
}
