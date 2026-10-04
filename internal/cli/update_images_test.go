package cli_test

import (
	"encoding/csv"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func successfulRunningUpdateResponses() []testutil.Response {
	return []testutil.Response{{}, {}, {}, {}, {Stdout: "sandboxed-agents-manager v1.2.3\n"}, {}}
}

func updateCurrentNativeImageResponses() []testutil.Response {
	return []testutil.Response{
		{}, {Stdout: `[{"Id":"current-base"}]`},
		{}, {Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`},
	}
}

func assertUpdateCreatedFromImage(t *testing.T, fakes *testutil.FakePrograms, image string) []string {
	t.Helper()
	var create []string
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if args[0] != "create" {
			continue
		}
		if create != nil {
			t.Fatal("update created more than one replacement")
		}
		create = args
	}
	if len(create) == 0 || create[len(create)-1] != image {
		t.Fatalf("replacement did not use image ID %q: %v", image, create)
	}
	return create
}

func TestUpdateReusesAnExistingToolchainImageWithTheCurrentBaseID(t *testing.T) {
	fakes := linuxHost(t)
	responses := updateObjectResponses(t, true, "old-native", "native", "")
	responses = append(responses, updateCurrentNativeImageResponses()...)
	responses = append(responses, successfulRunningUpdateResponses()...)
	scriptUpdate(t, fakes, false, responses)
	stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, call := range fakes.Calls("podman") {
		if call.Args[0] == "build" {
			t.Fatalf("rebuilt current images: %v", call.Args)
		}
	}
	create := assertUpdateCreatedFromImage(t, fakes, "current-native")
	if !slices.Contains(create, "io.github.sandboxed-agents.toolchains=native") {
		t.Fatalf("replacement lost its toolchain set: %v", create)
	}
	assertNoSSH(t, fakes)
}

func assertUpdateBuildsBeforeReplacement(t *testing.T, fakes *testutil.FakePrograms, hash string, selections ...string) {
	t.Helper()
	var builds [][]string
	renamed := false
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		switch args[0] {
		case "build":
			if renamed {
				t.Fatalf("image build followed container rename: %v", args)
			}
			builds = append(builds, args)
		case "rename":
			renamed = true
		}
	}
	if !renamed || len(builds) != len(selections) {
		t.Fatalf("renamed=%t builds=%v want selections=%v", renamed, builds, selections)
	}
	for index, selection := range selections {
		tag, baseID := updateBuildReferences(hash, selection)
		assertImageBuild(t, builds[index], hash, tag, selection, baseID)
	}
}

func updateBuildReferences(hash, selection string) (string, string) {
	if selection == "" {
		return "localhost/sandboxed-agents:base-" + hash, ""
	}
	return "localhost/sandboxed-agents:toolchains-native-" + hash, "current-base"
}

func TestUpdateRebuildsAStaleToolchainImageEvenWhenItsIDMatchesTheSandbox(t *testing.T) {
	fakes := linuxHost(t)
	hash := imageBuildAssetHash(t)
	stale := []testutil.Response{
		{}, {Stdout: `[{"Id":"current-base"}]`},
		{}, {Stdout: `[{"Id":"old-native","Labels":{"io.github.sandboxed-agents.base-image":"old-base"}}]`},
	}
	responses := updateObjectResponses(t, true, "old-native", "native", "")
	responses = append(responses, stale...)
	responses = append(responses, stale...)
	responses = append(responses, testutil.Response{}, updateCurrentNativeImageResponses()[3])
	responses = append(responses, updateCurrentNativeImageResponses()...)
	responses = append(responses, successfulRunningUpdateResponses()...)
	scriptUpdate(t, fakes, false, responses)
	stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
		t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
	}
	assertUpdateBuildsBeforeReplacement(t, fakes, hash, "native")
	assertUpdateCreatedFromImage(t, fakes, "current-native")
	assertNoSSH(t, fakes)
}

func TestUpdateBuildsMissingImagesBeforeReplacingTheSandbox(t *testing.T) {
	for _, scenario := range []struct {
		name, selection, image string
		initial, ensure        []testutil.Response
		builds                 []string
	}{
		{
			name: "base", image: "current-base",
			initial: []testutil.Response{{ExitCode: 1}},
			ensure:  []testutil.Response{{ExitCode: 1}, {}},
			builds:  []string{""},
		},
		{
			name: "toolchain", selection: "native", image: "current-native",
			initial: []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}},
			ensure: []testutil.Response{
				{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}, {},
				{Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`},
			},
			builds: []string{"native"},
		},
		{
			name: "base-and-toolchain", selection: "native", image: "current-native",
			initial: []testutil.Response{{ExitCode: 1}},
			ensure: []testutil.Response{
				{ExitCode: 1}, {}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}, {},
				{Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`},
			},
			builds: []string{"", "native"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fakes := linuxHost(t)
			hash := imageBuildAssetHash(t)
			responses := updateObjectResponses(t, true, "old-image", scenario.selection, "")
			responses = append(responses, scenario.initial...)
			responses = append(responses, scenario.ensure...)
			current := updateCurrentNativeImageResponses()
			if scenario.selection == "" {
				current = current[:2]
			}
			responses = append(responses, current...)
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertUpdateBuildsBeforeReplacement(t, fakes, hash, scenario.builds...)
			assertUpdateCreatedFromImage(t, fakes, scenario.image)
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpdateImageBuildFailuresLeaveTheSandboxUnchanged(t *testing.T) {
	for _, selection := range []string{"", "native"} {
		name := "base"
		if selection != "" {
			name = "toolchain"
		}
		t.Run(name, func(t *testing.T) {
			fakes := linuxHost(t)
			hash := imageBuildAssetHash(t)
			missing := []testutil.Response{{ExitCode: 1}}
			if selection != "" {
				missing = []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}}
			}
			responses := updateObjectResponses(t, true, "old-image", selection, "")
			responses = append(responses, missing...)
			responses = append(responses, missing...)
			responses = append(responses, testutil.Response{ExitCode: 42})
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "exit status 42") || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var builds [][]string
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Contains([]string{"rename", "create", "stop", "start", "rm", "exec"}, args[0]) {
					t.Fatalf("build failure changed or probed the sandbox: %v", args)
				}
				if args[0] == "build" {
					builds = append(builds, args)
				}
				if args[0] == "volume" && args[1] != "exists" && args[1] != "inspect" {
					t.Fatalf("build failure changed a volume: %v", args)
				}
			}
			if len(builds) != 1 {
				t.Fatalf("failed builds=%v", builds)
			}
			tag, baseID := updateBuildReferences(hash, selection)
			assertImageBuild(t, builds[0], hash, tag, selection, baseID)
			if len(fakes.Calls("ssh-keyscan")) != 0 {
				t.Fatal("build failure probed SSH readiness")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpdateKeepsBoundWorkspaceSourcesAndRetainedVolumes(t *testing.T) {
	for _, workspace := range []struct{ name, source, mount string }{
		{"plain", "/host/project", "type=bind,source=/host/project,target=/workspace"},
		{"comma", "/host/project,files", `type=bind,"source=/host/project,files",target=/workspace`},
		{"CRLF", "/host/project\r\nfiles", "type=bind,\"source=/host/project\r\r\nfiles\",target=/workspace"},
		{"comma-quote-CRLF", "/host/project,\"\r\nfiles", "type=bind,\"source=/host/project,\"\"\r\r\nfiles\",target=/workspace"},
	} {
		for _, retained := range []bool{false, true} {
			name := workspace.name + "/without-unused-workspace-volume"
			if retained {
				name = workspace.name + "/with-unused-workspace-volume"
			}
			t.Run(name, func(t *testing.T) {
				fakes := linuxHost(t)
				responses := updateObjectResponses(t, true, "old-image", "", workspace.source)
				if retained {
					responses[3] = testutil.Response{}
					responses = slices.Insert(responses, 4, testutil.Response{Stdout: `[{"Name":"sandboxed-agents.default.agent01.workspace","Labels":{"io.github.sandboxed-agents.owner":"default"}}]`})
				}
				responses = append(responses, updateCurrentNativeImageResponses()[:2]...)
				responses = append(responses, successfulRunningUpdateResponses()...)
				scriptUpdate(t, fakes, false, responses)
				stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01")
				if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				create := assertUpdateCreatedFromImage(t, fakes, "current-base")
				var mounts []string
				for index, arg := range create {
					if arg == "--mount" {
						mounts = append(mounts, create[index+1])
					}
				}
				want := []string{
					workspace.mount,
					"type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent",
					"type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh",
				}
				if !reflect.DeepEqual(mounts, want) || !slices.Contains(create, "io.github.sandboxed-agents.workspace-kind=bind") {
					t.Fatalf("replacement changed recorded mounts: %v", create)
				}
				decoded, err := csv.NewReader(strings.NewReader(mounts[0])).Read()
				if err != nil || !reflect.DeepEqual(decoded, []string{"type=bind", "source=" + workspace.source, "target=/workspace"}) {
					t.Fatalf("CSV changed the bind source: %q error=%v", decoded, err)
				}
				for _, call := range fakes.Calls("podman") {
					args := podmanUpdateArgs(call.Args)
					if args[0] == "build" || args[0] == "volume" && args[1] != "exists" && args[1] != "inspect" {
						t.Fatalf("replacement changed an image or preserved volume: %v", args)
					}
					if args[0] == "rm" {
						for _, arg := range args {
							if arg == "-v" || strings.HasPrefix(arg, "--volumes") {
								t.Fatalf("backup removal could remove retained volumes: %v", args)
							}
						}
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}
