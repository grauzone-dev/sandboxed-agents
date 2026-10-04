package manager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type agentExitStatus int

func (status agentExitStatus) Error() string {
	return fmt.Sprint(int(status))
}

func (m *Manager) runAgent(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) < 2 {
		return errors.New(agentcatalog.RunManagerUsage)
	}
	entry, ok := m.options.Catalog.Find(args[1])
	if !ok {
		return fmt.Errorf(agentcatalog.RunUnknownAgentFormat, args[1], strings.Join(m.options.Catalog.Names(), ", "))
	}
	if m.options.User() != agentIdentity {
		return errors.New(agentcatalog.RunIdentity)
	}
	selection, err := readSelection(filepath.Join(m.options.Home, ".local", "state", "sandboxed-agents", "selection.json"))
	if err != nil {
		return err
	}
	if _, enabled := selection[entry.Name]; !enabled {
		return fmt.Errorf(agentcatalog.RunNotEnabledFormat, entry.Name, args[0], entry.Name)
	}
	code, err := run(ctx, process.Request{Name: filepath.Join(m.options.Home, ".local", "bin", entry.Command), Args: args[2:], User: &agentIdentity, Dir: "/workspace", Env: agentEnvironment(m.options.Home), Streams: streams})
	if err != nil {
		return fmt.Errorf(agentcatalog.RunStartFormat, entry.Name, err)
	}
	if code != 0 {
		return agentExitStatus(code)
	}
	return nil
}
