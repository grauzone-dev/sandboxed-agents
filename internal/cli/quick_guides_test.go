package cli_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestQuickGuideCommandsAreAcceptedByCLI(t *testing.T) {
	for _, host := range []struct{ guide, fixture string }{
		{"quick-guide-linux.md", "ssh-ports-free"},
		{"quick-guide-windows.md", "windows"},
	} {
		t.Run(host.guide, func(t *testing.T) {
			guide := host.guide
			document, err := os.ReadFile(filepath.Join("..", "..", "docs", guide))
			if err != nil {
				t.Fatal(err)
			}
			commands := 0
			scanner := bufio.NewScanner(strings.NewReader(string(document)))
			for line := 1; scanner.Scan(); line++ {
				command := strings.TrimSpace(scanner.Text())
				fields := strings.Fields(command)
				if len(fields) == 0 || (fields[0] != "sandboxed-agents" && fields[0] != "sandboxed-agents.cmd") {
					continue
				}
				commands++
				t.Run(command, func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					fakes.Script("podman",
						testutil.Response{RepeatForArgs: []string{"--version"}, Stdout: "podman version 5.0.0\n"},
						testutil.Response{ExitCode: 99, Stderr: "offline guide check stops after usage validation\n"},
					)
					fakes.Script("ssh", testutil.Response{ExitCode: 99})
					args := fields[1:]
					stdout, stderr, status := runCLI(t, host.fixture, args...)
					if strings.Contains(stderr, "Usage:") {
						t.Fatalf("%s:%d rejects %q: status=%d stdout=%q stderr=%q", guide, line, command, status, stdout, stderr)
					}
				})
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if commands == 0 {
				t.Fatal("quick guide contains no sandboxed-agents command lines")
			}
		})
	}
}
