package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
)

func sshServer(stateDirectory string) Command {
	if stateDirectory == "" {
		stateDirectory = "/etc/ssh"
	}
	return func(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
		wait := len(args) == 2 && args[0] == "host-key" && args[1] == "--wait"
		if len(args) != 1 && !wait {
			return errors.New(sshUsage)
		}
		switch args[0] {
		case "host-key":
			key, err := sshHostKey(ctx, filepath.Join(stateDirectory, "ssh_host_ed25519_key.pub"), wait)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.Stdout, key)
			return err
		case "authorize":
			if streams.Stdin == nil {
				return errors.New("ssh authorize reads the public key from standard input, but none is connected; pass it with podman exec --interactive")
			}
			data, err := io.ReadAll(io.LimitReader(streams.Stdin, sshkeys.MaxPublicKeyBytes+1))
			if err != nil {
				return err
			}
			key, err := sshkeys.ParsePublicKey(data)
			if err != nil {
				return err
			}
			file, err := os.CreateTemp(stateDirectory, ".authorized-keys-*")
			if err != nil {
				return err
			}
			defer os.Remove(file.Name())
			defer file.Close()
			if _, err = io.WriteString(file, key+"\n"); err != nil {
				return err
			}
			if err = file.Chmod(0644); err != nil {
				return err
			}
			if err = file.Sync(); err != nil {
				return err
			}
			if err = file.Close(); err != nil {
				return err
			}
			return os.Rename(file.Name(), filepath.Join(stateDirectory, "authorized_keys"))
		case "deauthorize":
			err := os.Remove(filepath.Join(stateDirectory, "authorized_keys"))
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		case "start":
		default:
			return errors.New(sshUsage)
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

func sshHostKey(ctx context.Context, path string, wait bool) (string, error) {
	if !wait {
		return readSSHHostKey(path)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if key, err := readSSHHostKey(path); err == nil {
			return key, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func readSSHHostKey(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read SSH host key: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, sshkeys.MaxPublicKeyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read SSH host key: %w", err)
	}
	return sshkeys.ParsePublicKey(data)
}
