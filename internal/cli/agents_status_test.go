package cli_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAgentStatusReportsThroughTheSandboxManager(t *testing.T) {
	for _, agent := range []string{"copilot", "claude", "codex", "opencode"} {
		t.Run(agent, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			report := "Agent " + agent + " is enabled (version 1.2.3).\nSign-in state: unknown.\n"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "status", "agent01", agent)
			if status != 0 || stderr != "" || stdout != report {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			want := []testutil.Call{
				{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
				{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "status", agent}},
			}
			if len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2:], want) {
				t.Fatalf("calls=%v want manager calls=%v", calls, want)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestAgentStatusReturnsZeroForEveryReport(t *testing.T) {
	for _, report := range []string{
		"Agent claude is not enabled.\n",
		"Agent claude is enabled (version 1.2.3).\nSign-in state: signed in.\n",
		"Agent claude is enabled (version 1.2.3).\nSign-in state: not signed in.\n",
		"Agent claude is enabled (version 1.2.3).\nSign-in state: unknown.\n",
	} {
		t.Run(report, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "status", "agent01", "claude")
			if status != 0 || stdout != report || stderr != "" || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}

func TestAgentStatusRejectsInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{
			{"agents", "status"}, {"agents", "status", "agent01"},
			{"agents", "status", ".bad", "codex"},
			{"agents", "status", "agent01", "codex", "extra"},
			{"agents", "status", "agent01", "codex", "--force"},
			{"agents", "status", "agent01", "codex", "--version", "1.0.0"},
		} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
			})
		}
	}
}

func TestAgentStatusPropagatesAnUnreportableManagerFailure(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{ExitCode: 1, Stderr: "invalid installed package version\n"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "status", "agent01", "codex")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "invalid installed package version") || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
}
