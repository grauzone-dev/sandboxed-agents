package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	status, err := login.run(ctx, process.Request{Name: "podman", Args: login.managerArgs(false, "check-enabled", login.agent), Streams: process.Streams{Stdout: &output}})
	if err != nil || status != 0 || ctx.Err() != nil {
		return fmt.Errorf(managerUnavailableFormat, login.name)
	}
	var enabled *bool
	if err := json.Unmarshal(output.Bytes(), &enabled); err != nil || enabled == nil {
		return fmt.Errorf(managerUnavailableFormat, login.name)
	}
	if !*enabled {
		return fmt.Errorf(agentLoginNotEnabled, login.agent, login.name, login.agent)
	}
	return nil
}

func (login *AgentLogin) managerArgs(interactive bool, args ...string) []string {
	result := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent"}
	if interactive {
		result = append(result, "-it", "--env", "SANDBOXED_AGENTS_SANDBOX="+login.name)
	}
	result = append(result, login.container, manager.ExecutablePath, "agents")
	return append(result, args...)
}

func (login *AgentLogin) Apply(ctx context.Context) error {
	args := login.managerArgs(true, "login", login.agent, login.workflow)
	status, err := login.run(ctx, process.Request{Name: "podman", Args: args, Streams: login.streams})
	if err != nil {
		return fmt.Errorf(agentLoginRunFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(agentLoginWorkflowFailure, status)
	}
	return nil
}
