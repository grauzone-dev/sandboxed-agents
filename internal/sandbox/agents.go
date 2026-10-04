package sandbox

import (
	"bytes"
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
}

var managerVersionResponse = regexp.MustCompile(`^sandboxed-agents-manager [^\s]+\n$`)

func NewAgentCommand(name, group, agent, subcommand string, run process.Runner, streams process.Streams) *AgentCommand {
	return &AgentCommand{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent, subcommand: subcommand}
}

func (objects *sandboxObjects) CheckManager(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	status, err := objects.run(ctx, process.Request{Name: "podman", Args: managerArgs(objects.container, "version"), Streams: process.Streams{Stdout: &output}})
	if err != nil || status != 0 || ctx.Err() != nil || !managerVersionResponse.Match(output.Bytes()) {
		return fmt.Errorf(managerUnavailableFormat, objects.name)
	}
	return nil
}

func managerArgs(container string, args ...string) []string {
	return append([]string{"exec", "--user=0:0", container, manager.ExecutablePath}, args...)
}

func (request *AgentCommand) Execute(ctx context.Context) error {
	return request.runPodman(ctx, managerArgs(request.container, "agents", request.subcommand, request.agent)...)
}
