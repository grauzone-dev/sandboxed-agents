package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) agentSession(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) < 2 || len(args) > 3 || (len(args) == 3 && args[2] != "--stop") {
		return errors.New(agentSessionUsage)
	}
	entry, err := m.catalogEntry(args[1])
	if err != nil {
		return err
	}
	if m.options.User() != agentIdentity {
		return errors.New(agentSessionIdentity)
	}
	if len(args) == 3 {
		return m.stopAgentSession(ctx, entry, streams, run)
	}
	enabled, err := m.agentEnabled(entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, args[0], entry.Name)
	}
	if !platform.HasInteractiveTerminal(streams) {
		return errors.New(agentSessionNeedsTerminal)
	}
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	if !sessionRunning(sessions, entry.Name) {
		if err := m.startAgentSession(ctx, args[0], entry, streams, run); err != nil {
			return err
		}
	}
	return m.attachSession(ctx, entry.Name, streams, run)
}

func (m *Manager) lockSessionChange(ctx context.Context) (func(), error) {
	state := filepath.Dir(m.selectionPath())
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	return lockManager(ctx, filepath.Join(state, "manager.lock"))
}

func (m *Manager) startAgentSession(ctx context.Context, sandboxName string, entry agentcatalog.Entry, streams process.Streams, run process.Runner) error {
	unlock, err := m.lockSessionChange(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	enabled, err := m.agentEnabled(entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, sandboxName, entry.Name)
	}
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	if sessionRunning(sessions, entry.Name) {
		return nil
	}
	request := m.tmuxRequest([]string{"new-session", "-d", "-s", sessionPrefix + entry.Name, "-c", "/workspace", ExecutablePath, "agents", "session-worker", sandboxName, entry.Name}, process.Streams{Stderr: streams.Stderr})
	code, err := run(ctx, request)
	if err != nil {
		return fmt.Errorf(agentSessionStartFailure, err)
	}
	if code != 0 {
		return fmt.Errorf(agentSessionStartStatusFormat, code)
	}
	return nil
}

func (m *Manager) stopAgentSession(ctx context.Context, entry agentcatalog.Entry, streams process.Streams, run process.Runner) error {
	unlock, err := m.lockSessionChange(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	enabled, err := m.agentEnabled(entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		_, err = fmt.Fprintf(streams.Stdout, agentSessionNotEnabledFormat, entry.Name)
		return err
	}
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	if sessionRunning(sessions, entry.Name) {
		var diagnostic bytes.Buffer
		code, err := run(ctx, m.tmuxRequest([]string{"kill-session", "-t", "=" + sessionPrefix + entry.Name}, process.Streams{Stdout: io.Discard, Stderr: &diagnostic}))
		if code == 1 && err == nil {
			remaining, queryErr := m.runningSessions(ctx, run)
			if queryErr == nil && !sessionRunning(remaining, entry.Name) {
				_, err = fmt.Fprintf(streams.Stdout, agentSessionNotRunningFormat, entry.Name)
				return err
			}
		}
		if _, outputErr := io.Copy(streams.Stderr, &diagnostic); outputErr != nil {
			return outputErr
		}
		if err != nil {
			return fmt.Errorf(agentSessionStopFailure, err)
		}
		if code != 0 {
			return fmt.Errorf(agentSessionStopStatusFormat, code)
		}
		_, err = fmt.Fprintf(streams.Stdout, agentSessionStoppedFormat, entry.Name)
		return err
	}
	_, err = fmt.Fprintf(streams.Stdout, agentSessionNotRunningFormat, entry.Name)
	return err
}

func (m *Manager) attachSession(ctx context.Context, agent string, streams process.Streams, run process.Runner) error {
	request := m.tmuxRequest([]string{"attach-session", "-t", "=" + sessionPrefix + agent}, streams)
	if terminal := os.Getenv("TERM"); terminal != "" {
		request.Env = append(request.Env, "TERM="+terminal)
	}
	code, err := run(ctx, request)
	if err != nil {
		return fmt.Errorf(agentSessionAttachFailure, err)
	}
	if code != 0 {
		return fmt.Errorf(agentSessionAttachStatusFormat, code)
	}
	return nil
}

func (m *Manager) runSessionWorker(ctx context.Context, args []string, streams process.Streams, run process.Runner) (err error) {
	if len(args) != 2 {
		return errors.New(agentSessionWorkerUsage)
	}
	entry, err := m.catalogEntry(args[1])
	if err != nil {
		return err
	}
	if m.options.User() != agentIdentity {
		return errors.New(agentSessionIdentity)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		code, stopErr := run(cleanup, m.tmuxRequest([]string{"kill-session", "-t", "=" + sessionPrefix + entry.Name}, process.Streams{Stdout: io.Discard, Stderr: io.Discard}))
		if stopErr != nil {
			err = fmt.Errorf(agentSessionStopFailure, stopErr)
		} else if code != 0 {
			err = fmt.Errorf(agentSessionStopStatusFormat, code)
		}
	}()
	enabled, err := m.agentEnabled(entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf(agentNotEnabledFormat, entry.Name, args[0], entry.Name)
	}
	environment := agentEnvironment(m.options.Home)
	if terminal := os.Getenv("TERM"); terminal != "" {
		environment = append(environment, "TERM="+terminal)
	}
	err = m.executeAgent(ctx, entry, nil, environment, streams, run)
	var status agentExitStatus
	if errors.As(err, &status) {
		return nil
	}
	return err
}
