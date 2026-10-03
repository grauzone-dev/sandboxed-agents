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

func (enable *EnableAgent) CheckSandbox(ctx context.Context) error {
	return enable.CheckContainer(ctx)
}

func (enable *EnableAgent) CheckRunning() error {
	if !enable.containerRunning {
		return fmt.Errorf("sandbox %[1]s is stopped; run sandboxed-agents start %[1]s", enable.name)
	}
	return nil
}

func (enable *EnableAgent) CheckManager(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	status, err := enable.run(ctx, process.Request{Name: "podman", Args: enable.managerArgs("version"), Streams: process.Streams{Stdout: &output}})
	if err != nil || status != 0 || ctx.Err() != nil || !managerVersionResponse.Match(output.Bytes()) {
		return fmt.Errorf("manager does not answer in sandbox %[1]s; run sandboxed-agents check %[1]s for diagnosis, then sandboxed-agents restart %[1]s", enable.name)
	}
	return nil
}

func (enable *EnableAgent) managerArgs(args ...string) []string {
	return append([]string{"exec", "--user=0:0", enable.container, manager.ExecutablePath}, args...)
}

func (enable *EnableAgent) Apply(ctx context.Context) error {
	return enable.runPodman(ctx, enable.managerArgs("agents", "enable", enable.agent)...)
}
