package manager

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) guardAgentChange(ctx context.Context, agent string, force bool, streams process.Streams, run process.Runner) error {
	sessions, err := m.runningSessions(ctx, run)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Agent == agent {
			if !force {
				return fmt.Errorf(agentChangeRunningFormat, session.Name, session.Agent)
			}
			stopped, err := m.endAgentSession(ctx, agent, streams, run)
			if err != nil {
				return err
			}
			if stopped {
				_, err = fmt.Fprintf(streams.Stdout, agentChangeStoppedFormat, session.Name, session.Agent)
			}
			return err
		}
	}
	return nil
}

func extractAgentForce(args []string) ([]string, bool, error) {
	options := make([]string, 0, len(args))
	force := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--version" && i+1 < len(args) {
			options = append(options, arg, args[i+1])
			i++
			continue
		}
		if arg != "--force" {
			options = append(options, arg)
			continue
		}
		if force {
			return nil, false, fmt.Errorf(agentForceDuplicateMessage)
		}
		force = true
	}
	return options, force, nil
}
