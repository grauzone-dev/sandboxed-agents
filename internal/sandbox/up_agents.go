package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (up *Up) enableAgents(ctx context.Context) error {
	if len(up.agents) == 0 {
		return nil
	}
	if err := up.CheckManager(ctx); err != nil {
		return fmt.Errorf(upAgentsManagerUnavailableFormat, up.name, up.agentRetryCommands(up.agents))
	}
	var failed []string
	var reports, diagnostic bytes.Buffer
	for _, agent := range up.agents {
		status, err := up.run(ctx, process.Request{Name: "podman", Args: managerArgs(up.container, "agents", "enable", agent), Streams: process.Streams{Stdout: &reports, Stderr: &diagnostic}})
		if err != nil || status != 0 {
			if err := up.CheckManager(ctx); err != nil {
				return fmt.Errorf(upAgentsManagerUnavailableFormat, up.name, up.agentRetryCommands(up.agents))
			}
			failed = append(failed, agent)
		}
	}
	if _, err := io.Copy(up.streams.Stdout, &reports); err != nil {
		return err
	}
	if _, err := io.Copy(up.streams.Stderr, &diagnostic); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf(upAgentsFailedFormat, strings.Join(failed, ", "), up.agentRetryCommands(failed))
	}
	return nil
}

func (up *Up) agentRetryCommands(agents []string) string {
	commands := make([]string, 0, len(agents))
	for _, agent := range agents {
		commands = append(commands, "sandboxed-agents agents enable "+up.name+" "+agent)
	}
	return strings.Join(commands, "; ")
}
