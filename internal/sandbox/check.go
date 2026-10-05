package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Check struct {
	*sandboxObjects
	hostOS string
}

func NewCheck(name, group, hostOS string, run process.Runner, streams process.Streams) *Check {
	return &Check{sandboxObjects: newSandboxObjects(name, group, run, streams), hostOS: hostOS}
}

func (check *Check) CheckSandbox(ctx context.Context) error {
	if err := check.inspectSandbox(ctx); err != nil {
		return err
	}
	if err := check.inspectBackup(ctx); err != nil {
		return err
	}
	if !check.containerExists && !check.hasVolumes() && !check.backupExists {
		return check.checkContainerPresence()
	}
	return nil
}

func (check *Check) Report(ctx context.Context) error {
	report := &checkReport{Check: check, conflicts: check.ownerConflicts()}
	report.describeObjects()
	report.describeVolumes()
	report.describeLimits()
	report.checkManager(ctx)
	accessErr := report.checkSSH(ctx)
	if _, err := io.Copy(check.streams.Stdout, &report.output); err != nil {
		return err
	}
	if accessErr != nil {
		return accessErr
	}
	if report.problems {
		return fmt.Errorf(checkProblemsFormat, check.name)
	}
	return nil
}

type checkReport struct {
	*Check
	output    bytes.Buffer
	problems  bool
	conflicts []string
}

func (report *checkReport) finding(err error) {
	report.problems = true
	fmt.Fprintf(&report.output, checkProblemFormat, err)
}

func (check *Check) containerState() sandboxState {
	if !check.containerExists {
		return sandboxState(checkContainerAbsent)
	}
	if check.containerRunning {
		return sandboxRunning
	}
	return sandboxStopped
}

func (report *checkReport) describeObjects() {
	status := sandboxVolumesOnly
	if report.containerExists {
		status = report.containerState()
	}
	if report.backupExists {
		status = sandboxUpdateInterrupted
	}
	conflicts := report.conflicts
	if len(conflicts) > 0 {
		status = sandboxOwnerConflict
	}
	fmt.Fprintf(&report.output, checkSandboxFormat, report.name, status)
	fmt.Fprintf(&report.output, checkContainerFormat, report.container, report.containerState(), reportedOwner(report.containerOwner))
	if len(conflicts) > 0 {
		report.finding(ownerConflict(conflicts))
	}
	if err := report.CheckInterruptedUpdate(); err != nil {
		fmt.Fprintf(&report.output, checkBackupFormat, report.backup, reportedOwner(report.backupOwner))
		report.finding(err)
	}
}

func (report *checkReport) describeVolumes() {
	bound := false
	for _, mount := range report.containerMounts {
		if mount.Type == "bind" && mount.Destination == "/workspace" {
			bound = true
			fmt.Fprintf(&report.output, checkWorkspaceBindFormat, mount.Source)
		}
	}
	for _, volume := range report.volumes {
		mounted := false
		for _, mount := range report.containerMounts {
			if mount.Type == "volume" && mount.Name == volume.name && mount.Destination == volume.target {
				mounted = true
			}
		}
		unused := volume.target == "/workspace" && bound
		required := report.containerExists && !unused
		missing := required && (!volume.exists || !mounted)
		volumeStatus := checkVolumePresent
		if missing {
			volumeStatus = checkVolumeMissing
		} else if unused {
			volumeStatus = checkVolumeUnused
		}
		if volume.exists || required {
			fmt.Fprintf(&report.output, checkVolumeFormat, volume.name, volumeStatus, reportedOwner(volume.owner))
		}
		if missing {
			report.finding(fmt.Errorf(checkMissingVolumeFormat, volume.name, volume.target))
		}
	}
}

func (report *checkReport) describeLimits() {
	if !report.containerExists {
		return
	}
	for _, limit := range defaultResourceLimits().limits {
		value := report.containerLabels[limit.label]
		if value == "" {
			value = checkLimitMissing
		}
		fmt.Fprintf(&report.output, checkResourceFormat, strings.TrimPrefix(limit.option, "--"), value)
	}
}

func (report *checkReport) checkManager(ctx context.Context) {
	if len(report.conflicts) > 0 {
		report.output.WriteString(checkAccessOwnerSkipped)
		return
	}
	if !report.containerRunning {
		report.output.WriteString(checkManagerSkipped)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := probeManagerVersion(ctx, report.container, report.run); err != nil {
		report.finding(fmt.Errorf(checkManagerFailedFormat, err))
	} else {
		report.output.WriteString(checkManagerAnswered)
	}
}

func (report *checkReport) checkSSH(ctx context.Context) error {
	setup := &SSHSetup{sandboxObjects: report.sandboxObjects, hostOS: report.hostOS}
	installed, err := setup.checkInstalled()
	if err != nil {
		return fmt.Errorf(checkSSHReadFailedFormat, err)
	}
	if !installed {
		report.output.WriteString(checkSSHNotInstalled)
		return nil
	}
	if !report.containerExists {
		format := checkSSHStaleFormat
		if report.backupExists || len(report.conflicts) > 0 {
			format = checkSSHWithoutContainerFormat
		}
		report.finding(fmt.Errorf(format, report.name))
		return nil
	}
	if len(report.conflicts) > 0 {
		return nil
	}
	if !report.containerRunning {
		report.output.WriteString(checkSSHSkipped)
		return nil
	}
	if err := setup.checkConnection(ctx); err != nil {
		report.finding(fmt.Errorf(checkSSHFailedFormat, setup.hostName(), err))
	} else {
		fmt.Fprintf(&report.output, checkSSHAnsweredFormat, setup.hostName())
	}
	return nil
}

func reportedOwner(owner string) string {
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
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	config, err := readOptionalFile(paths.config)
	if err != nil {
		return false, err
	}
	return len(setup.managedRemovalEntry(paths, config)) > 0, nil
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
