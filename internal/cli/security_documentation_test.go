package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestSecurityDocumentsTheDefaultPodmanOptionsIssuedByUp(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
			scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}

			var create []string
			for _, call := range fakes.Calls("podman") {
				args := call.Args
				if host.windows && len(args) >= 2 && args[0] == "--connection" {
					args = args[2:]
				}
				if len(args) > 0 && args[0] == "create" {
					if create != nil {
						t.Fatal("up issued more than one container creation call")
					}
					create = args
				}
			}
			if create == nil {
				t.Fatal("up issued no container creation call")
			}
			assertAllocatedSSHPort(t, create)
			assertNoSSH(t, fakes)

			document, err := os.ReadFile(filepath.Join("..", "..", "SECURITY.md"))
			if err != nil {
				t.Fatal(err)
			}
			for index := 1; index < len(create)-1; index++ {
				option := create[index]
				if !strings.HasPrefix(option, "--") {
					t.Fatalf("unexpected positional argument in create: %q", option)
				}
				name, _, hasValue := strings.Cut(option, "=")
				if !hasValue && index+1 < len(create)-1 && !strings.HasPrefix(create[index+1], "--") {
					index++
					option += " " + create[index]
				}
				if name == "--name" || name == "--label" {
					option = name
				}
				if name == "--publish" {
					parts := strings.Split(option, ":")
					if len(parts) != 3 {
						t.Fatalf("unexpected SSH publication: %q", option)
					}
					parts[1] = "PORT"
					option = strings.Join(parts, ":")
				}
				option = strings.ReplaceAll(option, "sandboxed-agents.default.agent01", "sandboxed-agents.GROUP.NAME")
				if !strings.Contains(string(document), "`"+option+"`") {
					t.Errorf("SECURITY.md does not document the default Podman option %q", option)
				}
			}
		})
	}
}
