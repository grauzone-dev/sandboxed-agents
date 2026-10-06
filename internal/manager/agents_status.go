package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) agentStatus(ctx context.Context, entry agentcatalog.Entry, streams process.Streams, run process.Runner) error {
	selection, err := readSelection(m.selectionPath())
	if err != nil {
		return err
	}
	data, enabled := selection[entry.Name]
	var selected selectedAgent
	if enabled {
		if err := json.Unmarshal(data, &selected); err != nil {
			return errors.New(agentSelectionInvalid)
		}
	}
	var version string
	if enabled {
		version, err = installedVersion(m.options.Home, entry.Install.Package)
		if err != nil {
			return err
		}
	}
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	state := agentStatusSessionStopped
	if sessionRunning(sessions, entry.Name) {
		state = agentStatusSessionRunning
	}
	if !enabled {
		_, err := fmt.Fprintf(streams.Stdout, agentStatusNotEnabledFormat, entry.Name, state)
		return err
	}
	signIn := m.signInState(ctx, entry, run)
	_, err = fmt.Fprintf(streams.Stdout, agentStatusEnabledFormat, entry.Name, version, signIn, state)
	if err != nil {
		return err
	}
	return reportPin(streams.Stdout, selected.Pin)
}

func (m *Manager) signInState(ctx context.Context, entry agentcatalog.Entry, run process.Runner) string {
	if entry.StatusProbe == nil {
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	_, err := run(ctx, process.Request{
		Name: filepath.Join(m.options.Home, ".local", "bin", entry.Command), Args: entry.StatusProbe.Args,
		User: &agentIdentity, Dir: m.options.Home, Env: agentEnvironment(m.options.Home),
		CleanupGroup: true,
		Streams:      process.Streams{Stdout: &output, Stderr: io.Discard},
	})
	if (err != nil && !errors.Is(err, exec.ErrWaitDelay)) || ctx.Err() != nil {
		return "unknown"
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		return "unknown"
	}
	signedIn, ok := fields[entry.StatusProbe.BooleanField].(bool)
	if !ok {
		return "unknown"
	}
	if signedIn {
		return "signed in"
	}
	return "not signed in"
}
