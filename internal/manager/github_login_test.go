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

func TestGitHubLoginAuthenticatesAsAgentThenConfiguresGitForGitHub(t *testing.T) {
	for _, workflow := range [][]string{nil, {"device"}} {
		t.Run(strings.Join(workflow, ""), func(t *testing.T) {
			t.Setenv("GH_TOKEN", "ambient-token")
			t.Setenv("GH_CONFIG_DIR", "/ambient/gh")
			t.Setenv("GIT_CONFIG_GLOBAL", "/ambient/git")
			stdin := strings.NewReader("\n")
			var stdout, stderr bytes.Buffer
			var calls []process.Request
			run := func(_ context.Context, request process.Request) (int, error) {
				calls = append(calls, request)
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			args := append([]string{"integrations", "login", "github"}, workflow...)
			status := app.Run(context.Background(), args, process.Streams{Stdin: stdin, Stdout: &stdout, Stderr: &stderr})
			if status != 0 || stderr.Len() != 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
			want := [][]string{
				{"auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web"},
				{"auth", "setup-git", "--hostname", "github.com"},
			}
			if len(calls) != len(want) {
				t.Fatalf("calls=%+v", calls)
			}
			environment := []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin", "GH_CONFIG_DIR=/home/agent/.config/gh", "GH_PROMPT_DISABLED=1"}
			for i, call := range calls {
				if call.Name != "gh" || !reflect.DeepEqual(call.Args, want[i]) || call.User == nil || *call.User != (process.Identity{UID: 1000, GID: 1000}) || call.Dir != "/home/agent" || !reflect.DeepEqual(call.Env, environment) || call.Streams.Stdin != stdin || call.Streams.Stdout != &stdout || call.Streams.Stderr != &stderr {
					t.Fatalf("call=%+v", call)
				}
			}
		})
	}
}

func TestGitHubLoginStopsAtTheFailedStepAndNormalizesItsStatus(t *testing.T) {
	for _, failure := range []struct {
		name    string
		step    int
		status  int
		err     error
		message string
	}{
		{"login refused", 0, 17, nil, "login failed with exit status 17"},
		{"login canceled", 0, 130, nil, "login failed with exit status 130"},
		{"login cannot start", 0, 0, errors.New("cannot launch gh"), "could not start GitHub CLI login"},
		{"Git setup refused", 1, 23, nil, "setup-git failed with exit status 23"},
		{"Git setup cannot start", 1, 0, errors.New("cannot launch setup"), "could not start GitHub CLI setup-git"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			calls := 0
			run := func(_ context.Context, request process.Request) (int, error) {
				step := calls
				calls++
				if step == failure.step {
					return failure.status, failure.err
				}
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "github"}, process.Streams{Stderr: &stderr})
			if status != 1 || calls != failure.step+1 || !strings.Contains(stderr.String(), failure.message) {
				t.Fatalf("status=%d calls=%d stderr=%q", status, calls, stderr.String())
			}
		})
	}
}

func TestGitHubLoginRefusesOtherIdentitiesBeforeStartingGitHubCLI(t *testing.T) {
	for _, user := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 0}, {UID: 0, GID: 1000}} {
		t.Run(fmt.Sprint(user), func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("login started under another identity")
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return user }})
			var stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "github"}, process.Streams{Stderr: &stderr})
			if status != 1 || !strings.Contains(stderr.String(), "UID and GID 1000") {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}
