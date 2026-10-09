package livesuite

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type liveExecutable struct {
	run, podman      process.Runner
	connection, path string
}

// invoke checks that the selected Podman target has not changed before each
// command, including cleanup, while each suite part supplies its own streams
// and environment.
func (command liveExecutable) invoke(ctx context.Context, request process.Request) error {
	if err := requireSelectedConnection(ctx, command.podman, command.connection); err != nil {
		return err
	}
	request.Name = command.path
	status, err := command.run(ctx, request)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("%s: exit status %d", request.Args[0], status)
	}
	return nil
}
