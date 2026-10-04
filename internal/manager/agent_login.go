package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) login(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New(agentLoginUsage)
	}
	entry, err := m.catalogEntry(args[0])
	if err != nil {
		return err
	}
	_, arguments, err := entry.LoginWorkflow(args[1:]...)
	if err != nil {
		return err
	}
	enabled, err := m.agentEnabled(entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		sandboxName := os.Getenv("SANDBOXED_AGENTS_SANDBOX")
		if sandboxName == "" {
			sandboxName = "NAME"
		}
		return fmt.Errorf(agentLoginNotEnabled, entry.Name, sandboxName, entry.Name)
	}
	if _, err := fmt.Fprintln(streams.Stdout, entry.LoginMessage); err != nil {
		return err
	}
	environment := agentEnvironment(m.options.Home)
	if terminal := os.Getenv("TERM"); terminal != "" {
		environment = append(environment, "TERM="+terminal)
	}
	code, err := run(ctx, process.Request{
		Name: filepath.Join(m.options.Home, ".local", "bin", entry.Command), Args: arguments,
		User: &agentIdentity, Dir: m.options.Home, Env: environment, Streams: streams,
	})
	if err != nil {
		return fmt.Errorf(agentLoginStartFailure, entry.Name, err)
	}
	if code != 0 {
		return fmt.Errorf(agentLoginFailure, entry.Name, code)
	}
	return nil
}

func (m *Manager) checkEnabled(args []string, streams process.Streams) error {
	if len(args) != 1 {
		return errors.New(agentLoginCheckUsage)
	}
	if _, err := m.catalogEntry(args[0]); err != nil {
		return err
	}
	enabled, err := m.agentEnabled(args[0])
	if err != nil {
		return err
	}
	return json.NewEncoder(streams.Stdout).Encode(enabled)
}

func (m *Manager) agentEnabled(name string) (bool, error) {
	if m.options.User() != agentIdentity {
		return false, errors.New(agentLoginIdentity)
	}
	selection, err := readSelection(m.selectionPath())
	if err != nil {
		return false, err
	}
	_, enabled := selection[name]
	return enabled, nil
}
