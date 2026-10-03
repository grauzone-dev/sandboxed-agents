package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
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
			return tree.failure(stderr, path, fmt.Errorf("unknown command %q", args[0]), true)
		}
		path += " " + selected.Name
		args = args[1:]
		commands = selected.Commands
	}
	if selected == nil || selected.Action == nil {
		return tree.failure(stderr, path, errors.New("missing command"), true)
	}
	invocation := &Invocation{Args: args, Stdout: stdout, Stderr: stderr}
	steps := []Handler{
		selected.Checks.Usage,
		selected.Checks.Preflight,
		selected.Checks.Sandbox,
		selected.Checks.Owner,
		selected.Checks.InterruptedUpdate,
		selected.Checks.Running,
		selected.Checks.Preconditions,
		selected.Checks.Terminal,
		selected.Checks.SessionGuard,
	}
	for index, check := range steps {
		if check == nil {
			continue
		}
		if err := check(invocation); err != nil {
			return tree.failure(stderr, path, err, index == 0)
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
	tree := Tree{Name: "sandboxed-agents", Commands: []Command{
		{Name: "version", Checks: Checks{Usage: func(invocation *Invocation) error {
			if len(invocation.Args) > 0 {
				message := "unexpected argument"
				if strings.HasPrefix(invocation.Args[0], "-") {
					message = "unknown option"
				}
				return fmt.Errorf("%s %q", message, invocation.Args[0])
			}
			return nil
		}}, Action: func(invocation *Invocation) error {
			_, err := fmt.Fprintf(invocation.Stdout, "sandboxed-agents %s\nassets %s\n", version, assetHash)
			return err
		}},
	}}
	return tree.Execute(args, stdout, stderr)
}
