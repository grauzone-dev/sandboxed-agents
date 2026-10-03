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
	assetHash    string
	limits       ResourceLimits
	toolchains   toolchains.Set
	withProvided bool
}

func NewUp(name, group, assetHash string, limits ResourceLimits, selection toolchains.Set, withProvided bool, run process.Runner, streams process.Streams) *Up {
	return &Up{sandboxObjects: newSandboxObjects(name, group, run, streams), assetHash: assetHash, limits: limits, toolchains: selection, withProvided: withProvided}
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
	_, err := fmt.Fprintf(up.streams.Stdout, "Sandbox %s is running.\n", up.name)
	return err
}

func (up *Up) CheckOptions() error {
	if !up.containerExists {
		return nil
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

	args := []string{
		"create", "--name", up.container,
		"--label", OwnerLabel + "=" + up.group,
		"--label", NameLabel + "=" + up.name,
		"--label", WorkspaceKindLabel + "=volume",
		"--label", images.ToolchainsLabel + "=" + up.toolchains.String(),
		"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
	}
	args = append(args, up.limits.createArguments()...)
	for _, volume := range up.volumes {
		args = append(args, "--mount", "type=volume,source="+volume.name+",target="+volume.target)
	}
	args = append(args, image)
	if err := up.runPodman(ctx, args...); err != nil {
		return err
	}
	return nil
}
