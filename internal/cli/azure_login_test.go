package cli_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestAzureLoginReportsManagerThenToolchainThenTerminal(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name, recorded, update string
			manager                testutil.Response
			message                string
		}{
			{"manager before toolchain and terminal", "dotnet", "", testutil.Response{ExitCode: 125}, "does not answer"},
			{"manager before terminal", "azure", "", testutil.Response{Stdout: "not JSON"}, "does not answer"},
			{"no toolchains", "", "azure", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"explicit none", "none", "azure", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"keep dotnet", "dotnet", "azure,dotnet", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"keep every toolchain", "native,dotnet", "azure,dotnet,native", testutil.Response{Stdout: "[]"}, "azure toolchain"},
			{"terminal after manager and toolchain", "azure,dotnet", "", testutil.Response{Stdout: "[]"}, "login needs an interactive terminal"},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner := "default"
				responses := sandboxObjectResponses(&owner, true, map[string]string{"home": owner}, nil)
				responses = withIntegrationToolchains(t, responses, test.recorded)
				responses = append(responses, test.manager)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3], responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "integrations", "login", "agent01", "azure")
				calls := fakes.Calls("podman")
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) || len(calls) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
				}
				if fixture == "windows" {
					calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
				}
				if !reflect.DeepEqual(calls[len(calls)-1].Args, sessionQueryArgs()) {
					t.Fatalf("workflow started after failed precondition: %v", calls)
				}
				if test.update != "" && (!strings.Contains(stderr, "sandboxed-agents update agent01 --with "+test.update) || strings.Contains(stderr, "none") || strings.Contains(stderr, "terminal")) {
					t.Fatalf("stderr=%q", stderr)
				}
				if test.message == "does not answer" && (!strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01") || strings.Contains(stderr, "toolchain") || strings.Contains(stderr, "terminal")) {
					t.Fatalf("stderr=%q", stderr)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestAzureLoginRejectsInvalidRecordedToolchainsWithoutStartingLogin(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owner := "default"
	responses := withIntegrationToolchains(t, sandboxObjectResponses(&owner, true, nil, nil), "azure,nosuch")
	responses = append(responses, testutil.Response{Stdout: "[]"})
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "integrations", "login", "agent01", "azure")
	if status != 1 || stdout != "" || !strings.Contains(stderr, fmt.Sprintf("invalid toolchain %q", "nosuch")) || len(fakes.Calls("podman")) != len(responses) {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
}

func withIntegrationToolchains(t *testing.T, responses []testutil.Response, recorded string) []testutil.Response {
	t.Helper()
	responses = append([]testutil.Response{{}}, responses...)
	return withRecordedContainerLabels(t, responses, map[string]string{"toolchains": recorded})[1:]
}
