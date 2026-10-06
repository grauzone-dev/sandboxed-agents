package sandbox

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type AgentCommand struct {
	*sandboxObjects
	agent      string
	subcommand string
	options    []string
}

var managerVersionResponse = regexp.MustCompile(`^sandboxed-agents-manager [^\s]+\n$`)

func NewAgentCommand(name, group, agent, subcommand string, run process.Runner, streams process.Streams, options ...string) *AgentCommand {
	return &AgentCommand{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent, subcommand: subcommand, options: options}
}

func (objects *sandboxObjects) CheckManager(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := probeManagerVersion(ctx, objects.container, objects.run); err != nil {
		return fmt.Errorf(managerUnavailableFormat, objects.name)
	}
	return nil
}

func managerArgs(container string, args ...string) []string {
	return append([]string{"exec", "--user=0:0", container, manager.ExecutablePath}, args...)
}

func (request *AgentCommand) Execute(ctx context.Context) error {
	args := []string{"agents", request.subcommand}
	if request.subcommand == "update" {
		args = append(args, request.name)
	}
	args = append(args, request.agent)
	args = append(args, request.options...)
	return request.runPodman(ctx, managerArgs(request.container, args...)...)
}
