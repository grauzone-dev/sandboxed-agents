package sandbox

import (
	"context"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Shell struct {
	*sandboxObjects
}

func NewShell(name, group string, run process.Runner, streams process.Streams) *Shell {
	return &Shell{sandboxObjects: newSandboxObjects(name, group, run, streams)}
}

func (shell *Shell) Running() bool {
	return shell.containerRunning
}

func (shell *Shell) Open(ctx context.Context, terminal bool) (int, error) {
	args := []string{"exec", "--interactive"}
	if terminal {
		args = append(args, "--tty")
	}
	args = append(args, "--user=1000:1000", "--workdir=/workspace", shell.container, "/bin/bash")
	return shell.run(ctx, process.Request{Name: "podman", Args: args, Streams: shell.streams})
}
