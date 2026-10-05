package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAzureDevOpsLoginStoresTheTokenForCLIAndGitWithoutExposingIt(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	for _, workflow := range [][]string{nil, {"pat"}} {
		t.Run(strings.Join(workflow, ""), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var calls []process.Request
			var cliInput, gitInput string
			run := func(_ context.Context, request process.Request) (int, error) {
				calls = append(calls, request)
				if strings.Contains(strings.Join(request.Args, " "), token) || strings.Contains(strings.Join(request.Env, " "), token) {
					t.Fatal("token exposed in arguments or environment")
				}
				if request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || request.Dir != "/home/agent" {
					t.Fatalf("wrong storage identity: %+v", request)
				}
				if request.Streams.Stdin != nil {
					input, err := io.ReadAll(request.Streams.Stdin)
					if err != nil {
						t.Fatal(err)
					}
					if request.Name == "az" {
						cliInput = string(input)
					} else if len(request.Args) > 0 && request.Args[0] == "credential-store" {
						gitInput = string(input)
					}
				}
				io.WriteString(request.Streams.Stdout, token)
				io.WriteString(request.Streams.Stderr, token)
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			status := app.Run(context.Background(), append([]string{"integrations", "login", "azdo"}, workflow...), process.Streams{Stdin: strings.NewReader(token + "\n"), Stdout: &stdout, Stderr: &stderr})
			if status != 0 || stdout.Len() != 0 || stderr.Len() != 0 || cliInput != token+"\n" || gitInput != "protocol=https\nhost=dev.azure.com\nusername=\npassword="+token+"\n\n" {
				t.Fatalf("status=%d stdout=%q stderr=%q cli stored=%t git stored=%t", status, stdout.String(), stderr.String(), cliInput == token+"\n", gitInput != "")
			}
			if len(calls) == 0 || calls[0].Name != "az" || !reflect.DeepEqual(calls[0].Args, []string{"devops", "login"}) || !reflect.DeepEqual(calls[0].Env, []string{"HOME=/home/agent", "USER=agent", "LOGNAME=agent", "PATH=/usr/local/bin:/usr/bin:/bin", "AZURE_CONFIG_DIR=/home/agent/.azure", "AZURE_LOGGING_ENABLE_LOG_FILE=no", "AZURE_CORE_COLLECT_TELEMETRY=no", "PYTHON_KEYRING_BACKEND=keyring.backends.fail.Keyring"}) {
				t.Fatalf("calls=%+v", calls)
			}
		})
	}
}

func TestAzureDevOpsLoginRejectsEmptyOrInvalidInputBeforeStorage(t *testing.T) {
	for _, input := range []string{"", "\n", "\r\n", "with space\n", "nul\x00token\n", strings.Repeat("x", 65537)} {
		t.Run(fmt.Sprintf("length=%d", len(input)), func(t *testing.T) {
			run := func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid token started a storage process")
				return 0, nil
			}
			app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "azdo"}, process.Streams{Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr})
			if status != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
		})
	}
}

func TestAzureDevOpsLoginStopsOnStorageFailureAndKeepsDiagnosticsSecret(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	for failAt := 1; failAt <= 6; failAt++ {
		for _, startError := range []bool{false, true} {
			t.Run(fmt.Sprintf("step=%d/start=%t", failAt, startError), func(t *testing.T) {
				calls := 0
				run := func(_ context.Context, request process.Request) (int, error) {
					calls++
					io.WriteString(request.Streams.Stdout, token)
					io.WriteString(request.Streams.Stderr, token)
					if calls == failAt {
						if startError {
							return 0, errors.New(token)
						}
						return 17, nil
					}
					return 0, nil
				}
				app := manager.NewWithOptions("test", run, manager.Options{User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
				var stdout, stderr bytes.Buffer
				status := app.Run(context.Background(), []string{"integrations", "login", "azdo"}, process.Streams{Stdin: strings.NewReader(token), Stdout: &stdout, Stderr: &stderr})
				if status != 1 || calls != failAt || stdout.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), token) {
					t.Fatalf("status=%d calls=%d stdout=%q stderr=%q", status, calls, stdout.String(), stderr.String())
				}
			})
		}
	}
}

func TestAzureDevOpsLoginRefusesOtherIdentitiesBeforeReadingTheToken(t *testing.T) {
	for _, user := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 0}, {UID: 0, GID: 1000}} {
		t.Run(fmt.Sprint(user), func(t *testing.T) {
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("login started under another identity")
				return 0, nil
			}, manager.Options{User: func() process.Identity { return user }})
			var stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"integrations", "login", "azdo"}, process.Streams{Stdin: unreadToken{t}, Stderr: &stderr})
			if status != 1 || !strings.Contains(stderr.String(), "UID and GID 1000") {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

type unreadToken struct{ t *testing.T }

func (input unreadToken) Read([]byte) (int, error) {
	input.t.Fatal("token read before preconditions passed")
	return 0, io.EOF
}
