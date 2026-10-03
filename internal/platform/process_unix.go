//go:build !windows

package platform

import (
	"context"
	"os/exec"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func Run(ctx context.Context, request process.Request) (int, error) {
	return run(ctx, exec.CommandContext(ctx, request.Name, request.Args...), request)
}
