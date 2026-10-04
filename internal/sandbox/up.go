package sandbox

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

type Up struct {
	*sandboxObjects
	containerConfiguration
	assetHash        string
	agents           []string
	withProvided     bool
	workspaceHost    string
	workspaceOS      string
	sshPortAvailable func(int) (bool, error)
}

type UpOptions struct {
	Limits           ResourceLimits
	Port             int
	Toolchains       toolchains.Set
	WithProvided     bool
	Agents           []string
	SSHPortAvailable func(int) (bool, error)
}

func NewUp(name, group, assetHash string, options UpOptions, run process.Runner, streams process.Streams) *Up {
	available := options.SSHPortAvailable
	if available == nil {
		available = sshPortAvailable
	}
	return &Up{
		sandboxObjects: newSandboxObjects(name, group, run, streams),
		containerConfiguration: containerConfiguration{
			sandboxName: name, controllerGroup: group, limits: options.Limits, port: options.Port, toolchains: options.Toolchains,
		},
		assetHash: assetHash, withProvided: options.WithProvided, agents: options.Agents, sshPortAvailable: available,
	}
}

func (up *Up) Apply(ctx context.Context) error {
	if !up.containerExists {
		if err := up.createSandbox(ctx); err != nil {
			return err
		}
	}
	if !up.containerExists || !up.containerRunning {
		if err := up.runPodman(ctx, "start", up.container); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(up.streams.Stdout, "Sandbox %s is running.\n", up.name); err != nil {
		return err
	}
	return up.enableAgents(ctx)
}

func (up *Up) CheckOptions() error {
	if !up.containerExists {
		return nil
	}
	if err := up.checkWorkspace(); err != nil {
		return err
	}
	if err := up.limits.checkRecorded(up.name, up.containerLabels); err != nil {
		return err
	}
	if up.withProvided && up.containerLabels[images.ToolchainsLabel] != up.toolchains.String() {
		recorded := up.containerLabels[images.ToolchainsLabel]
		if recorded == "" {
			recorded = "none"
		}
		given := up.toolchains.String()
		if given == "" {
			given = "none"
		}
		return fmt.Errorf("toolchain set conflict: recorded %q, given %q; use sandboxed-agents update %s --with %s to change it", recorded, given, up.name, given)
	}
	return nil
}

func (up *Up) createSandbox(ctx context.Context) error {
	image, err := images.Ensure(ctx, up.assetHash, up.toolchains, up.run, up.streams)
	if err != nil {
		return err
	}
	for _, volume := range up.volumes {
		if up.workspaceSource != "" && volume.target == "/workspace" {
			if volume.exists {
				if _, err := fmt.Fprintln(up.streams.Stdout, fmt.Sprintf(workspaceUnusedVolumeMessage, volume.name)); err != nil {
					return err
				}
			}
			continue
		}
		verb := "Adopted"
		if !volume.exists {
			if err := up.runPodman(ctx, "volume", "create", "--label", OwnerLabel+"="+up.group, volume.name); err != nil {
				return err
			}
			verb = "Created"
		}
		if _, err := fmt.Fprintf(up.streams.Stdout, "%s volume %s.\n", verb, volume.name); err != nil {
			return err
		}
	}

	return up.runPodman(ctx, up.createArguments(image)...)
}
