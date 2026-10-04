package manager_test

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestDisableSelectionIsSharedWithListStatusLoginAndRun(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".local/state/sandboxed-agents/selection.json", `{"codex":{"version":"1.2.3"},"copilot":{"version":"4.5.6","future":{"keep":true}}}`, 0600)
	writeHomeFile(t, home, ".local/bin/codex", "managed command", 0700)
	writeInstalledPackage(t, home, "@github/copilot", "4.5.6")
	requests := []process.Request{}
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		requests = append(requests, request)
		return 0, nil
	}, agentOptions(home))
	if status, _, diagnostic := runAgentCommand(app, "disable", "codex"); status != 0 {
		t.Fatal(diagnostic)
	}
	for _, test := range []struct {
		args       []string
		status     int
		output     string
		diagnostic string
	}{
		{[]string{"agents", "list"}, 0, "[\"copilot\"]\n", ""},
		{[]string{"agents", "status", "codex"}, 0, "Agent codex is not enabled.\n", ""},
		{[]string{"agents", "check-enabled", "codex"}, 0, "false\n", ""},
		{[]string{"agents", "check-enabled", "copilot"}, 0, "true\n", ""},
		{[]string{"agents", "status", "copilot"}, 0, "Agent copilot is enabled (version 4.5.6).\nSign-in state: unknown.\n", ""},
		{[]string{"agents", "login", "codex", "chatgpt"}, 1, "", "is not enabled"},
		{[]string{"agents", "run", "agent01", "codex", "--help"}, 1, "", "is not enabled"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			status := app.Run(context.Background(), test.args, process.Streams{Stdout: &output, Stderr: &diagnostic})
			if status != test.status || output.String() != test.output || (test.diagnostic == "" && diagnostic.Len() != 0) || !strings.Contains(diagnostic.String(), test.diagnostic) {
				t.Fatalf("status=%d output=%q diagnostic=%q", status, output.String(), diagnostic.String())
			}
			if len(requests) != 0 {
				t.Fatalf("selection check started a process: %+v", requests)
			}
		})
	}
	for _, args := range [][]string{{"agents", "login", "copilot"}, {"agents", "run", "agent01", "copilot", "--help"}} {
		var output, diagnostic bytes.Buffer
		if status := app.Run(context.Background(), args, process.Streams{Stdout: &output, Stderr: &diagnostic}); status != 0 {
			t.Fatalf("args=%q status=%d diagnostic=%q", args, status, diagnostic.String())
		}
	}
	if len(requests) != 2 || !reflect.DeepEqual(requests[0].Args, []string{"login", "--device-code"}) || !reflect.DeepEqual(requests[1].Args, []string{"--help"}) {
		t.Fatalf("remaining enabled agent requests=%+v", requests)
	}
}
