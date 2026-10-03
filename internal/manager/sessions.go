package manager

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type Session struct {
	Name  string `json:"name"`
	Agent string `json:"agent"`
}

func listSessions(_ context.Context, args []string, streams process.Streams, _ process.Runner) error {
	if len(args) != 1 || args[0] != "list" {
		return errors.New("sandboxed-agents-manager sessions list")
	}
	return json.NewEncoder(streams.Stdout).Encode([]Session{})
}
