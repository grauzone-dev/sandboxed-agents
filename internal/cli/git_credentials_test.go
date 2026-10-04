package cli_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestGitCredentialsWorksWithoutATerminalAndRunsAsAgent(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", "credentials")
	if status != 0 || stderr != "" || stdout != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "config", "git", "credentials"}
	if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) || !reflect.DeepEqual(calls[len(calls)-2].Args, sessionQueryArgs()) {
		t.Fatalf("calls=%v", calls)
	}
	assertNoSSH(t, fakes)
}

func TestGitCredentialsRejectsOptionsBeforePodman(t *testing.T) {
	for _, options := range [][]string{{"--name=N"}, {"--email=E"}, {"--name=N", "--email=E"}, {"extra"}, {"--unknown"}} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			args := append([]string{"integrations", "config", "agent01", "git", "credentials"}, options...)
			stdout, stderr, status := runCLI(t, "sandbox-host", args...)
			if status != 1 || stdout != "" || !strings.Contains(stderr, "unexpected argument or option") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestGitCredentialsDoesNotImportHostConfigurationCredentialsOrEnvironment(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			hostConfig := filepath.Join(t.TempDir(), "host.gitconfig")
			hostCredentials := filepath.Join(t.TempDir(), "host-credentials")
			for path, contents := range map[string]string{hostConfig: "[credential]\n helper = host-only\n", hostCredentials: "https://host-only:host-only@example.org\n"} {
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GIT_CONFIG_GLOBAL", hostConfig)
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
			t.Setenv("GIT_CONFIG_VALUE_0", "store --file="+hostCredentials)
			t.Setenv("GIT_ASKPASS", "host-only")
			t.Setenv("GH_TOKEN", "host-only")
			t.Setenv("AZURE_DEVOPS_EXT_PAT", "host-only")
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			t.Setenv("CONTAINER_CONNECTION", "foreign")
			t.Setenv("CONTAINER_HOST", "ssh://foreign")
			t.Setenv("CONTAINER_SSHKEY", "foreign-key")
			owner := "team-a"
			responses := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
			for i := range responses {
				responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, ".default.", ".team-a.")
			}
			responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
			for i := range responses {
				responses[i].AbsentEnv = []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_ASKPASS"}
				if fixture == "windows" {
					responses[i].AbsentEnv = append(responses[i].AbsentEnv, "CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY")
				}
			}
			if fixture == "windows" {
				selection := healthyWindowsPodman()[1:3]
				for i := range selection {
					selection[i].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
				}
				responses = append(selection, responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, "integrations", "config", "agent01", "git", "credentials")
			calls := fakes.Calls("podman")
			if status != 0 || stdout != "" || stderr != "" || len(calls) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
			if fixture == "windows" {
				calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
			}
			for _, call := range calls {
				for _, arg := range call.Args {
					if strings.Contains(arg, "host-only") || strings.Contains(arg, hostConfig) || strings.Contains(arg, hostCredentials) || strings.Contains(arg, ".default.") || strings.Contains(arg, "--mount") || arg == "-v" || arg == "--env-host" {
						t.Fatalf("host data or mount forwarded: %v", call)
					}
				}
			}
			want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "config", "git", "credentials"}
			if !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("workflow call=%v", calls[len(calls)-1])
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestGitCredentialsReportsWorkflowFailureWithoutPassingThroughItsStatus(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{ExitCode: 17, Stderr: "git refused\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", "credentials")
	if status != 1 || stdout != "" || !strings.Contains(stderr, "git refused") || !strings.Contains(stderr, "exit status 17") || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertNoSSH(t, fakes)
}
