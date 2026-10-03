package sandbox

import (
	"context"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Up struct {
	*objects
	assetHash string
}

func NewUp(name, assetHash string, run process.Runner, streams process.Streams) *Up {
	return &Up{objects: newObjects(name, run, streams), assetHash: assetHash}
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

func (up *Up) createSandbox(ctx context.Context) error {
	image := images.BaseTag(up.assetHash)
	exists, err := up.objectExists(ctx, "image", image)
	if err != nil {
		return err
	}
	if !exists {
		if err := images.BuildBase(ctx, up.assetHash, up.run, up.streams); err != nil {
			return err
		}
	}
	for _, volume := range up.volumes {
		verb := "Adopted"
		if !volume.exists {
			if err := up.runPodman(ctx, "volume", "create", "--label", OwnerLabel+"="+defaultGroup, volume.name); err != nil {
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
		"--label", OwnerLabel + "=" + defaultGroup,
		"--label", NameLabel + "=" + up.name,
		"--label", WorkspaceKindLabel + "=volume",
		"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
		"--memory=8g", "--cpus=4", "--pids-limit=2048", "--shm-size=1g",
	}
	for _, volume := range up.volumes {
		args = append(args, "--mount", "type=volume,source="+volume.name+",target="+volume.target)
	}
	args = append(args, image)
	if err := up.runPodman(ctx, args...); err != nil {
		return err
	}
	return nil
}
