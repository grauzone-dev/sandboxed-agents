package cli_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAgentChangeForceOptionsReachTheManager(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{{"enable", "--force"}, {"enable", "--version", "2.3.4", "--force"}, {"enable", "--force", "--version=2.3.4"}, {"disable", "--force"}, {"update", "--force"}, {"update", "--force", "--unpin"}, {"update", "--unpin", "--force"}} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := agentChangeObjectResponses(fixture)
				report := "Ended the agent session sandboxed-agents-codex of codex before changing its installation.\n"
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
				fakes.Script("podman", responses...)
				invocation := append([]string{"agents", args[0], "agent01", "codex"}, args[1:]...)
				out, diagnostic, status := runCLI(t, fixture, invocation...)
				calls := fakes.Calls("podman")
				want := []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", args[0]}
				if args[0] == "update" {
					want = append(want, "agent01")
				}
				want = append(want, "codex")
				for i := 1; i < len(args); i++ {
					if strings.HasPrefix(args[i], "--version=") {
						want = append(want, "--version", strings.TrimPrefix(args[i], "--version="))
					} else {
						want = append(want, args[i])
					}
				}
				if fixture == "windows" {
					want = append([]string{"--connection", "podman-machine-default"}, want...)
				}
				if status != 0 || diagnostic != "" || out != report || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
					t.Fatalf("status=%d out=%q diagnostic=%q calls=%v want=%v", status, out, diagnostic, calls, want)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestAgentChangesWithForceStillRefuseAnUnavailableManager(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, action := range []string{"disable", "update", "enable"} {
			for _, force := range []bool{false, true} {
				t.Run(fixture+"/"+action+"/"+map[bool]string{false: "plain", true: "force"}[force], func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					responses := agentChangeObjectResponses(fixture)
					responses = append(responses, testutil.Response{ExitCode: 127, Stderr: "manager unavailable\n"})
					fakes.Script("podman", responses...)
					args := []string{"agents", action, "agent01", "codex"}
					if action == "enable" {
						args = append(args, "--version", "2.3.4")
					}
					if force {
						args = append(args, "--force")
					}
					out, diagnostic, status := runCLI(t, fixture, args...)
					calls := fakes.Calls("podman")
					if status == 0 || out != "" || !strings.Contains(diagnostic, "manager does not answer") || !strings.Contains(diagnostic, "check agent01") || !strings.Contains(diagnostic, "restart agent01") || len(calls) != len(responses) {
						t.Fatalf("status=%d out=%q diagnostic=%q calls=%v", status, out, diagnostic, calls)
					}
					if last := calls[len(calls)-1].Args; last[len(last)-1] != "version" {
						t.Fatalf("guard or mutation ran after unavailable manager: %v", last)
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestAgentChangeRefusalsNameTheRunningSessionAtTheCLIBoundary(t *testing.T) {
	for _, args := range [][]string{{"disable"}, {"update"}, {"update", "--unpin"}, {"enable", "--version", "2.3.4"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			reason := "agent session sandboxed-agents-codex of codex is running; repeat with --force\n"
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{ExitCode: 1, Stderr: reason})
			fakes.Script("podman", responses...)
			invocation := append([]string{"agents", args[0], "agent01", "codex"}, args[1:]...)
			out, diagnostic, status := runCLI(t, "sandbox-host", invocation...)
			if status == 0 || out != "" || !strings.Contains(diagnostic, reason) || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d out=%q diagnostic=%q calls=%v", status, out, diagnostic, fakes.Calls("podman"))
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestAgentForceOptionsRejectInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{{"enable", "--force", "--force"}, {"enable", "--force", "--version", "1.2.3", "--version", "2.3.4"}, {"enable", "--version", "--force"}, {"disable", "--force=true"}, {"disable", "--force", "yes"}, {"update", "--force", "--unpin", "--unpin"}, {"status", "--force"}} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				invocation := append([]string{"agents", args[0], "agent01", "codex"}, args[1:]...)
				out, diagnostic, status := runCLI(t, fixture, invocation...)
				if status == 0 || out != "" || !strings.Contains(diagnostic, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d out=%q diagnostic=%q calls=%v", status, out, diagnostic, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func agentChangeObjectResponses(fixture string) []testutil.Response {
	owned := "default"
	responses := sandboxObjectResponses(&owned, true, nil, nil)
	if fixture == "windows" {
		return append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
	}
	return responses
}
