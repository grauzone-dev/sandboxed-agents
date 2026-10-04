package manager

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) forwardToAgentWorker(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	code, err := run(ctx, process.Request{Name: ExecutablePath, Args: args, User: &agentIdentity, Dir: "/", Env: agentEnvironment(m.options.Home), Streams: streams})
	if err != nil {
		return fmt.Errorf(managerWorkerStartFailure, err)
	}
	if code != 0 {
		return fmt.Errorf(managerWorkerStatusFormat, code)
	}
	return nil
}
