package cli_test

import (
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestListMarksPreviousImagesAfterBuildInAnotherControllerGroup(t *testing.T) {
	for _, selection := range []string{"", "native", "azure"} {
		for _, failedNative := range []bool{false, true} {
			t.Run(selection+"/"+map[bool]string{false: "success", true: "failed-native"}[failedNative], func(t *testing.T) {
				fakes := linuxHost(t)
				hash := imageBuildAssetHash(t)
				baseTag := "localhost/sandboxed-agents:base-" + hash
				azureTag := "localhost/sandboxed-agents:toolchains-azure-" + hash
				nativeTag := "localhost/sandboxed-agents:toolchains-native-" + hash
				responses := []testutil.Response{
					{Stdout: "podman version 5.0.0\n"}, {},
					{Stdout: `[{"Id":"old-azure"},{"Id":"old-native"}]`},
					imageInspection(t, "old-azure", hash, "azure", azureTag),
					imageInspection(t, "old-native", hash, "native", nativeTag),
					{Stdout: `[{"Id":"current-base"}]`}, {}, {},
				}
				if failedNative {
					responses[7].ExitCode = 42
				}
				base := listImageSandbox("sandboxed-agents.observer.base", "", "old-base", baseTag, false)
				azure := listImageSandbox("sandboxed-agents.observer.azure", "azure", "old-azure", azureTag, false)
				native := listImageSandbox("sandboxed-agents.observer.native", "native", "old-native", nativeTag, false)
				for _, record := range []map[string]any{base, azure, native} {
					record["Config"].(map[string]any)["Labels"].(map[string]string)["io.github.sandboxed-agents.owner"] = "observer"
				}
				responses = append(responses,
					listJSONResponse([]map[string]any{base, native, azure}), testutil.Response{Stdout: `[]`},
					listJSONResponse([]map[string]any{azure}), listJSONResponse([]map[string]any{base}), listJSONResponse([]map[string]any{native}),
				)
				responses = append(responses, listCurrentImageResponses("azure", "current-azure", "current-base")...)
				responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
				nativeImage := listCurrentImageResponses("native", "current-native", "current-base")
				if failedNative {
					nativeImage = listToolchainImageResponses("current-base", "old-native", "old-base")
				}
				responses = append(responses, nativeImage...)
				fakes.Script("podman", responses...)
				t.Setenv("SANDBOXED_AGENTS_GROUP", "builder")
				args := []string{"build"}
				if selection != "" {
					args = append(args, "--with", selection)
				}
				stdout, stderr, status := runCLI(t, "linux-build", args...)
				if failedNative {
					if status == 0 || !strings.Contains(stderr, "native") {
						t.Fatalf("build status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
				} else if status != 0 || stderr != "" {
					t.Fatalf("build status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				buildCalls := fakes.Calls("podman")
				assertImageBuild(t, buildCalls[1].Args, hash, baseTag, "", "")
				assertImageBuild(t, buildCalls[6].Args, hash, azureTag, "azure", "current-base")
				assertImageBuild(t, buildCalls[7].Args, hash, nativeTag, "native", "current-base")
				t.Setenv("SANDBOXED_AGENTS_GROUP", "observer")
				stdout, stderr, status = runCLI(t, "linux-build", "list")
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				if status != 0 || stderr != "" || len(lines) != 4 || strings.Count(stdout, "(outdated)") != 3 {
					t.Fatalf("list status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for index, name := range []string{"azure", "base", "native"} {
					if !strings.HasPrefix(strings.Join(strings.Fields(lines[index+1]), " "), name+" stopped (outdated) ") {
						t.Fatalf("row=%q", lines[index+1])
					}
				}
				if len(fakes.Calls("podman")) != len(responses) {
					t.Fatalf("calls=%v", fakes.Calls("podman"))
				}
				for _, call := range fakes.Calls("podman")[len(buildCalls):] {
					if call.Args[0] != "ps" && call.Args[0] != "volume" && call.Args[0] != "container" && call.Args[0] != "image" {
						t.Fatalf("list changed a sandbox or image: %v", call.Args)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}
