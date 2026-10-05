package sandbox

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
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
				if !strings.HasPrefix(key, "GIT_") && !strings.HasPrefix(key, "GH_") && !strings.HasPrefix(key, "GITHUB_") && !strings.HasPrefix(key, "AZURE_") && !strings.HasPrefix(key, "ARM_") && key != "EMAIL" {
					request.Env = append(request.Env, entry)
				}
			}
		}
		return run(ctx, request)
	}
	return &IntegrationWorkflow{sandboxObjects: newSandboxObjects(name, group, cleanRun, streams), request: workflow}
}

func (integration *IntegrationWorkflow) CheckPreconditions(ctx context.Context) error {
	if _, err := RunningSessions(ctx, integration.container, integration.run); err != nil {
		return fmt.Errorf(managerUnavailableFormat, integration.name)
	}
	if integration.request.Integration != "azure" {
		return nil
	}
	var set toolchains.Set
	if recorded := integration.containerLabels[images.ToolchainsLabel]; recorded != "" {
		var err error
		set, err = toolchains.Parse(recorded)
		if err != nil {
			return err
		}
	}
	if !slices.Contains(set.Names(), "azure") {
		selected, err := toolchains.Parse(strings.Join(append(set.Names(), "azure"), ","))
		if err != nil {
			return err
		}
		return fmt.Errorf(integrations.AzureToolchainRequired, integration.name, selected.String())
	}
	return nil
}

func (integration *IntegrationWorkflow) Apply(ctx context.Context) error {
	args := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent"}
	if integration.request.NeedsTerminal() {
		args = append(args, "-it")
	}
	args = append(args, integration.container, manager.ExecutablePath)
	args = append(args, integration.request.Args()...)
	if !integration.request.NeedsTerminal() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	status, err := integration.run(ctx, process.Request{Name: "podman", Args: args, Streams: integration.streams})
	if err != nil {
		return fmt.Errorf(integrations.IntegrationRunFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(integrations.IntegrationFailure, status)
	}
	return nil
}
