package sandbox

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type AgentSession struct {
	*sandboxObjects
	agent string
	stop  bool
}

func NewAgentSession(name, group, agent string, stop bool, run process.Runner, streams process.Streams) *AgentSession {
	return &AgentSession{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent, stop: stop}
}

func (operation *AgentSession) CheckReady(ctx context.Context) error {
	if err := operation.CheckManager(ctx); err != nil {
		return err
	}
	if operation.stop {
		return nil
	}
	enabled, err := operation.checkAgentEnabled(ctx, operation.agent)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf(agentSessionNotEnabled, operation.agent, operation.name, operation.agent)
	}

	return nil
}

func (operation *AgentSession) Apply(ctx context.Context) error {
	args := []string{"exec", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent"}
	if !operation.stop {
		args = append(args, "--interactive", "--tty")
	}
	args = append(args, operation.container, manager.ExecutablePath, "agents", "session", operation.name, operation.agent)
	if operation.stop {
		args = append(args, "--stop")
	}
	status, err := operation.run(ctx, process.Request{Name: "podman", Args: args, Streams: operation.streams})
	if err != nil {
		return fmt.Errorf(agentSessionRunFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(agentSessionFailure, status)
	}
	return nil
}
