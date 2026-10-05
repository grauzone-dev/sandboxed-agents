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

func TestAzureLoginAuthenticatesAsAgentWithADeviceCodeInTheHomeVolume(t *testing.T) {
	for _, workflow := range [][]string{nil, {"device"}} {
		t.Run(strings.Join(workflow, ""), func(t *testing.T) {
			t.Setenv("AZURE_CONFIG_DIR", "/host-only/azure")
			t.Setenv("AZURE_CLIENT_SECRET", "host-only-secret")
			t.Setenv("ARM_CLIENT_SECRET", "host-only-secret")
			stdin := strings.NewReader("\n")
			var stdout, stderr bytes.Buffer
			var calls []process.Request
			run := func(ctx context.Context, request process.Request) (int, error) {
				if _, deadline := ctx.Deadline(); deadline {
					t.Fatal("device login has an added deadline")
				}
				calls = append(calls, request)
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			status := app.Run(context.Background(), append([]string{"integrations", "login", "azure"}, workflow...), process.Streams{Stdin: stdin, Stdout: &stdout, Stderr: &stderr})
			if status != 0 || stderr.Len() != 0 || len(calls) != 1 {
				t.Fatalf("status=%d stderr=%q calls=%+v", status, stderr.String(), calls)
			}
			call := calls[0]
			wantEnvironment := []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin", "AZURE_CONFIG_DIR=/home/agent/.azure"}
			if call.Name != "az" || !reflect.DeepEqual(call.Args, []string{"login", "--use-device-code"}) || call.User == nil || *call.User != (process.Identity{UID: 1000, GID: 1000}) || call.Dir != "/home/agent" || !reflect.DeepEqual(call.Env, wantEnvironment) || call.Streams.Stdin != stdin || call.Streams.Stdout != &stdout || call.Streams.Stderr != &stderr {
				t.Fatalf("call=%+v", call)
			}
		})
	}
}

func TestAzureLoginNormalizesFailureAndPreservesCLIOutput(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		err     error
		message string
	}{
		{"refused", 17, nil, "login failed with exit status 17"},
		{"canceled", 130, nil, "login failed with exit status 130"},
		{"cannot start", 0, errors.New("cannot launch az"), "could not start Azure CLI login"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			run := func(_ context.Context, request process.Request) (int, error) {
				calls++
				fmt.Fprint(request.Streams.Stdout, "Azure output\n")
				fmt.Fprint(request.Streams.Stderr, "Azure error\n")
				return test.status, test.err
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "azure"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if status != 1 || calls != 1 || stdout.String() != "Azure output\n" || !strings.Contains(stderr.String(), "Azure error\n") || !strings.Contains(stderr.String(), test.message) {
				t.Fatalf("status=%d calls=%d stdout=%q stderr=%q", status, calls, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAzureLoginRefusesOtherIdentitiesBeforeStartingAzureCLI(t *testing.T) {
	for _, user := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 0}, {UID: 0, GID: 1000}} {
		t.Run(fmt.Sprint(user), func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("login started under another identity")
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return user }})
			var stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "azure"}, process.Streams{Stderr: &stderr})
			if status != 1 || !strings.Contains(stderr.String(), "UID and GID 1000") {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}
