package livesuite_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAzureSmokeFailsWhenVersionCommandFailsAfterWritingValidVersions(t *testing.T) {
	config := configWithExecutedSmoke(t, "azure", "az", `printf '%s\n' '{"azure-cli":"2.90.0","extensions":{"azure-devops":"1.0.8"}}'
exit 42
`)
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("failed Azure CLI passed its smoke check")
	}
	if summary := readSummary(t, config, "linux"); !hasCheck(summary, "images/azure/smoke", "fail") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestDotnetSmokeRequiresAllThreeSDKSeries(t *testing.T) {
	for _, missing := range []string{"", "8", "9", "10"} {
		t.Run("missing-"+missing, func(t *testing.T) {
			var output strings.Builder
			for _, series := range []string{"8", "9", "10"} {
				if series != missing {
					fmt.Fprintf(&output, "printf '%%s\\n' '%s.0.100 [/usr/share/dotnet/sdk]'\n", series)
				}
			}
			config := configWithExecutedSmoke(t, "dotnet", "dotnet", output.String())
			err := livesuite.Run(context.Background(), config)
			if (err == nil) != (missing == "") {
				t.Fatalf("missing=%q error=%v", missing, err)
			}
			want := "fail"
			if missing == "" {
				want = "pass"
			}
			if summary := readSummary(t, config, "linux"); !hasCheck(summary, "images/dotnet/smoke", want) {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestAzureSmokeRequiresTheDevOpsExtension(t *testing.T) {
	for _, versions := range []string{
		`{"azure-cli":"2.90.0","extensions":{"azure-devops":"1.0.8"}}`,
		`{"azure-cli":"2.90.0","extensions":{}}`,
	} {
		t.Run(versions, func(t *testing.T) {
			config := configWithExecutedSmoke(t, "azure", "az", fmt.Sprintf("printf '%%s\\n' '%s'\n", versions))
			err := livesuite.Run(context.Background(), config)
			if (err == nil) != strings.Contains(versions, "azure-devops") {
				t.Fatalf("versions=%s error=%v", versions, err)
			}
		})
	}
}

func TestSmokeChecksTheAgentAccountsConfiguredIdentity(t *testing.T) {
	for _, wrong := range []string{"-u", "-g"} {
		t.Run(wrong, func(t *testing.T) {
			config := configWithExecutedSmokePrograms(t, "dotnet", map[string]string{
				"id": fmt.Sprintf(`if [ "$1" = "%s" ] && [ "${2:-}" = agent ]; then
  echo 2000
else
  case "$1" in -u|-g) echo 1000;; -un) echo agent;; *) exit 1;; esac
fi
`, wrong),
				"dotnet": "printf '%s\\n' '8.0.100 [/usr/share/dotnet/sdk]' '9.0.100 [/usr/share/dotnet/sdk]' '10.0.100 [/usr/share/dotnet/sdk]'\n",
			})
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("incorrect agent account identity passed its smoke check")
			}
			if summary := readSummary(t, config, "linux"); !hasCheck(summary, "images/dotnet/smoke", "fail") {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func configWithExecutedSmoke(t *testing.T, kind, command, script string) livesuite.Config {
	t.Helper()
	return configWithExecutedSmokePrograms(t, kind, map[string]string{
		"id":    "case \"$1\" in -u|-g) echo 1000;; -un) echo agent;; *) exit 1;; esac\n",
		command: script,
	})
}

func configWithExecutedSmokePrograms(t *testing.T, kind string, programs map[string]string) livesuite.Config {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("execute in-sandbox Bash scripts on Linux; orchestration tests cover both host platforms")
	}
	config, fixture := newImageCoverageFixture(t, "linux")
	directory := t.TempDir()
	for name, body := range programs {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	executed := false
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if !executed && request.Name != "git" && request.Name != "go" && request.Name != "podman" && request.Args[0] == "shell" && fixture.sandboxes[request.Args[1]].kind == kind {
			executed = true
			cmd := exec.CommandContext(ctx, "bash")
			cmd.Env = append(os.Environ(), "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd.Stdin = request.Streams.Stdin
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := cmd.Run(); err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					return exit.ExitCode(), nil
				}
				return 1, err
			}
			return 0, nil
		}
		return fixture.run(ctx, request)
	}
	return config
}
