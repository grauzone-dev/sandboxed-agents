package cli_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func listImageSandbox(name, set, image, imageName string, running bool) map[string]any {
	return map[string]any{
		"Name": name, "Names": []string{name}, "Image": image, "ImageName": imageName,
		"Config": map[string]any{"Labels": map[string]string{
			"io.github.sandboxed-agents.owner":          "default",
			"io.github.sandboxed-agents.workspace-kind": "volume",
			"io.github.sandboxed-agents.toolchains":     set,
			"io.github.sandboxed-agents.ssh-port":       "2300",
		}},
		"State": map[string]bool{"Running": running},
	}
}

func TestListMarksSandboxWhoseBaseImageWasRebuiltUnderSameTag(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	record := listImageSandbox("sandboxed-agents.default.agent01", "", "old-base", "localhost/sandboxed-agents:base-fixture-assets", false)
	fakes.Script("podman",
		listJSONResponse([]map[string]any{record}),
		testutil.Response{Stdout: `[]`},
		listJSONResponse([]map[string]any{record}),
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"current-base"}]`},
	)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 stopped (outdated) volume 2300 - - -"
	if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertListReadOnly(t, fakes, false)
}

func TestListComparesImageIDsAndBaseLineage(t *testing.T) {
	base := listCurrentImageResponses("", "current-base", "current-base")
	native := listCurrentImageResponses("native", "current-native", "current-base")
	stale := listToolchainImageResponses("current-base", "current-native", "old-base")
	noLabel := listToolchainImageResponses("current-base", "current-native", "")
	configLabel := append(append([]testutil.Response{}, base...), testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-native","Config":{"Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}}]`})
	for _, scenario := range []struct {
		name, set, image, imageName, tag string
		responses                        []testutil.Response
		outdated                         bool
	}{
		{name: "current-base", image: "current-base", responses: base},
		{name: "current-ID-under-another-name", image: "current-base", imageName: "some-other-image:alias", responses: base},
		{name: "old-executable-base-tag", image: "old-base", imageName: "localhost/sandboxed-agents:base-old-assets", responses: base, outdated: true},
		{name: "old-executable-missing-base", image: "old-base", imageName: "localhost/sandboxed-agents:base-old-assets", responses: []testutil.Response{{ExitCode: 1}}, outdated: true},
		{name: "missing-base-for-toolchains", set: "native", image: "old-native", responses: []testutil.Response{{ExitCode: 1}}, outdated: true},
		{name: "current-toolchain", set: "native", tag: "native", image: "current-native", responses: native},
		{name: "toolchain-base-label-in-config", set: "native", tag: "native", image: "current-native", responses: configLabel},
		{name: "same-tag-toolchain-rebuild", set: "native", tag: "native", image: "old-native", imageName: "localhost/sandboxed-agents:toolchains-native-fixture-assets", responses: native, outdated: true},
		{name: "old-executable-toolchain-tag", set: "native", tag: "native", image: "old-native", imageName: "localhost/sandboxed-agents:toolchains-native-old-assets", responses: native, outdated: true},
		{name: "old-executable-missing-toolchain", set: "native", tag: "native", image: "old-native", imageName: "localhost/sandboxed-agents:toolchains-native-old-assets", responses: append(append([]testutil.Response{}, base...), testutil.Response{ExitCode: 1}), outdated: true},
		{name: "failed-toolchain-rebuild", set: "native", tag: "native", image: "current-native", responses: stale, outdated: true},
		{name: "missing-base-image-label", set: "native", tag: "native", image: "current-native", responses: noLabel, outdated: true},
		{name: "canonical-toolchain-set", set: "native,azure,native", tag: "azure-native", image: "current-combined", responses: listCurrentImageResponses("azure,native", "current-combined", "current-base")},
	} {
		for _, fixture := range []string{"sandbox-host", "windows"} {
			for _, running := range []bool{false, true} {
				t.Run(scenario.name+"/"+fixture+"/"+map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
					record := listImageSandbox("sandboxed-agents.team-a.agent01", scenario.set, scenario.image, scenario.imageName, running)
					record["Config"].(map[string]any)["Labels"].(map[string]string)["io.github.sandboxed-agents.owner"] = "team-a"
					responses := []testutil.Response{listJSONResponse([]map[string]any{record}), {Stdout: `[]`}, listJSONResponse([]map[string]any{record})}
					responses = append(responses, scenario.responses...)
					state := "stopped"
					agents := "-"
					if running {
						state, agents = "running", "codex"
						responses = append(responses, testutil.Response{Stdout: `["codex"]`})
					}
					if scenario.outdated {
						state += " (outdated)"
					}
					if fixture == "windows" {
						responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
						for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
							t.Setenv(key, "ambient-other-target")
						}
						for index := 2; index < len(responses); index++ {
							responses[index].AbsentEnv = []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"}
						}
					}
					fakes.Script("podman", responses...)
					stdout, stderr, status := runCLI(t, fixture, "list")
					selection := scenario.set
					if selection == "" {
						selection = "-"
					}
					want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 " + state + " volume 2300 " + selection + " " + agents + " -"
					if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
						t.Fatalf("status=%d stdout=%q stderr=%q want=%q", status, stdout, stderr, want)
					}
					calls := fakes.Calls("podman")
					if fixture == "windows" {
						calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
					}
					var imageCalls [][]string
					for _, call := range calls {
						if call.Args[0] == "image" {
							imageCalls = append(imageCalls, call.Args)
						}
					}
					wantCalls := [][]string{{"image", "exists", "localhost/sandboxed-agents:base-fixture-assets"}}
					if len(scenario.responses) > 1 {
						wantCalls = append(wantCalls, []string{"image", "inspect", "localhost/sandboxed-agents:base-fixture-assets"})
					}
					if len(scenario.responses) > 2 {
						wantCalls = append(wantCalls, []string{"image", "exists", "localhost/sandboxed-agents:toolchains-" + scenario.tag + "-fixture-assets"})
					}
					if len(scenario.responses) > 3 {
						wantCalls = append(wantCalls, []string{"image", "inspect", "localhost/sandboxed-agents:toolchains-" + scenario.tag + "-fixture-assets"})
					}
					if !reflect.DeepEqual(imageCalls, wantCalls) {
						t.Fatalf("image calls=%v want=%v", imageCalls, wantCalls)
					}
					assertListReadOnly(t, fakes, fixture == "windows")
				})
			}
		}
	}
}

func listToolchainImageResponses(baseID, imageID, recordedBaseID string) []testutil.Response {
	image := map[string]any{"Id": imageID}
	if recordedBaseID != "" {
		image["Labels"] = map[string]string{"io.github.sandboxed-agents.base-image": recordedBaseID}
	}
	responses := listCurrentImageResponses("", baseID, baseID)
	return append(responses, testutil.Response{}, listJSONResponse([]map[string]any{image}))
}

func TestListLeavesCurrentToolchainImageUnmarked(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	record := listImageSandbox("sandboxed-agents.default.agent01", "native", "current-native", "localhost/sandboxed-agents:toolchains-native-fixture-assets", false)
	responses := []testutil.Response{
		listJSONResponse([]map[string]any{record}),
		{Stdout: `[]`},
		listJSONResponse([]map[string]any{record}),
	}
	responses = append(responses, listCurrentImageResponses("native", "current-native", "current-base")...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 stopped volume 2300 native - -"
	if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertListReadOnly(t, fakes, false)
}

func TestListKeepsSandboxesWithUnrecognizedToolchainsVisibleAndOutdated(t *testing.T) {
	for _, set := range []string{"unknown", "none,native"} {
		t.Run(set, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			unknown := listImageSandbox("sandboxed-agents.default.alpha", set, "old-image", "", true)
			current := listImageSandbox("sandboxed-agents.default.beta", "", "current-base", "", false)
			responses := []testutil.Response{
				listJSONResponse([]map[string]any{current, unknown}), {Stdout: `[]`},
				listJSONResponse([]map[string]any{unknown}), listJSONResponse([]map[string]any{current}),
			}
			responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
			responses = append(responses, testutil.Response{Stdout: `["codex"]`})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES alpha running (outdated) volume 2300 " + set + " codex - beta stopped volume 2300 - - -"
			if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want || len(fakes.Calls("podman")) != len(responses) {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestListMarksOnlyTheOutdatedSandboxInAMixedList(t *testing.T) {
	for _, set := range []string{"", "native", "native,azure,native"} {
		for _, fixture := range []string{"sandbox-host", "windows"} {
			for _, oldRunning := range []bool{false, true} {
				for _, currentRunning := range []bool{false, true} {
					t.Run(set+"/"+fixture+"/"+map[bool]string{false: "old-stopped", true: "old-running"}[oldRunning]+"/"+map[bool]string{false: "current-stopped", true: "current-running"}[currentRunning], func(t *testing.T) {
						fakes := testutil.NewFakePrograms(t)
						image := "current-base"
						if set != "" {
							image = "current-toolchain"
						}
						old := listImageSandbox("sandboxed-agents.default.alpha", set, "old-image", "", oldRunning)
						currentSet := set
						if set == "native,azure,native" {
							currentSet = "azure,native"
						}
						current := listImageSandbox("sandboxed-agents.default.beta", currentSet, image, "", currentRunning)
						foreign := listImageSandbox("sandboxed-agents.other.hidden", "", "foreign-image", "", true)
						responses := []testutil.Response{
							listJSONResponse([]map[string]any{current, foreign, old}), {Stdout: `[]`},
							listJSONResponse([]map[string]any{old}), listJSONResponse([]map[string]any{current}),
						}
						responses = append(responses, listCurrentImageResponses(currentSet, image, "current-base")...)
						for _, running := range []bool{oldRunning, currentRunning} {
							if running {
								responses = append(responses, testutil.Response{Stdout: `["codex"]`})
							}
						}
						if fixture == "windows" {
							responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
						}
						fakes.Script("podman", responses...)
						stdout, stderr, status := runCLI(t, fixture, "list")
						lines := strings.Split(strings.TrimSpace(stdout), "\n")
						if status != 0 || stderr != "" || len(lines) != 3 {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						for index, row := range []struct {
							name, selection   string
							running, outdated bool
						}{{"alpha", set, oldRunning, true}, {"beta", currentSet, currentRunning, false}} {
							state, agents := "stopped", "-"
							if row.running {
								state, agents = "running", "codex"
							}
							if row.outdated {
								state += " (outdated)"
							}
							if row.selection == "" {
								row.selection = "-"
							}
							want := row.name + " " + state + " volume 2300 " + row.selection + " " + agents + " -"
							if strings.Join(strings.Fields(lines[index+1]), " ") != want {
								t.Fatalf("row=%q want=%q", lines[index+1], want)
							}
						}
						if len(fakes.Calls("podman")) != len(responses) {
							t.Fatalf("unexpected or repeated query: %v", fakes.Calls("podman"))
						}
						assertListReadOnly(t, fakes, fixture == "windows")
					})
				}
			}
		}
	}
}

func TestListKeepsAdministrativeStatesUnmarkedWithoutImageQueries(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, state := range []string{"update interrupted", "owner conflict", "volumes only"} {
			t.Run(fixture+"/"+state, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owner, foreign := "default", "other"
				container, backup := &owner, &owner
				volumes := map[string]string{"home": owner}
				if state == "owner conflict" {
					volumes["home"] = foreign
				}
				if state == "volumes only" {
					container, backup = nil, nil
				}
				responses := listOneSandboxResponses("default", "agent01", container, true, volumes, backup)
				for index := range responses {
					responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"Image":"current-base"`, `"Image":"outdated-image"`)
				}
				if fixture == "windows" {
					responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "list")
				if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 "+state+" ") || strings.Contains(stdout, "outdated") || len(fakes.Calls("podman")) != len(responses) {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertListReadOnly(t, fakes, fixture == "windows")
			})
		}
	}
}

func TestListReportsImageLookupFailuresWithoutAPartialTable(t *testing.T) {
	base := listCurrentImageResponses("", "current-base", "current-base")
	for _, scenario := range []struct {
		name, set, image, message string
		responses                 []testutil.Response
	}{
		{name: "missing-container-image-ID", message: "image ID"},
		{name: "base-existence-query-failed", image: "old-image", message: "exit status 125", responses: []testutil.Response{{ExitCode: 125}}},
		{name: "base-inspection-failed", image: "old-image", message: "exit status 125", responses: []testutil.Response{{}, {ExitCode: 125}}},
		{name: "base-inspection-not-JSON", image: "old-image", message: "read podman", responses: []testutil.Response{{}, {Stdout: `not-json`}}},
		{name: "base-inspection-null", image: "old-image", message: "expected one image", responses: []testutil.Response{{}, {Stdout: `null`}}},
		{name: "base-inspection-no-ID", image: "old-image", message: "expected one image", responses: []testutil.Response{{}, {Stdout: `[{}]`}}},
		{name: "toolchain-existence-query-failed", set: "native", image: "old-image", message: "exit status 125", responses: append(append([]testutil.Response{}, base...), testutil.Response{ExitCode: 125})},
		{name: "toolchain-inspection-failed", set: "native", image: "old-image", message: "exit status 125", responses: append(append([]testutil.Response{}, base...), testutil.Response{}, testutil.Response{ExitCode: 125})},
		{name: "toolchain-inspection-empty", set: "native", image: "old-image", message: "expected one image", responses: append(append([]testutil.Response{}, base...), testutil.Response{}, testutil.Response{Stdout: `[]`})},
	} {
		for _, fixture := range []string{"sandbox-host", "windows"} {
			t.Run(scenario.name+"/"+fixture, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				current := listImageSandbox("sandboxed-agents.default.alpha", "", "current-base", "", false)
				record := listImageSandbox("sandboxed-agents.default.beta", scenario.set, scenario.image, "", true)
				responses := []testutil.Response{
					listJSONResponse([]map[string]any{record, current}), {Stdout: `[]`},
					listJSONResponse([]map[string]any{current}), listJSONResponse([]map[string]any{record}),
				}
				if scenario.set != "" || len(scenario.responses) == 0 {
					responses = append(responses, base...)
				}
				responses = append(responses, scenario.responses...)
				if fixture == "windows" {
					responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "list")
				if status == 0 || stdout != "" || !strings.Contains(stderr, scenario.message) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, call := range fakes.Calls("podman") {
					if call.Args[0] == "exec" {
						t.Fatalf("queried a manager after an invalid image lookup: %v", call.Args)
					}
				}
				assertListReadOnly(t, fakes, fixture == "windows")
			})
		}
	}
}
