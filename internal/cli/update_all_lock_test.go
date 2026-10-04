package cli_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpdateAllPassesOverABusySandboxWithoutInspectingItAndUpdatesOtherSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, _, _ := sshSetupHost(t, host.windows)
			release, err := sandbox.LockLifecycle(host.name, "default", "agent01")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			responses := updateAllInventory("agent01", "agent02")
			responses = append(responses, updateAllObjects(t, "agent02", true, "old-base", "")...)
			responses = append(responses, updateAllCurrentImage("")...)
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdateAll(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "--all")
			if status == 0 || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "another lifecycle command") || !strings.Contains(stderr, "retry") || !strings.Contains(stdout, "Sandbox agent02 is updated.") || strings.Contains(stdout, "Sandbox agent01 is updated.") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			for _, call := range fakes.Calls("podman") {
				if strings.Contains(strings.Join(podmanUpdateArgs(call.Args), " "), "agent01") {
					t.Fatalf("inspected or changed the busy sandbox: %v", call.Args)
				}
			}
			assertUpdateAllPreservesData(t, fakes)
		})
	}
}

func TestUpdateAllKeepsEverySandboxLockedFromInspectionThroughAllBuildsAndUpdates(t *testing.T) {
	for _, failBuild := range []bool{false, true} {
		name := "success"
		if failBuild {
			name = "build-failure"
		}
		t.Run(name, func(t *testing.T) {
			fakes, _, _, _ := sshSetupHost(t, false)
			responses := updateAllInventory("agent01", "agent02")
			for index, selection := range []string{"dotnet", "native"} {
				sandboxName := []string{"agent01", "agent02"}[index]
				responses = append(responses, updateAllObjects(t, sandboxName, true, "old-"+selection, selection)...)
				responses = append(responses, updateAllOutdatedImage(selection, false)...)
			}
			responses = append(responses, updateAllBuildImage("dotnet", false)...)
			if failBuild {
				responses = append(responses, updateAllOutdatedImage("native", false)...)
				responses = append(responses, testutil.Response{ExitCode: 42})
			} else {
				responses = append(responses, updateAllBuildImage("native", false)...)
				for range 2 {
					responses = append(responses, successfulRunningUpdateResponses()...)
				}
			}
			scriptUpdateAll(t, fakes, false, responses)
			inspected := make(map[string]bool)
			builds, replacements, completions := 0, 0, 0
			host := sshPortHostFixture("ssh-ports-free")
			host.Run = func(ctx context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					args := podmanUpdateArgs(request.Args)
					for _, sandboxName := range []string{"agent01", "agent02"} {
						if strings.Contains(strings.Join(args, " "), "sandboxed-agents.default."+sandboxName) {
							inspected[sandboxName] = true
						}
					}
					if args[0] == "build" {
						builds++
						if len(inspected) != 2 {
							t.Fatalf("built before inspecting both sandboxes: %v", args)
						}
					}
					if args[0] == "rename" {
						replacements++
					}
					if args[0] == "rm" && (slices.Contains(args, "sandboxed-agents-backup.default.agent01") || slices.Contains(args, "sandboxed-agents-backup.default.agent02")) {
						completions++
					}
				}
				for sandboxName := range inspected {
					release, err := sandbox.LockLifecycle("linux", "default", sandboxName)
					if err == nil {
						release()
						t.Fatalf("sandbox %s was unlocked during %s %v", sandboxName, request.Name, request.Args)
					}
					if !strings.Contains(err.Error(), "another lifecycle command") {
						t.Fatalf("checking lifecycle lock for %s: %v", sandboxName, err)
					}
				}
				return platform.Run(ctx, request)
			}
			var stdout, stderr bytes.Buffer
			status := cli.RunWithHost([]string{"update", "--all"}, &stdout, &stderr, "v1.2.3", assets.Hash(), host)
			if builds != 2 || len(inspected) != 2 {
				t.Fatalf("builds=%d inspected=%v status=%d stdout=%q stderr=%q", builds, inspected, status, stdout.String(), stderr.String())
			}
			if failBuild {
				if status == 0 || !strings.Contains(stderr.String(), "42") || replacements != 0 || completions != 0 {
					t.Fatalf("status=%d replacements=%d completions=%d stdout=%q stderr=%q", status, replacements, completions, stdout.String(), stderr.String())
				}
			} else if status != 0 || stderr.Len() != 0 || replacements != 2 || completions != 2 {
				t.Fatalf("status=%d replacements=%d completions=%d stdout=%q stderr=%q", status, replacements, completions, stdout.String(), stderr.String())
			}
			for _, sandboxName := range []string{"agent01", "agent02"} {
				release, err := sandbox.LockLifecycle("linux", "default", sandboxName)
				if err != nil {
					t.Fatalf("sandbox %s remains locked after command exit: %v", sandboxName, err)
				}
				release()
			}
			assertUpdateAllPreservesData(t, fakes)
		})
	}
}
