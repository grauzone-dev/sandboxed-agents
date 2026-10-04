package sandbox

import (
	"context"
	"fmt"
	"strconv"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

const UpdateWasRunningLabel = "io.github.sandboxed-agents.update-was-running"

type Update struct {
	*sandboxObjects
	assetHash   string
	image       string
	current     bool
	replacement *Up
}

func NewUpdate(name, group, assetHash string, run process.Runner, streams process.Streams) *Update {
	return &Update{sandboxObjects: newSandboxObjects(name, group, run, streams), assetHash: assetHash}
}

func (update *Update) Prepare(ctx context.Context) error {
	if !update.containerExists {
		return fmt.Errorf(updateNoContainerFormat, update.name)
	}
	if update.containerImage == "" {
		return fmt.Errorf(updateMissingImageIDFormat, update.container)
	}
	var set toolchains.Set
	recorded, present := update.containerLabels[images.ToolchainsLabel]
	if !present {
		return fmt.Errorf(updateInvalidConfigurationFormat, update.container, "toolchain set")
	}
	if recorded != "" {
		var err error
		set, err = toolchains.Parse(recorded)
		if err != nil {
			return fmt.Errorf("%s: %w", fmt.Sprintf(updateInvalidConfigurationFormat, update.container, "toolchain set"), err)
		}
	}
	image, current, err := images.Current(ctx, update.assetHash, set, update.run, update.streams)
	if err != nil {
		return err
	}
	if current && image == update.containerImage {
		update.current = true
		return nil
	}
	replacement, err := update.recordedConfiguration(set)
	if err != nil {
		return err
	}
	update.replacement = replacement
	if !current {
		if _, err = images.Ensure(ctx, update.assetHash, set, update.run, update.streams); err != nil {
			return err
		}
		image, current, err = images.Current(ctx, update.assetHash, set, update.run, update.streams)
		if err != nil {
			return err
		}
		if !current {
			return fmt.Errorf(updateInvalidConfigurationFormat, update.container, "current image")
		}
	}
	update.image = image
	return nil
}

func (update *Update) recordedConfiguration(set toolchains.Set) (*Up, error) {
	var args []string
	for _, field := range []string{"memory", "cpus", "pids-limit", "shm-size", "ssh-port"} {
		value, ok := update.containerLabels["io.github.sandboxed-agents."+field]
		if !ok || value == "" {
			return nil, fmt.Errorf(updateInvalidConfigurationFormat, update.container, field)
		}
		option := field
		if field == "ssh-port" {
			option = "port"
		}
		if _, _, err := ParseUpOptions([]string{"--" + option, value}); err != nil {
			return nil, fmt.Errorf("%s: %w", fmt.Sprintf(updateInvalidConfigurationFormat, update.container, field), err)
		}
		args = append(args, "--"+option, value)
	}
	limits, port, err := ParseUpOptions(args)
	if err != nil {
		return nil, err
	}
	replacement := NewUp(update.name, update.group, update.assetHash, UpOptions{Limits: limits, Port: port, Toolchains: set}, update.run, update.streams)
	kind := update.containerLabels[WorkspaceKindLabel]
	if kind != "volume" && kind != "bind" {
		return nil, fmt.Errorf(updateInvalidConfigurationFormat, update.container, "workspace-kind")
	}
	if len(update.containerMounts) != len(volumeDefinitions) {
		return nil, fmt.Errorf(updateInvalidMountsFormat, update.container)
	}
	for _, volume := range update.volumes {
		var mounted *containerMount
		for i := range update.containerMounts {
			if update.containerMounts[i].Destination == volume.target {
				if mounted != nil {
					return nil, fmt.Errorf(updateInvalidMountsFormat, update.container)
				}
				mounted = &update.containerMounts[i]
			}
		}
		if mounted == nil {
			return nil, fmt.Errorf(updateInvalidMountsFormat, update.container)
		}
		if volume.target == "/workspace" && update.containerLabels[WorkspaceKindLabel] == "bind" {
			if mounted.Type != "bind" || mounted.Source == "" {
				return nil, fmt.Errorf(updateInvalidMountsFormat, update.container)
			}
			replacement.workspaceSource = mounted.Source
			continue
		}
		if mounted.Type != "volume" || mounted.Name != volume.name {
			return nil, fmt.Errorf(updateInvalidMountsFormat, update.container)
		}
		if !volume.exists {
			return nil, fmt.Errorf(updateMissingVolumeFormat, volume.name)
		}
	}
	return replacement, nil
}

func (update *Update) Apply(ctx context.Context) error {
	if update.current {
		_, err := fmt.Fprintln(update.streams.Stdout, fmt.Sprintf(updateAlreadyCurrentFormat, update.name))
		return err
	}
	if err := update.runPodman(ctx, "rename", update.container, update.backup); err != nil {
		return err
	}
	args := update.replacement.createArguments(update.image)
	args = append(args[:len(args)-1], "--label", UpdateWasRunningLabel+"="+strconv.FormatBool(update.containerRunning), update.image)
	if err := update.runPodman(ctx, args...); err != nil {
		return err
	}
	if update.containerRunning {
		if err := update.runPodman(ctx, "stop", update.backup); err != nil {
			return err
		}
	}
	if err := update.runPodman(ctx, "start", update.container); err != nil {
		return err
	}
	if err := WaitReady(ctx, update.container, update.replacement.port, update.run); err != nil {
		return err
	}
	if err := update.runPodman(ctx, "rm", update.backup); err != nil {
		return err
	}
	if !update.containerRunning {
		if err := update.runPodman(ctx, "stop", update.container); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(update.streams.Stdout, fmt.Sprintf(updateSuccessFormat, update.name))
	return err
}
