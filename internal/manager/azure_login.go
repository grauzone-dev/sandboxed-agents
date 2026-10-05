package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) loginAzure(ctx context.Context, streams process.Streams, run process.Runner) error {
	if m.options.User() != agentIdentity {
		return errors.New(integrations.AzureIdentity)
	}
	status, err := run(ctx, process.Request{
		Name: "az", Args: []string{"login", "--use-device-code"}, User: &agentIdentity, Dir: "/home/agent",
		Env:     []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin", "AZURE_CONFIG_DIR=/home/agent/.azure"},
		Streams: streams,
	})
	if err != nil {
		return fmt.Errorf(integrations.AzureStartFailure, err)
	}
	if status != 0 {
		return fmt.Errorf(integrations.AzureFailure, status)
	}
	return nil
}
