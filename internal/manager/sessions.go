package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Session struct {
	Name  string `json:"name"`
	Agent string `json:"agent"`
}

const sessionPrefix = "sandboxed-agents-"

func (m *Manager) listSessions(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) != 1 || args[0] != "list" {
		return errors.New("sandboxed-agents-manager sessions list")
	}
	identity := m.options.User()
	if identity.UID == 0 {
		return m.forwardToAgentWorker(ctx, []string{"sessions", "list"}, streams, run)
	}

	if identity != agentIdentity {
		return errors.New(sessionQueryIdentity)
	}
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	return json.NewEncoder(streams.Stdout).Encode(sessions)
}

func (m *Manager) tmuxRequest(args []string, streams process.Streams) process.Request {
	environment := append(agentEnvironment(m.options.Home), "LC_ALL=C.UTF-8")
	return process.Request{Name: "/usr/bin/tmux", Args: append([]string{"-L", "sandboxed-agents", "-f", "/dev/null"}, args...), User: &agentIdentity, Dir: m.options.Home, Env: environment, Streams: streams}
}

func (m *Manager) runningSessions(ctx context.Context, run process.Runner) ([]Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out, diagnostic bytes.Buffer
	code, err := run(ctx, m.tmuxRequest([]string{"list-sessions", "-F", "#{session_name}"}, process.Streams{Stdout: &out, Stderr: &diagnostic}))
	if ctx.Err() != nil {
		return nil, fmt.Errorf(agentSessionQueryFailure, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf(agentSessionQueryFailure, err)
	}
	sessions := []Session{}
	if code != 0 {
		message := strings.TrimSpace(diagnostic.String())
		if code == 1 && out.Len() == 0 && (strings.HasPrefix(message, "no server running on ") || (strings.HasPrefix(message, "error connecting to ") && strings.HasSuffix(message, " (No such file or directory)"))) {
			return sessions, nil
		}
		return nil, fmt.Errorf(agentSessionQueryStatusFormat, code, message)
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if !strings.HasPrefix(name, sessionPrefix) {
			continue
		}
		agent := strings.TrimPrefix(name, sessionPrefix)
		if _, ok := m.options.Catalog.Find(agent); !ok || seen[name] {
			return nil, errors.New(agentSessionInvalidList)
		}
		seen[name] = true
		sessions = append(sessions, Session{Name: name, Agent: agent})
	}
	slices.SortFunc(sessions, func(a, b Session) int { return strings.Compare(a.Name, b.Name) })
	return sessions, nil
}

func sessionRunning(sessions []Session, agent string) bool {
	return slices.ContainsFunc(sessions, func(session Session) bool { return session.Agent == agent })
}
