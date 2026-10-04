package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type windowsPodman struct {
	connection    string
	automountRoot string
	runner        process.Runner
}

func (podman *windowsPodman) invoke(ctx context.Context, request process.Request) (int, error) {
	if request.Name == "podman" {
		environment := request.Env
		if environment == nil {
			environment = os.Environ()
		}
		request.Env = make([]string, 0, len(environment))
		for _, value := range environment {
			key, _, _ := strings.Cut(value, "=")
			switch strings.ToUpper(key) {
			case "CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY":
			default:
				request.Env = append(request.Env, value)
			}
		}
	}
	return podman.runner(ctx, request)
}

func (podman *windowsPodman) run(ctx context.Context, request process.Request) (int, error) {
	if request.Name != "podman" {
		return podman.runner(ctx, request)
	}
	if podman.connection == "" {
		selectionContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		connection, err := preflight.SelectWindowsConnection(selectionContext, podman.invoke)
		if err != nil {
			return 1, fmt.Errorf("%w; %s", err, windowsTargetHint)
		}
		podman.connection = connection
	}
	request.Args = append([]string{"--connection", podman.connection}, request.Args...)
	return podman.invoke(ctx, request)
}
