package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestGitHubLoginForwardsATerminalWithoutHostCredentials(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, workflow := range [][]string{nil, {"device"}} {
			for _, exitStatus := range []int{0, 17} {
				t.Run(fixture+"/workflow="+strings.Join(workflow, "")+"/status="+fmt.Sprint(exitStatus), func(t *testing.T) {
					terminal := shellTerminal(t)
					fakes := testutil.NewFakePrograms(t)
					t.Setenv("GH_TOKEN", "host-only-token")
					t.Setenv("GITHUB_TOKEN", "host-only-token")
					t.Setenv("GH_CONFIG_DIR", "/host-only/gh")
					t.Setenv("GIT_CONFIG_GLOBAL", "/host-only/git")
					owner := "default"
					responses := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
					responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{ExitCode: exitStatus})
					for i := range responses {
						responses[i].AbsentEnv = []string{"GIT_CONFIG_GLOBAL", "GH_TOKEN", "GITHUB_TOKEN", "GH_CONFIG_DIR"}
					}
					if fixture == "windows" {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					args := append([]string{"integrations", "login", "agent01", "github"}, workflow...)
					command := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
					command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE="+fixture)
					command.Stdin, command.Stdout = terminal, terminal
					var stderr bytes.Buffer
					command.Stderr = &stderr
					status := 0
					if err := command.Run(); err != nil {
						if exit, ok := err.(*exec.ExitError); ok {
							status = exit.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					if exitStatus == 0 && (status != 0 || stderr.Len() != 0) || exitStatus != 0 && (status != 1 || !strings.Contains(stderr.String(), "exit status 17")) {
						t.Fatalf("status=%d stderr=%q", status, stderr.String())
					}
					calls := fakes.Calls("podman")
					if len(calls) != len(responses) {
						t.Fatalf("calls=%v", calls)
					}
					if fixture == "windows" {
						calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
					}
					want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "-it", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "login", "github", "device"}
					if !reflect.DeepEqual(calls[len(calls)-1].Args, want) || !reflect.DeepEqual(calls[len(calls)-2].Args, sessionQueryArgs()) {
						t.Fatalf("calls=%v", calls)
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestGitHubLoginRequiresBothInputAndOutputToBeATerminal(t *testing.T) {
	for _, terminalInput := range []bool{false, true} {
		t.Run(fmt.Sprint(terminalInput), func(t *testing.T) {
			terminal := shellTerminal(t)
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"})
			fakes.Script("podman", responses...)
			command := exec.Command(os.Args[0], "-test.run=^TestCLIProcess$", "--", "integrations", "login", "agent01", "github")
			command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=sandbox-host")
			var stdout, stderr bytes.Buffer
			if terminalInput {
				command.Stdin, command.Stdout = terminal, &stdout
			} else {
				command.Stdin, command.Stdout = strings.NewReader("\n"), terminal
			}
			command.Stderr = &stderr
			err := command.Run()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), "login needs an interactive terminal") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("err=%v stderr=%q calls=%v", err, stderr.String(), fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}
