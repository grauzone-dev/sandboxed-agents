package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

type Invocation struct {
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
}

type Handler func(*Invocation) error

type Checks struct {
	Usage             Handler
	Preflight         Handler
	Sandbox           Handler
	Owner             Handler
	InterruptedUpdate Handler
	Running           Handler
	Preconditions     Handler
	Terminal          Handler
	SessionGuard      Handler
}

type Command struct {
	Name     string
	Commands []Command
	Checks   Checks
	Prepare  Handler
	Action   Handler
}

type Tree struct {
	Name     string
	Commands []Command
}

func (tree Tree) Execute(args []string, stdout, stderr io.Writer) int {
	commands := tree.Commands
	path := tree.Name
	var selected *Command
	for len(commands) > 0 {
		if len(args) == 0 {
			return tree.failure(stderr, path, errors.New("missing command"), true)
		}
		selected = nil
		for index := range commands {
			if commands[index].Name == args[0] {
				selected = &commands[index]
				break
			}
		}
		if selected == nil {
			message := "unknown command"
			if strings.HasPrefix(args[0], "-") {
				message = "unknown option"
			}
			return tree.failure(stderr, path, fmt.Errorf("%s %q", message, args[0]), true)
		}
		path += " " + selected.Name
		args = args[1:]
		commands = selected.Commands
	}
	if selected == nil || selected.Action == nil {
		return tree.failure(stderr, path, errors.New("missing command"), true)
	}
	invocation := &Invocation{Args: args, Stdout: stdout, Stderr: stderr}
	if selected.Checks.Usage != nil {
		if err := selected.Checks.Usage(invocation); err != nil {
			return tree.failure(stderr, path, err, true)
		}
	}
	steps := []Handler{
		selected.Checks.Preflight,
		selected.Checks.Sandbox,
		selected.Checks.Owner,
		selected.Checks.InterruptedUpdate,
		selected.Checks.Running,
		selected.Checks.Preconditions,
		selected.Checks.Terminal,
		selected.Prepare,
		selected.Checks.SessionGuard,
	}
	for _, check := range steps {
		if check == nil {
			continue
		}
		if err := check(invocation); err != nil {
			return tree.failure(stderr, path, err, false)
		}
	}
	if err := selected.Action(invocation); err != nil {
		return tree.failure(stderr, path, err, false)
	}
	return 0
}

func (tree Tree) failure(stderr io.Writer, path string, err error, usage bool) int {
	fmt.Fprintf(stderr, "%s: %s\n", tree.Name, err)
	if usage {
		fmt.Fprintf(stderr, "Usage: %s\n", path)
	}
	return 1
}

func Run(args []string, stdout, stderr io.Writer, version, assetHash string) int {
	host := platform.CurrentHost()
	if host.OS == "windows" {
		return RunWithWindowsHost(args, stdout, stderr, version, assetHash, host)
	}
	return RunWithHost(args, stdout, stderr, version, assetHash, preflight.LocalHost())
}

func noArguments(invocation *Invocation) error {
	if len(invocation.Args) == 0 {
		return nil
	}
	return unexpectedArgument(invocation.Args[0])
}

func unexpectedArgument(arg string) error {
	message := "unexpected argument"
	if strings.HasPrefix(arg, "-") {
		message = "unknown option"
	}
	return fmt.Errorf("%s %q", message, arg)
}

func upCommand(assetHash string, run process.Runner, check Handler) Command {
	ctx := context.Background()
	var up *sandbox.Up
	return Command{Name: "up", Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return errors.New("missing sandbox name; use sandboxed-agents up NAME")
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			if len(invocation.Args) > 1 {
				return unexpectedArgument(invocation.Args[1])
			}
			up = sandbox.NewUp(invocation.Args[0], assetHash, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Preflight:         check,
		Sandbox:           func(*Invocation) error { return up.CheckSandbox(ctx) },
		Owner:             func(*Invocation) error { return up.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return up.CheckInterruptedUpdate() },
	}, Action: func(*Invocation) error { return up.Apply(ctx) }}
}

func RunWithWindowsHost(args []string, stdout, stderr io.Writer, version, assetHash string, host platform.Host) int {
	return runWithCheck(args, stdout, stderr, version, assetHash, platform.Run, func(invocation *Invocation) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		report := preflight.CheckWindows(ctx, host, platform.Run)
		for _, result := range report.Results {
			prefix := result.Status
			if _, err := fmt.Fprintf(invocation.Stdout, "%s: %s\n", prefix, result.Message); err != nil {
				return err
			}
		}
		return report.Err()
	})
}

func RunWithHost(args []string, stdout, stderr io.Writer, version, assetHash string, host preflight.Host) int {
	return runWithCheck(args, stdout, stderr, version, assetHash, host.Run, func(invocation *Invocation) error {
		return preflight.Run(context.Background(), host, invocation.Stdout)
	})
}

func runWithCheck(args []string, stdout, stderr io.Writer, version, assetHash string, run process.Runner, check Handler) int {
	tree := Tree{Name: "sandboxed-agents", Commands: []Command{
		upCommand(assetHash, run, check),
		{Name: "build", Checks: Checks{Usage: noArguments, Preflight: check}, Action: func(invocation *Invocation) error {
			if err := images.BuildBase(context.Background(), assetHash, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr}); err != nil {
				return err
			}
			_, err := fmt.Fprintf(invocation.Stdout, "Built image %s.\nExisting sandboxes keep their current image until you update them; list marks them as outdated.\n", images.BaseTag(assetHash))
			return err
		}},
		{Name: "version", Checks: Checks{Usage: noArguments}, Action: func(invocation *Invocation) error {
			_, err := fmt.Fprintf(invocation.Stdout, "sandboxed-agents %s\nassets %s\n", version, assetHash)
			return err
		}},
		{Name: "check", Checks: Checks{Usage: noArguments}, Action: check},
	}}
	return tree.Execute(args, stdout, stderr)
}
