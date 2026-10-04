package platform

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func run(ctx context.Context, cmd *exec.Cmd, request process.Request) (int, error) {
	cmd.Env = request.Env
	cmd.Dir = request.Dir
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
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), nil
		}
		return exit.ExitCode(), nil
	}
	return 1, err
}
