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

type EnableAgent struct {
	*sandboxObjects
	agent string
}

var managerVersionResponse = regexp.MustCompile(`^sandboxed-agents-manager [^\s]+\n$`)

func NewEnableAgent(name, group, agent string, run process.Runner, streams process.Streams) *EnableAgent {
	return &EnableAgent{sandboxObjects: newSandboxObjects(name, group, run, streams), agent: agent}
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

func (enable *EnableAgent) Apply(ctx context.Context) error {
	return enable.runPodman(ctx, managerArgs(enable.container, "agents", "enable", enable.agent)...)
}
