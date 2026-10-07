package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) updateAgent(ctx context.Context, sandbox string, entry agentcatalog.Entry, unpin, force bool, streams process.Streams, run process.Runner) error {
	selectionPath := m.selectionPath()
	state := filepath.Dir(selectionPath)
	if _, err := os.Stat(state); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, sandbox, entry.Name)
	} else if err != nil {
		return err
	}
	unlock, err := lockManager(ctx, filepath.Join(state, "manager.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	selection, err := readSelection(selectionPath)
	if err != nil {
		return err
	}
	data, enabled := selection[entry.Name]
	if !enabled {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, sandbox, entry.Name)
	}
	var selected selectedAgent
	if err := json.Unmarshal(data, &selected); err != nil {
		return errors.New(agentSelectionInvalid)
	}
	pin := selected.Pin
	if unpin {
		pin = ""
	}
	if pin != "" && !agentcatalog.IsExactVersion(pin) {
		return fmt.Errorf(agentVersionInvalidFormat, pin)
	}
	if err := m.guardAgentChange(ctx, entry.Name, force, streams, run); err != nil {
		return err
	}
	version, err := m.installAgent(ctx, entry, pin, streams, run)
	if err != nil {
		return err
	}
	selected.Version, selected.Pin = version, pin
	if err := saveSelectedAgent(selectionPath, selection, entry.Name, selected); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(streams.Stdout, agentUpdatedVersionFormat, entry.Name, version); err != nil {
		return err
	}
	return reportPin(streams.Stdout, pin)
}
