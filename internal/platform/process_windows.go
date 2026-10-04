//go:build windows

package platform

import (
	"context"
	"errors"
	"os/exec"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func Run(ctx context.Context, request process.Request) (int, error) {
	if request.User != nil {
		return 1, errors.New("Unix process identity is unavailable on Windows")
	}
	if request.CleanupGroup {
		return 1, errors.New("Unix process group cleanup is unavailable on Windows")
	}
	return run(ctx, exec.CommandContext(ctx, request.Name, request.Args...), request)
}
