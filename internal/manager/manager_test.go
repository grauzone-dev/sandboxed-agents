package manager_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestRegisteredCommandCanUseInjectedProcesses(t *testing.T) {
	var requests []process.Request
	var stdout, stderr bytes.Buffer
	runner := func(ctx context.Context, request process.Request) (int, error) {
		requests = append(requests, request)
		request.Streams.Stdout.Write([]byte("scripted output\n"))
		request.Streams.Stderr.Write([]byte("scripted error\n"))
		return 17, nil
	}
	app := manager.New("test-version", runner)
	app.Register("stand-in", func(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
		code, err := run(ctx, process.Request{Name: "stand-in-process", Args: args, Streams: streams})
		if err != nil {
			return err
		}
		if code != 0 {
			return errors.New("stand-in failed")
		}
		return nil
	})
	code := app.Run(context.Background(), []string{"stand-in", "arg with spaces", "--option"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if code == 0 {
		t.Fatal("command failure returned zero")
	}
	if stdout.String() != "scripted output\n" || !strings.Contains(stderr.String(), "scripted error\n") || !strings.Contains(stderr.String(), "stand-in failed") {
		t.Fatalf("streams = %q, %q", stdout.String(), stderr.String())
	}
	if len(requests) != 1 || requests[0].Name != "stand-in-process" || !reflect.DeepEqual(requests[0].Args, []string{"arg with spaces", "--option"}) {
		t.Fatalf("requests = %#v", requests)
	}
}
