package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func (m *Manager) loginAzureDevOps(ctx context.Context, streams process.Streams, run process.Runner) error {
	if m.options.User() != agentIdentity {
		return errors.New(integrations.AzdoIdentity)
	}
	token, err := integrations.ReadToken(streams)
	if err != nil {
		return err
	}
	helper := `!f() { if test "$1" = get; then sed -e '/^username=/d' -e '/^path=/d' -e 's/^host=.*/host=dev.azure.com/' | git credential-store --file=/home/agent/.git-credentials-azdo get; fi; }; f`
	type step struct {
		phase string
		name  string
		args  []string
		input string
	}
	steps := []step{
		{"login", "az", []string{"devops", "login"}, token + "\n"},
		{"credential-store", "git", []string{"credential-store", "--file=/home/agent/.git-credentials-azdo", "store"}, "protocol=https\nhost=dev.azure.com\nusername=\npassword=" + token + "\n\n"},
	}
	for _, scope := range []string{"https://dev.azure.com", "https://*.visualstudio.com"} {
		key := "credential." + scope + ".helper"
		steps = append(steps,
			step{"config", "git", []string{"config", "--global", "--replace-all", "--", key, ""}, ""},
			step{"config", "git", []string{"config", "--global", "--add", "--", key, helper}, ""},
		)
	}
	for _, step := range steps {
		var input io.Reader
		if step.input != "" {
			input = strings.NewReader(step.input)
		}
		status, err := run(ctx, process.Request{
			Name: step.name, Args: step.args, User: &agentIdentity, Dir: "/home/agent",
			Env: []string{
				"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin",
				"AZURE_CONFIG_DIR=/home/agent/.azure", "AZURE_LOGGING_ENABLE_LOG_FILE=no", "AZURE_CORE_COLLECT_TELEMETRY=no",
				"PYTHON_KEYRING_BACKEND=keyring.backends.fail.Keyring",
			},
			Streams: process.Streams{Stdin: input, Stdout: io.Discard, Stderr: io.Discard},
		})
		if err != nil {
			return fmt.Errorf(integrations.AzdoStartFailure, step.phase)
		}
		if status != 0 {
			return fmt.Errorf(integrations.AzdoFailure, step.phase, status)
		}
	}
	return nil
}
