package manager

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Command func(context.Context, []string, process.Streams, process.Runner) error

type Manager struct {
	version  string
	run      process.Runner
	commands map[string]Command
	options  Options
}

type Options struct {
	Home    string
	Catalog *agentcatalog.Catalog
	User    func() process.Identity
}

func New(version string, run process.Runner) *Manager {
	return NewWithOptions(version, run, Options{})
}

func NewWithOptions(version string, run process.Runner, options Options) *Manager {
	if run == nil {
		run = platform.Run
	}
	if options.Home == "" {
		options.Home = "/home/agent"
	}
	if options.Catalog == nil {
		catalog := agentcatalog.Embedded()
		options.Catalog = &catalog
	}
	if options.User == nil {
		options.User = func() process.Identity { return process.Identity{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())} }
	}
	m := &Manager{version: version, run: run, commands: make(map[string]Command), options: options}
	m.Register("sessions", listSessions)
	m.Register("agents", m.agents)
	m.Register("integrations", runIntegrationWorkflow)
	return m
}

func (m *Manager) Register(name string, command Command) {
	m.commands[name] = command
}

func (m *Manager) Run(ctx context.Context, args []string, streams process.Streams) int {
	if streams.Stdout == nil {
		streams.Stdout = io.Discard
	}
	if streams.Stderr == nil {
		streams.Stderr = io.Discard
	}
	if len(args) == 0 {
		fmt.Fprintln(streams.Stderr, "missing command")
		return 1
	}
	if args[0] == "version" {
		if len(args) != 1 {
			fmt.Fprintln(streams.Stderr, "version accepts no arguments or options")
			return 1
		}
		if _, err := fmt.Fprintf(streams.Stdout, "sandboxed-agents-manager %s\n", m.version); err != nil {
			fmt.Fprintln(streams.Stderr, err)
			return 1
		}
		return 0
	}
	command, ok := m.commands[args[0]]
	if !ok {
		fmt.Fprintf(streams.Stderr, "unknown command %q\n", args[0])
		return 1
	}
	if err := command(ctx, args[1:], streams, m.run); err != nil {
		fmt.Fprintln(streams.Stderr, err)
		return 1
	}
	return 0
}
