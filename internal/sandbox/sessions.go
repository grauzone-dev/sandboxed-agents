package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

var errInvalidSessionResponse = errors.New("invalid manager session response: expected a JSON array of objects with nonempty name and agent strings")

func RunningSessions(ctx context.Context, container string, run process.Runner) ([]manager.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output, diagnostic bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: []string{"exec", container, "/usr/local/bin/sandboxed-agents-manager", "sessions", "list"}, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("start manager session query: %w", err)
	}
	if status != 0 {
		return nil, fmt.Errorf("manager session query failed with exit status %d: %s", status, diagnostic.String())
	}
	var sessions []manager.Session
	if err := json.Unmarshal(output.Bytes(), &sessions); err != nil {
		return nil, fmt.Errorf("decode manager session response: %w", err)
	}
	if sessions == nil {
		return nil, errInvalidSessionResponse
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.Name) == "" || strings.TrimSpace(session.Agent) == "" {
			return nil, errInvalidSessionResponse
		}
	}
	return sessions, nil
}
