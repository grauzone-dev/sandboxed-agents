package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) loginGitHub(ctx context.Context, streams process.Streams, run process.Runner) error {
	if m.options.User() != agentIdentity {
		return errors.New(integrations.GitHubIdentity)
	}
	environment := []string{
		"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin",
		"GH_CONFIG_DIR=/home/agent/.config/gh", "GH_PROMPT_DISABLED=1",
	}
	for _, step := range []struct {
		name string
		args []string
	}{
		{"login", []string{"auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web"}},
		{"setup-git", []string{"auth", "setup-git", "--hostname", "github.com"}},
	} {
		status, err := run(ctx, process.Request{
			Name: "gh", Args: step.args, User: &agentIdentity, Dir: "/home/agent", Env: environment, Streams: streams,
		})
		if err != nil {
			return fmt.Errorf(integrations.GitHubStartFailure, step.name, err)
		}
		if status != 0 {
			return fmt.Errorf(integrations.GitHubFailure, step.name, status)
		}
	}
	return nil
}
