package sandbox

import (
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

type containerConfiguration struct {
	sandboxName     string
	controllerGroup string
	limits          ResourceLimits
	port            int
	toolchains      toolchains.Set
	workspaceSource string
}

func (configuration *containerConfiguration) createArguments(image string, extraLabels ...string) []string {
	container := containerPrefix + configuration.controllerGroup + "." + configuration.sandboxName
	kind := "volume"
	if configuration.workspaceSource != "" {
		kind = "bind"
	}
	args := []string{
		"create", "--name", container,
		"--label", OwnerLabel + "=" + configuration.controllerGroup,
		"--label", NameLabel + "=" + configuration.sandboxName,
		"--label", WorkspaceKindLabel + "=" + kind,
		"--label", images.ToolchainsLabel + "=" + configuration.toolchains.String(),
		"--label", fmt.Sprintf("%s=%d", SSHPortLabel, configuration.port),
		"--publish", fmt.Sprintf("127.0.0.1:%d:22", configuration.port),
		"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
	}
	args = append(args, configuration.limits.createArguments()...)
	for _, volume := range volumeDefinitions {
		if configuration.workspaceSource != "" && volume.target == "/workspace" {
			args = append(args, "--mount", configuration.workspaceMount())
			continue
		}
		args = append(args, "--mount", "type=volume,source="+container+"."+volume.suffix+",target="+volume.target)
	}
	for _, label := range extraLabels {
		args = append(args, "--label", label)
	}
	return append(args, image)
}
