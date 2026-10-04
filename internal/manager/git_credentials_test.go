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

func TestGitCredentialsConfiguresPersistentStorageInsideTheHomeVolumeWithoutInput(t *testing.T) {
	var calls []process.Request
	run := func(_ context.Context, request process.Request) (int, error) {
		calls = append(calls, request)
		return 0, nil
	}
	var stdout, stderr bytes.Buffer
	status := manager.New("test", run).Run(context.Background(), []string{"integrations", "config", "git", "credentials"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	want := []string{"config", "--global", "--replace-all", "--", "credential.helper", "store --file=/home/agent/.git-credentials"}
	if len(calls) != 1 || calls[0].Name != "git" || !reflect.DeepEqual(calls[0].Args, want) || !reflect.DeepEqual(calls[0].Env, []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin"}) {
		t.Fatalf("calls=%+v", calls)
	}
}

func TestGitCredentialsRejectsIdentityOptionsWithoutStartingGit(t *testing.T) {
	for _, options := range [][]string{{"--name=N"}, {"--email=E"}, {"--name=N", "--email=E"}, {"extra"}, {"--unknown"}} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid credentials request started a process")
				return 0, nil
			}
			var stderr bytes.Buffer
			args := append([]string{"integrations", "config", "git", "credentials"}, options...)
			if status := manager.New("test", run).Run(context.Background(), args, process.Streams{Stderr: &stderr}); status != 1 || !strings.Contains(stderr.String(), "unexpected argument or option") {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

func TestGitCredentialsNormalizesConfigurationFailures(t *testing.T) {
	for _, failure := range []struct {
		status  int
		err     error
		message string
	}{{17, nil, "exit status 17"}, {0, errors.New("cannot launch git"), "cannot launch git"}} {
		t.Run(failure.message, func(t *testing.T) {
			calls := 0
			run := func(context.Context, process.Request) (int, error) {
				calls++
				return failure.status, failure.err
			}
			var stderr bytes.Buffer
			status := manager.New("test", run).Run(context.Background(), []string{"integrations", "config", "git", "credentials"}, process.Streams{Stderr: &stderr})
			if status != 1 || calls != 1 || !strings.Contains(stderr.String(), failure.message) {
				t.Fatalf("status=%d calls=%d stderr=%q", status, calls, stderr.String())
			}
		})
	}
}
