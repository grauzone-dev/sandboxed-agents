package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func Check(ctx context.Context, name, group, hostOS string, run process.Runner, output io.Writer) error {
	state := newSandboxObjects(name, group, run, process.Streams{})
	if err := state.inspectSandbox(ctx); err != nil {
		return err
	}
	if err := state.inspectBackup(ctx); err != nil {
		return err
	}
	if !state.containerExists && !state.hasVolumes() && !state.backupExists {
		return state.checkContainerPresence()
	}
	var report bytes.Buffer
	problems := false
	finding := func(err error) { problems = true; fmt.Fprintf(&report, checkProblemFormat, err) }
	conflicts := state.ownerConflicts()
	status := sandboxVolumesOnly
	if state.containerExists {
		status = sandboxStopped
		if state.containerRunning {
			status = sandboxRunning
		}
	}
	if state.backupExists {
		status = sandboxUpdateInterrupted
	}
	if len(conflicts) > 0 {
		status = sandboxOwnerConflict
	}
	fmt.Fprintf(&report, checkSandboxFormat, name, status)
	containerStatus := checkContainerAbsent
	if state.containerExists {
		containerStatus = string(sandboxStopped)
		if state.containerRunning {
			containerStatus = string(sandboxRunning)
		}
	}
	fmt.Fprintf(&report, checkContainerFormat, state.container, containerStatus, checkOwner(state.containerOwner))
	if len(conflicts) > 0 {
		finding(ownerConflict(conflicts))
	}
	if err := state.CheckInterruptedUpdate(); err != nil {
		fmt.Fprintf(&report, checkBackupFormat, state.backup, checkOwner(state.backupOwner))
		finding(err)
	}
	bind := ""
	for _, mount := range state.containerMounts {
		if mount.Type == "bind" && mount.Destination == "/workspace" {
			bind = mount.Source
			fmt.Fprintf(&report, checkWorkspaceBindFormat, mount.Source)
		}
	}
	for _, volume := range state.volumes {
		mounted := false
		for _, mount := range state.containerMounts {
			if mount.Type == "volume" && mount.Name == volume.name && mount.Destination == volume.target {
				mounted = true
			}
		}
		required := state.containerExists && !(volume.target == "/workspace" && bind != "")
		volumeStatus := checkVolumePresent
		if !volume.exists || (required && !mounted) {
			volumeStatus = checkVolumeMissing
		}
		if volume.exists && volume.target == "/workspace" && bind != "" {
			volumeStatus = checkVolumeUnused
		}
		if volume.exists || required {
			fmt.Fprintf(&report, checkVolumeFormat, volume.name, volumeStatus, checkOwner(volume.owner))
		}
		if required && (!volume.exists || !mounted) {
			finding(fmt.Errorf(checkMissingVolumeFormat, volume.name, volume.target))
		}
	}
	if state.containerExists {
		for _, limit := range defaultResourceLimits().limits {
			value := state.containerLabels[limit.label]
			if value == "" {
				value = checkLimitMissing
			}
			fmt.Fprintf(&report, checkResourceFormat, strings.TrimPrefix(limit.option, "--"), value)
		}
	}
	if len(conflicts) > 0 {
		report.WriteString(checkAccessOwnerSkipped)
	} else if !state.containerRunning {
		report.WriteString(checkManagerSkipped)
	} else {
		probeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := probeManagerVersion(probeContext, state.container, run)
		cancel()
		if err != nil {
			finding(fmt.Errorf(checkManagerFailedFormat, err))
		} else {
			report.WriteString(checkManagerAnswered)
		}
	}
	setup := &SSHSetup{sandboxObjects: state, hostOS: hostOS}
	installed, err := setup.checkInstalled()
	switch {
	case err != nil:
		finding(fmt.Errorf(checkSSHReadFailedFormat, err))
	case !installed:
		report.WriteString(checkSSHNotInstalled)
	case !state.containerExists:
		problems = true
		fmt.Fprintf(&report, checkSSHStaleFormat, name)
	case len(conflicts) > 0:
	case !state.containerRunning:
		report.WriteString(checkSSHSkipped)
	default:
		if err := setup.checkConnection(ctx); err != nil {
			finding(fmt.Errorf(checkSSHFailedFormat, setup.hostName(), err))
		} else {
			fmt.Fprintf(&report, checkSSHAnsweredFormat, setup.hostName())
		}
	}
	if _, err := io.Copy(output, &report); err != nil {
		return err
	}
	if problems {
		return fmt.Errorf(checkProblemsFormat, name)
	}
	return nil
}

func checkOwner(owner string) string {
	if owner == "" {
		return checkOwnerMissing
	}
	return owner
}

func (setup *SSHSetup) checkInstalled() (bool, error) {
	paths, err := setup.paths()
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(paths.sandboxDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	entry, err := os.ReadFile(filepath.Join(paths.sandboxDirectory, "entry"))
	if err != nil {
		return true, err
	}
	config, err := os.ReadFile(paths.config)
	if err != nil {
		return true, err
	}
	if len(entry) == 0 || !bytes.Contains(config, entry) {
		return true, fmt.Errorf(sshIncompleteFormat, setup.name)
	}
	return true, nil
}

func (setup *SSHSetup) checkConnection(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var diagnostic bytes.Buffer
	args := []string{"-n", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "--", setup.hostName(), "true"}
	status, err := setup.run(ctx, process.Request{Name: "ssh", Args: args, Streams: process.Streams{Stdout: io.Discard, Stderr: &diagnostic}})
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if status != 0 {
		return fmt.Errorf(checkSSHStatusFormat, status, strings.TrimSpace(diagnostic.String()))
	}
	return nil
}
