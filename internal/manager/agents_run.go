package manager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type agentExitStatus int

func (status agentExitStatus) Error() string {
	return fmt.Sprint(int(status))
}

func (m *Manager) runAgent(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) < 2 {
		return errors.New(agentRunUsage)
	}
	entry, err := m.catalogEntry(args[1])
	if err != nil {
		return err
	}
	if m.options.User() != agentIdentity {
		return errors.New(agentRunIdentity)
	}
	selection, err := readSelection(m.selectionPath())
	if err != nil {
		return err
	}
	if _, enabled := selection[entry.Name]; !enabled {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, args[0], entry.Name)
	}
	return m.executeAgent(ctx, entry, args[2:], agentEnvironment(m.options.Home), streams, run)
}

func (m *Manager) executeAgent(ctx context.Context, entry agentcatalog.Entry, args, environment []string, streams process.Streams, run process.Runner) error {
	code, err := run(ctx, process.Request{Name: filepath.Join(m.options.Home, ".local", "bin", entry.Command), Args: args, User: &agentIdentity, Dir: "/workspace", Env: environment, Streams: streams})
	if err != nil {
		return fmt.Errorf(agentRunStartFormat, entry.Name, err)
	}
	if code != 0 {
		return agentExitStatus(code)
	}
	return nil
}
