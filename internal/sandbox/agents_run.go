package sandbox

import (
	"context"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type RunAgent struct {
	*sandboxObjects
	agent string
	args  []string
}

func NewRunAgent(name, group, agent string, args []string, run process.Runner, streams process.Streams) *RunAgent {
	return &RunAgent{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent, args: args}
}

func (agent *RunAgent) Run(ctx context.Context) (int, error) {
	args := []string{"exec", "--interactive", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent", agent.container, manager.ExecutablePath, "agents", "run", agent.name, agent.agent}
	args = append(args, agent.args...)
	return agent.run(ctx, process.Request{Name: "podman", Args: args, Streams: agent.streams})
}
