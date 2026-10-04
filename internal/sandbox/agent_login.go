package sandbox

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type AgentLogin struct {
	*sandboxObjects
	agent    string
	workflow string
}

func NewAgentLogin(name, group, agent, workflow string, run process.Runner, streams process.Streams) *AgentLogin {
	return &AgentLogin{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent, workflow: workflow}
}

func (login *AgentLogin) CheckReady(ctx context.Context) error {
	if _, err := RunningSessions(ctx, login.container, login.run); err != nil {
		return fmt.Errorf(managerUnavailableFormat, login.name)
	}
	enabled, err := login.checkAgentEnabled(ctx, login.agent)
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf(agentLoginNotEnabled, login.agent, login.name, login.agent)
	}

	return nil
}

func (login *AgentLogin) managerArgs(args ...string) []string {
	result := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent"}
	result = append(result, "-it", "--env", "SANDBOXED_AGENTS_SANDBOX="+login.name)
	result = append(result, login.container, manager.ExecutablePath, "agents")
	return append(result, args...)
}

func (login *AgentLogin) Apply(ctx context.Context) error {
	args := login.managerArgs("login", login.agent, login.workflow)
	status, err := login.run(ctx, process.Request{Name: "podman", Args: args, Streams: login.streams})
	if err != nil {
		return fmt.Errorf(agentLoginRunFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(agentLoginWorkflowFailure, status)
	}
	return nil
}
