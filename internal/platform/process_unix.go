//go:build !windows

package platform

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func Run(ctx context.Context, request process.Request) (int, error) {
	cmd := exec.CommandContext(ctx, request.Name, request.Args...)
	// A root caller always switches to request.User, which also clears its supplementary groups.
	// Any other caller switches only when its identity differs; without the privilege to change
	// identity the start then fails, so the process never runs under the wrong identity.
	if request.User != nil && (os.Geteuid() == 0 || uint32(os.Geteuid()) != request.User.UID || uint32(os.Getegid()) != request.User.GID) {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: request.User.UID, Gid: request.User.GID, Groups: []uint32{}}}
	}
	return run(ctx, cmd, request)
}
