package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func sshServer(stateDirectory string) Command {
	if stateDirectory == "" {
		stateDirectory = "/etc/ssh"
	}
	return func(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
		if len(args) != 1 || args[0] != "start" {
			return errors.New("sandboxed-agents-manager ssh start")
		}
		if err := os.MkdirAll(stateDirectory, 0755); err != nil {
			return fmt.Errorf("initialize SSH server state: %w", err)
		}
		serverArgs := []string{"-f", "/usr/local/etc/sandboxed-agents/sshd_config"}
		for _, keyType := range []string{"ed25519", "ecdsa", "rsa"} {
			key := filepath.Join(stateDirectory, "ssh_host_"+keyType+"_key")
			if _, err := os.Stat(key); errors.Is(err, os.ErrNotExist) {
				status, err := run(ctx, process.Request{Name: "ssh-keygen", Args: []string{"-q", "-t", keyType, "-N", "", "-f", key}, Streams: streams})
				if err != nil {
					return fmt.Errorf("generate SSH host key: %w", err)
				}
				if status != 0 {
					return fmt.Errorf("ssh-keygen exited with status %d", status)
				}
			} else if err != nil {
				return fmt.Errorf("read SSH host key: %w", err)
			}
			serverArgs = append(serverArgs, "-h", key)
		}
		status, err := run(ctx, process.Request{Name: "/usr/sbin/sshd", Args: serverArgs, Streams: streams})
		if err != nil {
			return fmt.Errorf("start SSH server: %w", err)
		}
		if status != 0 {
			return fmt.Errorf("sshd exited with status %d", status)
		}
		return nil
	}
}
