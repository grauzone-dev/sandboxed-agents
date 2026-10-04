package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (objects *sandboxObjects) checkAgentEnabled(ctx context.Context, agent string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	status, err := objects.run(ctx, process.Request{Name: "podman", Args: []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", objects.container, manager.ExecutablePath, "agents", "check-enabled", agent}, Streams: process.Streams{Stdout: &output}})
	if err != nil || status != 0 || ctx.Err() != nil {
		return false, fmt.Errorf(managerUnavailableFormat, objects.name)
	}
	var enabled *bool
	if err := json.Unmarshal(output.Bytes(), &enabled); err != nil || enabled == nil {
		return false, fmt.Errorf(managerUnavailableFormat, objects.name)
	}
	return *enabled, nil
}
