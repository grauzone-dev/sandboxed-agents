package manager

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func runIntegrationWorkflow(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) == 0 {
		return fmt.Errorf(integrations.UnknownKind, "")
	}
	request, err := integrations.Parse(args[0], args[1:])
	if err != nil {
		return err
	}
	return setGitIdentity(ctx, request, streams, run)
}

func setGitIdentity(ctx context.Context, request integrations.Request, streams process.Streams, run process.Runner) error {
	if request.NeedsTerminal() {
		if streams.Stdin == nil {
			return errors.New(integrations.NeedsTerminal)
		}
		reader := bufio.NewReader(streams.Stdin)
		for _, field := range []struct {
			option string
			prompt string
			target **string
		}{
			{"--name", integrations.NamePrompt, &request.CommitName},
			{"--email", integrations.EmailPrompt, &request.CommitEmail},
		} {
			if *field.target != nil {
				continue
			}
			if _, err := io.WriteString(streams.Stderr, field.prompt); err != nil {
				return err
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf(integrations.PromptFailure, field.option, err)
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if strings.ContainsRune(line, 0) {
				return fmt.Errorf(integrations.InvalidOptionValue, field.option)
			}
			*field.target = &line
		}
	}
	for _, value := range []struct{ key, value string }{
		{"user.name", *request.CommitName},
		{"user.email", *request.CommitEmail},
	} {
		status, err := run(ctx, process.Request{
			Name: "git", Args: []string{"config", "--global", "--replace-all", "--", value.key, value.value},
			Env:     []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin"},
			Streams: streams,
		})
		if err != nil {
			return fmt.Errorf(integrations.GitStartFailure, err)
		}
		if status != 0 {
			return fmt.Errorf(integrations.GitFailure, status)
		}
	}
	return nil
}
