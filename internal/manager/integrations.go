package manager

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/integrations"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func configureIntegration(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	if len(args) == 0 {
		return fmt.Errorf(integrations.UnknownKind, "")
	}
	request, err := integrations.Parse(args[0], args[1:])
	if err != nil {
		return err
	}
	if request.NeedsTerminal() {
		if streams.Stdin == nil {
			return fmt.Errorf("%s", integrations.NeedsTerminal)
		}
		reader := bufio.NewReader(streams.Stdin)
		for _, field := range []struct {
			option string
			prompt string
			target **string
		}{
			{"--name", integrations.NamePrompt, &request.Name},
			{"--email", integrations.EmailPrompt, &request.Email},
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
		{"user.name", *request.Name},
		{"user.email", *request.Email},
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
