package cli_test

import (
	"reflect"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestDisableAgentRemovesThroughTheSandboxManager(t *testing.T) {
	for _, agent := range []string{"copilot", "claude", "codex", "opencode"} {
		for _, report := range []string{"Agent " + agent + " is disabled.\n", "Agent " + agent + " is not enabled; nothing to do.\n"} {
			t.Run(agent+"/"+report, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owned := "default"
				responses := sandboxObjectResponses(&owned, true, map[string]string{"home": "default"}, nil)
				responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager dev\n"}, testutil.Response{Stdout: report})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "sandbox-host", "agents", "disable", "agent01", agent)
				calls := fakes.Calls("podman")
				want := []testutil.Call{
					{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"}},
					{Args: []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "agents", "disable", agent}},
				}
				if status != 0 || stderr != "" || stdout != report || len(calls) != len(responses) || !reflect.DeepEqual(calls[len(calls)-2:], want) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}
