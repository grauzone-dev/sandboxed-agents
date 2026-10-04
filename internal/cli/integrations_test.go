package cli_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestGitIdentityWorksWithoutATerminalAndRunsAsAgent(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", "identity", "--name", "Chosen Name", "--email", "chosen@example.org")
	if status != 0 || stderr != "" || stdout != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	want := [][]string{
		sessionQueryArgs(),
		{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "config", "git", "identity", "--name=Chosen Name", "--email=chosen@example.org"},
	}
	if len(calls) != len(responses) {
		t.Fatalf("calls=%v", calls)
	}
	for i, args := range want {
		if !reflect.DeepEqual(calls[len(calls)-2+i].Args, args) {
			t.Fatalf("call=%v want=%v", calls[len(calls)-2+i].Args, args)
		}
	}
	assertNoSSH(t, fakes)
}

func TestIntegrationNamesAndUsageFailBeforePodman(t *testing.T) {
	for _, test := range []struct {
		args    []string
		message string
	}{
		{[]string{"config", "unknown", "nosuch"}, "valid integrations: git"},
		{[]string{"config", "unknown", "git", "nosuch"}, "valid workflows: identity, credentials"},
		{[]string{"config", "agent01", "github"}, "valid integrations: git"},
		{[]string{"config", "agent01", "azure"}, "valid integrations: git"},
		{[]string{"config", "agent01", "azdo"}, "valid integrations: git"},
		{[]string{"config", "agent01", "git"}, "valid workflows: identity, credentials"},
		{[]string{"login", "agent01", "github"}, "no valid names are available yet"},
		{[]string{"login", "agent01", "azure"}, "no valid names are available yet"},
		{[]string{"login", "agent01", "azdo"}, "no valid names are available yet"},
		{[]string{"login", "agent01", "git"}, "valid config workflows: identity, credentials"},
		{[]string{"config"}, "sandbox name"},
		{[]string{"config", "agent01"}, "missing integration"},
		{[]string{"config", "a/b", "git"}, "invalid sandbox name"},
		{[]string{"config", "agent01", "git", "identity", "--unknown"}, "unexpected argument or option"},
		{[]string{"config", "agent01", "git", "identity", "--name"}, "missing value for --name"},
		{[]string{"config", "agent01", "git", "identity", "--name=", "--email=E"}, "missing value for --name"},
		{[]string{"config", "agent01", "git", "identity", "--name", "", "--email=E"}, "missing value for --name"},
		{[]string{"config", "agent01", "git", "identity", "--email", "--name", "N"}, "missing value for --email"},
		{[]string{"config", "agent01", "git", "identity", "--name", "N", "--name", "M"}, "duplicate option"},
		{[]string{"config", "agent01", "git", "identity", "extra"}, "unexpected argument or option"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "sandbox-host", append([]string{"integrations"}, test.args...)...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			if strings.Contains(test.message, "no valid names") && (strings.Contains(stderr, `"git"`) || strings.Contains(stderr, "identity") || strings.Contains(stderr, "valid integrations:")) {
				t.Fatalf("undelivered login names leaked: %q", stderr)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestIntegrationChecksReportTheFirstFailureAndNeverStartAnything(t *testing.T) {
	owner, foreign, missing := "default", "other", ""
	for _, workflow := range []string{"identity", "credentials"} {
		for _, test := range []struct {
			name      string
			container *string
			running   bool
			volumes   map[string]string
			backup    *string
			manager   *testutil.Response
			message   string
		}{
			{"unknown", nil, false, nil, nil, nil, "does not exist"},
			{"volumes only", nil, false, map[string]string{"home": owner}, nil, nil, "up agent01"},
			{"foreign kept volume", nil, false, map[string]string{"home": foreign}, nil, nil, "owner conflict"},
			{"foreign container", &foreign, false, nil, &owner, nil, "owner conflict"},
			{"unlabelled container", &missing, false, nil, nil, nil, "owner conflict"},
			{"foreign workspace", &owner, false, map[string]string{"workspace": foreign}, &owner, nil, "owner conflict"},
			{"foreign home", &owner, false, map[string]string{"home": foreign}, nil, nil, "owner conflict"},
			{"unlabelled home", &owner, false, map[string]string{"home": missing}, nil, nil, "owner conflict"},
			{"foreign ssh", &owner, false, map[string]string{"ssh": foreign}, nil, nil, "owner conflict"},
			{"foreign backup", &owner, false, nil, &foreign, nil, "owner conflict"},
			{"update", &owner, false, nil, &owner, nil, "update agent01"},
			{"backup only", nil, false, nil, &owner, nil, "update agent01"},
			{"stopped", &owner, false, nil, nil, nil, "start agent01"},
			{"manager failed", &owner, true, nil, nil, &testutil.Response{ExitCode: 125}, "does not answer"},
			{"manager malformed", &owner, true, nil, nil, &testutil.Response{Stdout: "not JSON"}, "does not answer"},
			{"manager null", &owner, true, nil, nil, &testutil.Response{Stdout: "null"}, "does not answer"},
			{"terminal", &owner, true, nil, nil, &testutil.Response{Stdout: "[]"}, "terminal"},
		} {
			if workflow == "credentials" && test.name == "terminal" {
				continue
			}
			t.Run(workflow+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
				if test.manager != nil {
					responses = append(responses, *test.manager)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", workflow)
				calls := fakes.Calls("podman")
				if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) || len(calls) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
				}
				if test.manager != nil {
					if !reflect.DeepEqual(calls[len(calls)-1].Args, sessionQueryArgs()) {
						t.Fatalf("unexpected manager query: %v", calls)
					}
					if test.message == "does not answer" && (!strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || strings.Contains(stderr, "terminal")) {
						t.Fatalf("stderr=%q", stderr)
					}
				} else {
					assertLifecycleReadOnly(t, fakes)
				}
				if test.message == "owner conflict" && (!strings.Contains(stderr, "Podman") || !strings.Contains(stderr, "sandboxed-agents.") || strings.Contains(stderr, "terminal") || strings.Contains(stderr, "interrupted update")) {
					t.Fatalf("stderr=%q", stderr)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestGitIdentityDoesNotImportHostGitConfigurationOrEnvironment(t *testing.T) {
	for _, polluted := range []bool{false, true} {
		t.Run(fmt.Sprint(polluted), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			variables := []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_DIR", "GIT_WORK_TREE", "EMAIL"}
			if polluted {
				for _, variable := range variables {
					t.Setenv(variable, "host-only")
				}
				t.Setenv("GIT_CONFIG_COUNT", "1")
				t.Setenv("GIT_CONFIG_KEY_0", "user.name")
				path := t.TempDir() + "/host.gitconfig"
				if err := os.WriteFile(path, []byte("[user]\n name = Host Name\n email = host@example.org\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GIT_CONFIG_GLOBAL", path)
			}
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
			for i := range responses {
				responses[i].AbsentEnv = variables
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", "identity", "--name", "Explicit Name", "--email", "explicit@example.org")
			if status != 0 || stderr != "" || stdout != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				if strings.Contains(strings.Join(call.Args, " "), "host-only") || strings.Contains(strings.Join(call.Args, " "), "Host Name") {
					t.Fatalf("host configuration leaked: %v", call)
				}
			}
		})
	}
}

func TestIntegrationUsesTheSelectedWindowsTargetAndControllerGroup(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	t.Setenv("CONTAINER_CONNECTION", "foreign")
	t.Setenv("CONTAINER_HOST", "ssh://foreign")
	t.Setenv("CONTAINER_SSHKEY", "foreign-key")
	owner := "team-a"
	responses := append([]testutil.Response{}, healthyWindowsPodman()[1:3]...)
	objects := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
	for i := range objects {
		objects[i].Stdout = strings.ReplaceAll(objects[i].Stdout, ".default.", ".team-a.")
	}
	responses = append(responses, objects...)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
	for i := range responses {
		responses[i].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "integrations", "config", "agent01", "git", "identity", "--name=N", "--email=E")
	if status != 0 || stdout != "" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
	calls := fakes.Calls("podman")
	if len(calls) != len(responses) {
		t.Fatalf("calls=%v", calls)
	}
	operations := windowsOperationCalls(t, calls[2:], "podman-machine-default")
	for _, call := range operations {
		if strings.Contains(strings.Join(call.Args, " "), ".default.") {
			t.Fatalf("wrong group: %v", call)
		}
	}
	want := []string{"exec", "--user=1000:1000", "--env", "HOME=/home/agent", "sandboxed-agents.team-a.agent01", "/usr/local/bin/sandboxed-agents-manager", "integrations", "config", "git", "identity", "--name=N", "--email=E"}
	if !reflect.DeepEqual(operations[len(operations)-1].Args, want) {
		t.Fatalf("workflow call=%v", operations[len(operations)-1])
	}
	assertNoSSH(t, fakes)
}

func TestGitIdentityRequiresATerminalForEachMissingOption(t *testing.T) {
	for _, options := range [][]string{nil, {"--name=N"}, {"--email=E"}} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := sandboxObjectResponses(&owner, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "[]"})
			fakes.Script("podman", responses...)
			args := append([]string{"integrations", "config", "agent01", "git", "identity"}, options...)
			stdout, stderr, status := runCLI(t, "sandbox-host", args...)
			if status != 1 || stdout != "" || !strings.Contains(stderr, "terminal") || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestIntegrationWorkflowFailureReturnsOneWithoutFurtherPodmanCalls(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := sandboxObjectResponses(&owner, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{ExitCode: 17, Stderr: "git refused\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "config", "agent01", "git", "identity", "--name=N", "--email=E")
	if status != 1 || stdout != "" || !strings.Contains(stderr, "git refused") || !strings.Contains(stderr, "exit status 17") || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}
