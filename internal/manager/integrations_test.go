package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestGitIdentitySetsTheAgentGlobalNameAndEmailWithoutPrompting(t *testing.T) {
	var calls []process.Request
	run := func(_ context.Context, request process.Request) (int, error) {
		calls = append(calls, request)
		return 0, nil
	}
	var stdout, stderr bytes.Buffer
	status := manager.New("test", run).Run(context.Background(), []string{"integrations", "config", "git", "identity", "--name", "Chosen Name", "--email", "chosen@example.org"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	want := [][]string{
		{"config", "--global", "--replace-all", "--", "user.name", "Chosen Name"},
		{"config", "--global", "--replace-all", "--", "user.email", "chosen@example.org"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls=%v", calls)
	}
	for i, call := range calls {
		if call.Name != "git" || !reflect.DeepEqual(call.Args, want[i]) || !reflect.DeepEqual(call.Env, []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin"}) {
			t.Fatalf("call=%+v want args=%v", call, want[i])
		}
	}
}

func TestGitIdentityRejectsIncompleteInputBeforeAnyWrite(t *testing.T) {
	for _, input := range []string{"", "\nemail@example.org\n", "Typed Name\n", "Typed Name\nunterminated", "Name\x00\nemail@example.org\n", "Name\nemail\x00\n"} {
		t.Run(input, func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("incomplete prompt input wrote configuration")
				return 0, nil
			}
			var stderr bytes.Buffer
			status := manager.New("test", run).Run(context.Background(), []string{"integrations", "config", "git", "identity"}, process.Streams{Stdin: strings.NewReader(input), Stderr: &stderr})
			if status != 1 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

func TestGitIdentityNormalizesProcessFailuresAndStopsWriting(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		for _, failure := range []struct {
			status int
			err    error
		}{
			{17, nil}, {1, errors.New("cannot launch git")},
		} {
			t.Run(fmt.Sprintf("%d/%d/%v", failAt, failure.status, failure.err), func(t *testing.T) {
				calls := 0
				run := func(context.Context, process.Request) (int, error) {
					calls++
					if calls == failAt {
						return failure.status, failure.err
					}
					return 0, nil
				}
				var stderr bytes.Buffer
				status := manager.New("test", run).Run(context.Background(), []string{"integrations", "config", "git", "identity", "--name=N", "--email=E"}, process.Streams{Stderr: &stderr})
				if status != 1 || calls != failAt || stderr.Len() == 0 {
					t.Fatalf("status=%d calls=%d stderr=%q", status, calls, stderr.String())
				}
			})
		}
	}
}

func TestManagerRejectsUndeliveredIntegrationWorkflowsWithoutProcesses(t *testing.T) {
	for _, args := range [][]string{
		{"config", "git", "identity", "--name=", "--email=E"},
		{}, {"config"}, {"login", "github", "nosuch"}, {"login", "azure"}, {"login", "azdo"}, {"login", "git"}, {"config", "github"}, {"config", "git"}, {"config", "git", "identity", "--unknown"}, {"config", "git", "identity", "--name=N\x00", "--email=E"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid request started process")
				return 0, nil
			}
			var stderr bytes.Buffer
			status := manager.New("test", run).Run(context.Background(), append([]string{"integrations"}, args...), process.Streams{Stderr: &stderr})
			if status != 1 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

func TestGitIdentityPromptsOnlyForMissingValuesBeforeChangingConfiguration(t *testing.T) {
	for _, test := range []struct {
		options []string
		input   string
		prompts string
	}{
		{nil, "Typed Name\ntyped@example.org\n", "Git commit name: Git commit email: "},
		{[]string{"--name", "Typed Name"}, "typed@example.org\n", "Git commit email: "},
		{[]string{"--email", "typed@example.org"}, "Typed Name\r\n", "Git commit name: "},
	} {
		t.Run(strings.Join(test.options, " "), func(t *testing.T) {
			var values []string
			run := func(_ context.Context, request process.Request) (int, error) {
				values = append(values, request.Args[len(request.Args)-1])
				return 0, nil
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"integrations", "config", "git", "identity"}, test.options...)
			status := manager.New("test", run).Run(context.Background(), args, process.Streams{Stdin: strings.NewReader(test.input), Stdout: &stdout, Stderr: &stderr})
			if status != 0 || stdout.Len() != 0 || stderr.String() != test.prompts || !reflect.DeepEqual(values, []string{"Typed Name", "typed@example.org"}) {
				t.Fatalf("status=%d stdout=%q stderr=%q values=%v", status, stdout.String(), stderr.String(), values)
			}
		})
	}
}
