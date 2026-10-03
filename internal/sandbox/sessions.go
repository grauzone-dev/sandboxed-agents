package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func querySessions(ctx context.Context, container string, run process.Runner) ([]manager.Session, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var output, diagnostic bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: []string{"exec", container, "/usr/local/bin/sandboxed-agents-manager", "sessions", "list"}, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err != nil || status != 0 || ctx.Err() != nil {
		return nil, false
	}
	var sessions []manager.Session
	if err := json.Unmarshal(output.Bytes(), &sessions); err != nil || sessions == nil {
		return nil, false
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.Name) == "" || strings.TrimSpace(session.Agent) == "" {
			return nil, false
		}
	}
	return sessions, true
}
