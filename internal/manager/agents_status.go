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
	if _, enabled := selection[entry.Name]; !enabled {
		_, err := fmt.Fprintf(streams.Stdout, "Agent %s is not enabled.\n", entry.Name)
		return err
	}
	version, err := installedVersion(m.options.Home, entry.Install.Package)
	if err != nil {
		return err
	}
	signIn := m.signInState(ctx, entry, run)
	_, err = fmt.Fprintf(streams.Stdout, "Agent %s is enabled (version %s).\nSign-in state: %s.\n", entry.Name, version, signIn)
	return err
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
