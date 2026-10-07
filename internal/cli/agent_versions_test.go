package cli_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestEnableAgentForwardsAnExactVersionToTheManager(t *testing.T) {
	for _, options := range [][]string{{"--version", "1.2.3"}, {"--version=1.2.3"}, {"--version", "1.2.3-beta.1+build.2"}} {
		t.Run(options[0]+options[len(options)-1], func(t *testing.T) {
			version := "1.2.3"
			if len(options) > 1 {
				version = options[1]
			}
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			report := "Agent codex is enabled (version " + version + ").\nPin: " + version + ".\n"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
			fakes.Script("podman", responses...)
			args := append([]string{"agents", "enable", "agent01", "codex"}, options...)
			stdout, stderr, status := runCLI(t, "sandbox-host", args...)
			calls := fakes.Calls("podman")
			want := []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "enable", "codex", "--version", version}
			if status != 0 || stdout != report || stderr != "" || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpdateAgentForwardsTheSandboxAndUnpinToTheManager(t *testing.T) {
	for _, test := range []struct {
		name    string
		options []string
		report  string
	}{
		{"pinned", nil, "Agent codex is updated (version 1.2.3).\nPin: 1.2.3.\n"},
		{"unpinned", nil, "Agent codex is updated (version 2.0.0).\nPin: none.\n"},
		{"unpin", []string{"--unpin"}, "Agent codex is updated (version 2.0.0).\nPin: none.\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: test.report})
			fakes.Script("podman", responses...)
			args := append([]string{"agents", "update", "agent01", "codex"}, test.options...)
			stdout, stderr, status := runCLI(t, "sandbox-host", args...)
			calls := fakes.Calls("podman")
			want := append([]string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "update", "agent01", "codex"}, test.options...)
			if status != 0 || stdout != test.report || stderr != "" || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestAgentVersionOptionsRejectInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{
			{"enable", "--version"}, {"enable", "--version="}, {"enable", "--version", "--unpin"},
			{"enable", "--version", "1.2.3", "--version", "2.0.0"},
			{"enable", "--version=1.2.3", "--version=1.2.3"},
			{"enable", "--unpin"}, {"enable", "--force", "--force"}, {"enable", "--version", "1.2.3", "extra"},
			{"update", "--unpin", "--unpin"}, {"update", "--unpin=true"}, {"update", "--unpin", "extra"},
			{"update", "--version", "1.2.3"}, {"update", "--force", "--force"},
		} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				invocation := append([]string{"agents", args[0], "agent01", "codex"}, args[1:]...)
				stdout, stderr, status := runCLI(t, fixture, invocation...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestEnableAgentRejectsNonExactVersionsBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, version := range []string{"latest", "next", "^1.2.3", "~1.2.3", "1.2", "1", "v1.2.3", "1.2.3 || 2.0.0", "1.2.3\n", "01.2.3", "1.2.3-beta.01"} {
			t.Run(fixture+"/"+version, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, "agents", "enable", "agent01", "codex", "--version", version)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "invalid agent version") || !strings.Contains(stderr, "exact version") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
			})
		}
	}
}

func TestAgentCommandsForwardPinReportsWithoutChangingThem(t *testing.T) {
	for _, test := range []struct {
		operation string
		report    string
	}{
		{"enable", "Agent codex is enabled (version 1.2.3).\nPin: 1.2.3.\n"},
		{"status", "Agent codex is enabled (version 1.2.3).\nSign-in state: unknown.\nAgent session: not running.\nPin: 1.2.3.\n"},
		{"status", "Agent codex is enabled (version 2.0.0).\nSign-in state: unknown.\nAgent session: not running.\nPin: none.\n"},
		{"disable", "Agent codex is disabled (removed pin 1.2.3).\n"},
	} {
		t.Run(test.operation+"/"+test.report, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: test.report})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", test.operation, "agent01", "codex")
			calls := fakes.Calls("podman")
			want := []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", test.operation, "codex"}
			if status != 0 || stdout != test.report || stderr != "" || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
		})
	}
}

func TestUpdateAgentReportsWhenTheAgentIsNotEnabled(t *testing.T) {
	for _, options := range [][]string{nil, {"--unpin"}} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := sandboxObjectResponses(&owned, true, nil, nil)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{ExitCode: 1, Stderr: "agent codex is not enabled; use sandboxed-agents agents enable agent01 codex\n"})
			fakes.Script("podman", responses...)
			args := append([]string{"agents", "update", "agent01", "codex"}, options...)
			stdout, stderr, status := runCLI(t, "sandbox-host", args...)
			calls := fakes.Calls("podman")
			want := append([]string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "update", "agent01", "codex"}, options...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "agents enable agent01 codex") || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-1].Args, want) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
			}
		})
	}
}

func TestAgentVersionHelpExplainsPinAndUpdateOptions(t *testing.T) {
	for _, operation := range []string{"enable", "update"} {
		t.Run(operation, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "sandbox-host", "agents", operation, "--help")
			option := "--version X"
			if operation == "update" {
				option = "--unpin"
			}
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Usage: sandboxed-agents agents "+operation+" NAME AGENT") || !strings.Contains(stdout, option) || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
		})
	}
}
