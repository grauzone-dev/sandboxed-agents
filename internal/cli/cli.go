package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

type Invocation struct {
	Args   []string
	Stdin  io.Reader
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
	Help     Handler
}

type Tree struct {
	Usage    Handler
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
	invocation := &Invocation{Args: args, Stdin: os.Stdin, Stdout: stdout, Stderr: stderr}
	if tree.Usage != nil {
		if err := tree.Usage(invocation); err != nil {
			return tree.failure(stderr, path, err, true)
		}
	}
	if selected.Help != nil && slices.Contains(args, "--help") {
		if err := selected.Help(invocation); err != nil {
			return tree.failure(stderr, path, err, true)
		}
		return 0
	}
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
		var status exitStatus
		if errors.As(err, &status) {
			return int(status)
		}
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
	if host.OS == "linux" {
		return RunWithHost(args, stdout, stderr, version, assetHash, preflight.LocalHost())
	}
	return RunWithWindowsHost(args, stdout, stderr, version, assetHash, host)
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

func upCommand(assetHash string, host sandbox.WorkspaceHost, group *string, run process.Runner, check Handler) Command {
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
			workspace, limits, selection, provided, err := parseUpArguments(invocation.Args[1:])
			if err != nil {
				return err
			}
			up = sandbox.NewUp(invocation.Args[0], *group, assetHash, limits, selection, provided, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			if err := up.BindWorkspace(host, workspace); err != nil {
				return err
			}
			return nil
		},
		Preflight:         check,
		Sandbox:           func(*Invocation) error { return up.CheckSandbox(ctx) },
		Owner:             func(*Invocation) error { return up.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return up.CheckInterruptedUpdate() },
		Preconditions:     func(*Invocation) error { return up.CheckOptions() },
	}, Action: func(*Invocation) error { return up.Apply(ctx) }, Help: func(invocation *Invocation) error {
		args := slices.DeleteFunc(slices.Clone(invocation.Args), func(arg string) bool { return arg == "--help" })
		if len(args) > 0 {
			if err := sandbox.ValidateName(args[0]); err != nil {
				return err
			}
			if _, _, _, _, err := parseUpArguments(args[1:]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(invocation.Stdout, upHelp)
		return err
	}}
}

func lifecycleCommand(action sandbox.LifecycleAction, group *string, run process.Runner) Command {
	name := string(action)
	ctx := context.Background()
	var lifecycle *sandbox.Lifecycle
	var force bool
	return Command{Name: name, Checks: Checks{
		Usage: func(invocation *Invocation) error {
			if len(invocation.Args) == 0 {
				return fmt.Errorf("missing sandbox name; use sandboxed-agents %s NAME", name)
			}
			if err := sandbox.ValidateName(invocation.Args[0]); err != nil {
				return err
			}
			for _, arg := range invocation.Args[1:] {
				if arg != "--force" || action == sandbox.Start {
					return unexpectedArgument(arg)
				}
				if force {
					return errors.New("duplicate option \"--force\"")
				}
				force = true
			}
			lifecycle = sandbox.NewLifecycle(invocation.Args[0], *group, action, force, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
			return nil
		},
		Sandbox:           func(*Invocation) error { return lifecycle.CheckContainer(ctx) },
		Owner:             func(*Invocation) error { return lifecycle.CheckOwner(ctx) },
		InterruptedUpdate: func(*Invocation) error { return lifecycle.CheckInterruptedUpdate() },
		Preconditions:     func(*Invocation) error { return lifecycle.CheckManager(ctx) },
		SessionGuard:      func(*Invocation) error { return lifecycle.CheckSessions() },
	}, Action: func(*Invocation) error { return lifecycle.Apply(ctx) }}
}

func RunWithWindowsHost(args []string, stdout, stderr io.Writer, version, assetHash string, host platform.Host) int {
	podman := &windowsPodman{runner: platform.Run}
	run := platform.Run
	if host.OS == "windows" {
		run = podman.run
	}
	return runWithCheck(args, stdout, stderr, version, assetHash, sandbox.WorkspaceHost{OS: host.OS, ReadFile: os.ReadFile}, run, func(invocation *Invocation) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		report := preflight.CheckWindows(ctx, host, podman.invoke)
		for _, result := range report.Results {
			prefix := result.Status
			if _, err := fmt.Fprintf(invocation.Stdout, "%s: %s\n", prefix, result.Message); err != nil {
				return err
			}
		}
		if err := report.Err(); err != nil {
			return err
		}
		podman.connection = report.PodmanConnection
		return nil
	})
}

func RunWithHost(args []string, stdout, stderr io.Writer, version, assetHash string, host preflight.Host) int {
	return runWithCheck(args, stdout, stderr, version, assetHash, sandbox.WorkspaceHost{OS: host.Platform, ReadFile: host.ReadFile}, host.Run, func(invocation *Invocation) error {
		return preflight.Run(context.Background(), host, invocation.Stdout)
	})
}

func runWithCheck(args []string, stdout, stderr io.Writer, version, assetHash string, host sandbox.WorkspaceHost, run process.Runner, check Handler) int {
	var group string
	var buildSelection toolchains.Set
	tree := Tree{Name: "sandboxed-agents", Usage: func(*Invocation) error {
		var err error
		group, err = controllergroup.CurrentGroup()
		return err
	}, Commands: []Command{
		upCommand(assetHash, host, &group, run, check),
		{Name: "list", Checks: Checks{Usage: noArguments}, Action: func(invocation *Invocation) error {
			return sandbox.List(context.Background(), group, run, invocation.Stdout)
		}},
		removeCommand(&group, run),
		lifecycleCommand(sandbox.Start, &group, run),
		lifecycleCommand(sandbox.Stop, &group, run),
		lifecycleCommand(sandbox.Restart, &group, run),
		shellCommand(&group, run),
		{Name: "build", Checks: Checks{Usage: func(invocation *Invocation) error {
			selection, _, remaining, err := parseToolchains(invocation.Args)
			if err != nil {
				return err
			}
			if len(remaining) > 0 {
				return unexpectedArgument(remaining[0])
			}
			buildSelection = selection
			return nil
		}, Preflight: check}, Action: func(invocation *Invocation) error {
			return images.Build(context.Background(), assetHash, buildSelection, run, process.Streams{Stdout: invocation.Stdout, Stderr: invocation.Stderr})
		}},
		{Name: "version", Checks: Checks{Usage: noArguments}, Action: func(invocation *Invocation) error {
			_, err := fmt.Fprintf(invocation.Stdout, "sandboxed-agents %s\nassets %s\n", version, assetHash)
			return err
		}},
		{Name: "check", Checks: Checks{Usage: noArguments}, Action: check},
	}}
	return tree.Execute(args, stdout, stderr)
}

func parseUpArguments(args []string) (string, sandbox.ResourceLimits, toolchains.Set, bool, error) {
	workspace := ""
	if len(args) > 0 && args[0] != "" && !strings.HasPrefix(args[0], "-") {
		workspace = args[0]
		args = args[1:]
	}
	selection, provided, remaining, err := parseToolchains(args)
	if err != nil {
		return workspace, sandbox.ResourceLimits{}, selection, provided, err
	}
	limits, err := sandbox.ParseResourceLimits(remaining)
	return workspace, limits, selection, provided, err
}
