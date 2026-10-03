package platform

import (
	"context"
	"errors"
	"os/exec"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func run(ctx context.Context, cmd *exec.Cmd, request process.Request) (int, error) {
	cmd.Stdin = request.Streams.Stdin
	cmd.Stdout = request.Streams.Stdout
	cmd.Stderr = request.Streams.Stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ctx.Err() != nil {
		return 1, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 1, err
}
