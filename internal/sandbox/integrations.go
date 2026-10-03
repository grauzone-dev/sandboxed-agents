package sandbox

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type IntegrationWorkflow struct {
	*sandboxObjects
	request integrations.Request
}

func NewIntegrationWorkflow(name, group string, workflow integrations.Request, run process.Runner, streams process.Streams) *IntegrationWorkflow {
	cleanRun := func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			environment := request.Env
			if environment == nil {
				environment = os.Environ()
			}
			request.Env = make([]string, 0, len(environment))
			for _, entry := range environment {
				key, _, _ := strings.Cut(entry, "=")
				key = strings.ToUpper(key)
				if !strings.HasPrefix(key, "GIT_") && key != "EMAIL" {
					request.Env = append(request.Env, entry)
				}
			}
		}
		return run(ctx, request)
	}
	return &IntegrationWorkflow{sandboxObjects: newSandboxObjects(name, group, cleanRun, streams), request: workflow}
}

func (integration *IntegrationWorkflow) CheckRunning() error {
	if !integration.containerRunning {
		return fmt.Errorf(integrations.StoppedSandbox, integration.name)
	}
	return nil
}

func (integration *IntegrationWorkflow) CheckManager(ctx context.Context) error {
	if _, err := RunningSessions(ctx, integration.container, integration.run); err != nil {
		return fmt.Errorf(integrations.ManagerUnavailable, integration.name)
	}
	return nil
}

func (integration *IntegrationWorkflow) Apply(ctx context.Context) error {
	args := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent"}
	if integration.request.NeedsTerminal() {
		args = append(args, "-it")
	}
	args = append(args, integration.container, "/usr/local/bin/sandboxed-agents-manager")
	args = append(args, integration.request.Args()...)
	status, err := integration.run(ctx, process.Request{Name: "podman", Args: args, Streams: integration.streams})
	if err != nil {
		return fmt.Errorf(integrations.IntegrationStartFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(integrations.IntegrationFailure, status)
	}
	return nil
}
