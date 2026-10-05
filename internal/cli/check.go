package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func checkCommand(host sandboxHost, group *string, runCheck Handler, run process.Runner) Command {
	ctx := context.Background()
	var check *sandbox.Check
	return Command{Name: "check", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) > 1 {
				return errors.New(checkUsage)
			}
			if len(invocation.Args) == 0 {
				return nil
			}
			if strings.HasPrefix(invocation.Args[0], "-") {
				return unexpectedArgument(invocation.Args[0])
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			check = sandbox.NewCheck(invocation.Args[0], *group, host.workspace.OS, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox: func(*Invocation) error {
			if check == nil {
				return nil
			}
			return check.CheckSandbox(ctx)
		},
	}, Action: func(invocation *Invocation) error {
		if check == nil {
			return runCheck(invocation)
		}
		return check.Report(ctx)
	}}
}
