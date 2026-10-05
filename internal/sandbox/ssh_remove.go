package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (setup *SSHSetup) CheckRemovalSandbox(ctx context.Context) error {
	if err := setup.CheckSandbox(ctx); err != nil {
		return err
	}
	if setup.containerExists || setup.backupExists || setup.hasVolumes() {
		return nil
	}
	return setup.checkContainerPresence()
}

func (setup *SSHSetup) Remove(ctx context.Context) error {
	removed, err := setup.removeHost()
	if err != nil {
		return err
	}
	if !removed {
		_, err = fmt.Fprintf(setup.streams.Stdout, sshNotPresentFormat, setup.name)
		return err
	}
	if _, err := fmt.Fprintf(setup.streams.Stdout, sshRemovedFormat, setup.name); err != nil {
		return err
	}
	if !setup.containerRunning {
		_, err = fmt.Fprintf(setup.streams.Stdout, sshAuthorizationRemainsFormat, setup.name)
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := setup.CheckManager(ctx); err != nil {
		return fmt.Errorf(sshDeauthorizationFailureFormat, setup.name, err)
	}
	var diagnostic bytes.Buffer
	status, err := setup.run(ctx, process.Request{Name: "podman", Args: managerArgs(setup.container, "ssh", "deauthorize"), Streams: process.Streams{Stderr: &diagnostic}})
	if err == nil && status != 0 {
		err = fmt.Errorf(sshDeauthorizationStatusFormat, strings.TrimSpace(diagnostic.String()))
	}
	if err != nil {
		return fmt.Errorf(sshDeauthorizationFailureFormat, setup.name, err)
	}
	return nil
}

func (setup *SSHSetup) removeHost() (bool, error) {
	paths, err := setup.paths()
	if err != nil {
		return false, err
	}
	entry, err := readOptionalFile(filepath.Join(paths.sandboxDirectory, "entry"))
	if err != nil {
		return false, err
	}
	config, err := readOptionalFile(paths.config)
	if err != nil {
		return false, err
	}
	if len(entry) == 0 {
		entry = setup.managedRemovalEntry(paths, config)
		_, err := os.Lstat(paths.sandboxDirectory)
		if errors.Is(err, os.ErrNotExist) && len(entry) == 0 {
			return false, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	block := sshEntryBlock(entry)
	remaining := config
	if len(entry) > 0 {
		remaining = bytes.ReplaceAll(config, block, nil)
	}
	if len(remaining) > 0 {
		if !bytes.Equal(config, remaining) {
			if err := setup.writeSSHFile(paths.config, remaining, 0600, false); err != nil {
				return false, err
			}
		}
	} else {
		userConfig, err := readOptionalFile(paths.userConfig)
		if err != nil {
			return false, err
		}
		var updated []byte
		for _, line := range bytes.SplitAfter(userConfig, []byte("\n")) {
			if !sshIncludeMatches(string(line), paths.config) {
				updated = append(updated, line...)
			}
		}
		if !bytes.Equal(userConfig, updated) {
			info, err := os.Stat(paths.userConfig)
			if err != nil {
				return false, err
			}
			if err := setup.writeSSHFile(paths.userConfig, updated, info.Mode().Perm(), true); err != nil {
				return false, err
			}
		}
		if err := os.Remove(paths.config); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.RemoveAll(paths.sandboxDirectory); err != nil {
		return false, err
	}
	return true, nil
}

func (remove *Remove) removeSSH() error {
	setup := &SSHSetup{sandboxObjects: remove.sandboxObjects, hostOS: remove.hostOS}
	removed, err := setup.removeHost()
	if err != nil || !removed {
		return err
	}
	if _, err := fmt.Fprintf(remove.streams.Stdout, sshRemovedFormat, remove.name); err != nil {
		return err
	}
	for _, volume := range remove.volumes {
		if volume.target == "/etc/ssh" && volume.exists && !remove.deleteVolumes {
			_, err := fmt.Fprintf(remove.streams.Stdout, sshVolumeAuthorizationRemainsFormat, remove.name)
			return err
		}
	}
	return nil
}

func (setup *SSHSetup) managedRemovalEntry(paths sshPaths, config []byte) []byte {
	hostLine := []byte("Host " + setup.hostName() + "\n")
	identityLine := []byte("\n  IdentityFile " + sshConfigPath(filepath.Join(paths.sandboxDirectory, "id_ed25519")) + "\n")
	var candidate []byte
	active := false
	for _, line := range bytes.SplitAfter(config, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("Host ")) {
			if active && bytes.Equal(line, []byte("Host *\n")) && bytes.Contains(candidate, identityLine) {
				return candidate
			}
			active = bytes.Equal(line, hostLine)
			candidate = nil
		}
		if active {
			candidate = append(candidate, line...)
		}
	}
	return nil
}
