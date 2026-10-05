package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAzureDevOpsLoginPipesTheTokenToTheManagerWithoutArgumentsOrOutput(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, workflow := range [][]string{nil, {"pat"}} {
			t.Run(fixture+"/"+strings.Join(workflow, ""), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure")
				responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{WantStdin: token + "\n", Stdout: token, Stderr: token})
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3], responses...)
				}
				fakes.Script("podman", responses...)
				args := append([]string{"integrations", "login", "agent01", "azdo"}, workflow...)
				stdout, stderr, status := runCLIWithInput(t, "", fixture, strings.NewReader(token+"\n"), args...)
				calls := fakes.Calls("podman")
				if status != 0 || stdout != "" || stderr != "" || len(calls) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
				}
				if fixture == "windows" {
					calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
				}
				want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-i", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "login", "azdo", "pat"}
				if !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatalf("call=%v want=%v", calls[len(calls)-1].Args, want)
				}
				for _, call := range calls {
					if strings.Contains(strings.Join(call.Args, " "), token) {
						t.Fatal("token leaked into Podman arguments")
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestAzureDevOpsLoginRequiresInputAndNormalizesStorageFailures(t *testing.T) {
	for _, test := range []struct {
		name, input string
		response    *testutil.Response
		message     string
	}{
		{"empty", "", nil, "no Azure DevOps personal access token"},
		{"empty line", "\n", nil, "no Azure DevOps personal access token"},
		{"invalid", "two words\n", nil, "invalid Azure DevOps personal access token"},
		{"storage refused", "offline-pat-49-sensitive", &testutil.Response{ExitCode: 17, Stdout: "offline-pat-49-sensitive", Stderr: "offline-pat-49-sensitive"}, "exit status 17"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure")
			responses = append(responses, testutil.Response{Stdout: "[]"})
			if test.response != nil {
				responses = append(responses, *test.response)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLIWithInput(t, "", "sandbox-host", strings.NewReader(test.input), "integrations", "login", "agent01", "azdo")
			if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || strings.Contains(stderr, "offline-pat-49-sensitive") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAzureDevOpsLoginRejectsTokenArgumentsWithoutPrintingThem(t *testing.T) {
	const token = "offline-pat-49-sensitive"
	for _, args := range [][]string{{token}, {"pat", token}, {"--token=" + token}, {"pat", "--token", token}} {
		t.Run(fmt.Sprint(len(args), strings.HasPrefix(args[0], "--")), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "sandbox-host", append([]string{"integrations", "login", "agent01", "azdo"}, args...)...)
			if status != 1 || stdout != "" || !strings.Contains(stderr, "Usage:") || strings.Contains(stderr, token) || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAzureDevOpsLoginChecksManagerAndToolchainBeforeReadingInput(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name, recorded, update string
			manager                testutil.Response
			message                string
		}{
			{"manager first", "dotnet", "", testutil.Response{ExitCode: 125}, "does not answer"},
			{"malformed manager", "azure", "", testutil.Response{Stdout: "not JSON"}, "does not answer"},
			{"base", "", "azure", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"none", "none", "azure", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"dotnet", "dotnet", "azure,dotnet", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"all", "native,dotnet", "azure,dotnet,native", testutil.Response{Stdout: "[]"}, "azure toolchain"},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), test.recorded)
				responses = append(responses, test.manager)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3], responses...)
				}
				fakes.Script("podman", responses...)
				input, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				defer writer.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "login", "agent01", "azdo")
				command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE="+fixture)
				var stdout, stderr bytes.Buffer
				command.Stdin, command.Stdout, command.Stderr = input, &stdout, &stderr
				err = command.Run()
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 1 || ctx.Err() != nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.message) || len(fakes.Calls("podman")) != len(responses) {
					t.Fatalf("err=%v timeout=%v stdout=%q stderr=%q calls=%v", err, ctx.Err(), stdout.String(), stderr.String(), fakes.Calls("podman"))
				}
				if test.update != "" && (!strings.Contains(stderr.String(), "sandboxed-agents update agent01 --with "+test.update) || strings.Contains(stderr.String(), "none")) {
					t.Fatalf("stderr=%q", stderr.String())
				}
				if test.update == "" && (!strings.Contains(stderr.String(), "check agent01") || !strings.Contains(stderr.String(), "restart agent01") || strings.Contains(stderr.String(), "toolchain")) {
					t.Fatalf("stderr=%q", stderr.String())
				}
			})
		}
	}
}
