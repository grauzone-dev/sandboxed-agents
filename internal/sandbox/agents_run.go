package sandbox

import (
	"context"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type RunAgent struct {
	*sandboxObjects
	agentName string
	args      []string
}

func NewRunAgent(name, group, agentName string, args []string, run process.Runner, streams process.Streams) *RunAgent {
	return &RunAgent{sandboxObjects: newSandboxObjects(name, group, run, streams), agentName: agentName, args: args}
}

func (operation *RunAgent) Run(ctx context.Context) (int, error) {
	args := []string{"exec", "--interactive", "--user=1000:1000", "--workdir=/workspace", "--env", "HOME=/home/agent", operation.container, manager.ExecutablePath, "agents", "run", operation.name, operation.agentName}
	args = append(args, operation.args...)
	return operation.run(ctx, process.Request{Name: "podman", Args: args, Streams: operation.streams})
}
