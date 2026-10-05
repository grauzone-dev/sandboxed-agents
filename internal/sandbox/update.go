package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

const UpdateWasRunningLabel = "io.github.sandboxed-agents.update-was-running"

type Update struct {
	*sandboxObjects
	assetHash     string
	image         string
	current       bool
	options       UpdateOptions
	replacement   containerConfiguration
	sessions      []manager.Session
	sessionsKnown bool
}

type UpdateOptions struct {
	Toolchains   toolchains.Set
	WithProvided bool
	Force        bool
}

func NewUpdate(name, group, assetHash string, options UpdateOptions, run process.Runner, streams process.Streams) *Update {
	return &Update{sandboxObjects: newSandboxObjects(name, group, run, streams), assetHash: assetHash, options: options}
}

func (update *Update) Prepare(ctx context.Context) error {
	set, missing, err := update.plan(ctx)
	if err != nil || !missing {
		return err
	}
	return update.ensureImage(ctx, set)
}

func (update *Update) plan(ctx context.Context) (toolchains.Set, bool, error) {
	var recordedSet toolchains.Set
	if !update.containerExists {
		return recordedSet, false, fmt.Errorf(updateNoContainerFormat, update.name)
	}
	if update.containerImage == "" {
		return recordedSet, false, fmt.Errorf(updateMissingImageIDFormat, update.container)
	}
	recorded := update.containerLabels[images.ToolchainsLabel]
	if recorded != "" {
		var err error
		recordedSet, err = toolchains.Parse(recorded)
		if err != nil {
			return recordedSet, false, fmt.Errorf("%s: %w", fmt.Sprintf(updateInvalidConfigurationFormat, update.container, "toolchain set"), err)
		}
	}
	selected := recordedSet
	if update.options.WithProvided {
		selected = update.options.Toolchains
	}
	image, current, err := images.Current(ctx, update.assetHash, selected, update.run, update.streams)
	if err != nil {
		return selected, false, err
	}
	if current && image == update.containerImage && selected == recordedSet {
		update.current = true
		return selected, false, nil
	}
	replacement, err := update.replacementConfiguration(selected)
	if err != nil {
		return selected, false, err
	}
	update.replacement = replacement
	update.image = image
	return selected, !current, nil
}

func (update *Update) ensureImage(ctx context.Context, set toolchains.Set) error {
	if _, err := images.Ensure(ctx, update.assetHash, set, update.run, update.streams); err != nil {
		return err
	}
	image, current, err := images.Current(ctx, update.assetHash, set, update.run, update.streams)
	if err != nil {
		return err
	}
	if !current {
		retry := "sandboxed-agents update " + update.name
		if update.options.WithProvided {
			retry += " --with " + toolchains.Display(set.String())
		}
		return fmt.Errorf(updateImageChangedFormat, images.Tag(update.assetHash, set), update.name, retry)
	}
	update.image = image
	return nil
}

func (update *Update) replacementConfiguration(selected toolchains.Set) (containerConfiguration, error) {
	limits, port, err := recordedResourceConfiguration(update.container, update.containerLabels)
	if err != nil {
		return containerConfiguration{}, err
	}
	replacement := containerConfiguration{sandboxName: update.name, controllerGroup: update.group, limits: limits, port: port, toolchains: selected}
	kind := update.containerLabels[WorkspaceKindLabel]
	if kind != "volume" && kind != "bind" {
		return containerConfiguration{}, fmt.Errorf(updateInvalidConfigurationFormat, update.container, "workspace-kind")
	}
	if len(update.containerMounts) != len(volumeDefinitions) {
		return containerConfiguration{}, fmt.Errorf(updateInvalidMountsFormat, update.container)
	}
	for _, volume := range update.volumes {
		var mounted *containerMount
		for i := range update.containerMounts {
			if update.containerMounts[i].Destination == volume.target {
				if mounted != nil {
					return containerConfiguration{}, fmt.Errorf(updateInvalidMountsFormat, update.container)
				}
				mounted = &update.containerMounts[i]
			}
		}
		if mounted == nil {
			return containerConfiguration{}, fmt.Errorf(updateInvalidMountsFormat, update.container)
		}
		if volume.target == "/workspace" && update.containerLabels[WorkspaceKindLabel] == "bind" {
			if mounted.Type != "bind" || mounted.Source == "" {
				return containerConfiguration{}, fmt.Errorf(updateInvalidMountsFormat, update.container)
			}
			replacement.workspaceSource = mounted.Source
			continue
		}
		if mounted.Type != "volume" || mounted.Name != volume.name {
			return containerConfiguration{}, fmt.Errorf(updateInvalidMountsFormat, update.container)
		}
		if !volume.exists {
			return containerConfiguration{}, fmt.Errorf(updateMissingVolumeFormat, volume.name)
		}
	}
	return replacement, nil
}

func (update *Update) Apply(ctx context.Context) (err error) {
	oldStopped := false
	defer func() {
		if oldStopped {
			err = errors.Join(err, update.reportEndedSessions())
		}
	}()
	if update.current {
		_, err := fmt.Fprintln(update.streams.Stdout, fmt.Sprintf(updateAlreadyCurrentFormat, update.name))
		return err
	}
	if err := update.runPodman(ctx, "rename", update.container, update.backup); err != nil {
		return fmt.Errorf(updateStepFailureFormat, update.name, updateRenameStep, err)
	}
	args := update.replacement.createArguments(update.image, UpdateWasRunningLabel+"="+strconv.FormatBool(update.containerRunning))
	if err := update.runPodman(ctx, args...); err != nil {
		return update.rollback(ctx, updateCreateStep, err, false)
	}
	if update.containerRunning {
		if err := update.runPodman(ctx, "stop", update.backup); err != nil {
			return update.rollback(ctx, updateStopOldStep, err, true)
		}
		oldStopped = true
	}
	if err := update.runPodman(ctx, "start", update.container); err != nil {
		return update.rollback(ctx, updateStartNewStep, err, true)
	}
	if err := WaitReady(ctx, update.container, update.replacement.port, update.run); err != nil {
		return update.rollback(ctx, updateReadinessStep, err, true)
	}
	if err := update.runPodman(ctx, "rm", update.backup); err != nil {
		failure := fmt.Errorf(updateBackupRemovalFailureFormat, update.name, update.backup, err)
		if !update.containerRunning {
			return fmt.Errorf("%w%s", failure, updateBackupStillRunningMessage)
		}
		return failure
	}
	if !update.containerRunning {
		if err := update.runPodman(ctx, "stop", update.container); err != nil {
			return fmt.Errorf(updateFinalStopFailureFormat, update.name, err)
		}
	}
	_, err = fmt.Fprintln(update.streams.Stdout, fmt.Sprintf(updateSuccessFormat, update.name))
	return err
}

func (update *Update) rollback(ctx context.Context, step string, cause error, oldMayHaveStopped bool) error {
	failure := fmt.Errorf(updateStepFailureFormat, update.name, step, cause)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := update.runPodman(ctx, "rm", "--force", "--ignore", update.container); err != nil {
		return errors.Join(failure, fmt.Errorf(updateRollbackFailureFormat, update.name, updateRollbackRemoveStep, err))
	}
	if err := update.runPodman(ctx, "rename", update.backup, update.container); err != nil {
		return errors.Join(failure, fmt.Errorf(updateRollbackFailureFormat, update.name, updateRollbackRenameStep, err))
	}
	if oldMayHaveStopped && update.containerRunning {
		if err := update.runPodman(ctx, "start", update.container); err != nil {
			return errors.Join(failure, fmt.Errorf(updateRollbackFailureFormat, update.name, updateRollbackStartStep, err))
		}
	}
	return fmt.Errorf("%w; %s", failure, fmt.Sprintf(updateRestoredFormat, update.name))
}

func (update *Update) CheckInterruptedUpdate() error {
	if update.backupExists {
		return fmt.Errorf(updateInterruptedFormat, update.backup, update.name)
	}
	return nil
}
