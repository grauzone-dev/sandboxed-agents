package cli_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpdateReplacesTheToolchainSetAndKeepsSandboxConfiguration(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, workspace := range []string{"", "/host/team-project"} {
			for _, selection := range []struct {
				given, selected, image, tag string
				options                     []string
			}{
				{"dotnet", "dotnet", "current-dotnet", "localhost/sandboxed-agents:toolchains-dotnet-fixture-assets", []string{"--with", "dotnet"}},
				{"none", "", "current-base", "localhost/sandboxed-agents:base-fixture-assets", []string{"--with=none"}},
				{"native,dotnet,native", "dotnet,native", "current-dotnet-native", "localhost/sandboxed-agents:toolchains-dotnet-native-fixture-assets", []string{"--with=native,dotnet,native"}},
			} {
				t.Run(host.name+"/"+workspace+"/"+selection.given, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					responses := updateObjectResponses(t, true, "current-native", "native", workspace)
					responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-base"}]`})
					if selection.selected != "" {
						responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"` + selection.image + `","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`})
					}
					responses = append(responses, successfulRunningUpdateResponses()...)
					scriptUpdate(t, fakes, host.windows, responses)
					args := append([]string{"update", "agent01"}, selection.options...)
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					create := assertUpdateCreatedFromImage(t, fakes, selection.image)
					for _, option := range []string{
						"io.github.sandboxed-agents.toolchains=" + selection.selected, "io.github.sandboxed-agents.owner=default",
						"io.github.sandboxed-agents.update-was-running=true",
						"--memory=12884901888", "--cpus=2.5", "--pids-limit=512", "--shm-size=268435456",
						"io.github.sandboxed-agents.memory=12884901888", "io.github.sandboxed-agents.cpus=2.5",
						"io.github.sandboxed-agents.pids-limit=512", "io.github.sandboxed-agents.shm-size=268435456",
						"type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent",
						"type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh",
					} {
						if !slices.Contains(create, option) {
							t.Errorf("replacement lost %q: %v", option, create)
						}
					}
					mount, kind := "type=volume,source=sandboxed-agents.default.agent01.workspace,target=/workspace", "volume"
					if workspace != "" {
						mount, kind = "type=bind,source="+workspace+",target=/workspace", "bind"
					}
					if !slices.Contains(create, mount) || !slices.Contains(create, "io.github.sandboxed-agents.workspace-kind="+kind) {
						t.Fatalf("replacement changed workspace: %v", create)
					}
					assertSSHPublication(t, create, 2300)
					assertUpdateChangesPreserveData(t, fakes)
					inspectedSelectedImage := false
					for _, call := range fakes.Calls("podman") {
						podmanArgs := podmanUpdateArgs(call.Args)
						if slices.Equal(podmanArgs, []string{"image", "inspect", selection.tag}) {
							inspectedSelectedImage = true
						}
						if podmanArgs[0] == "build" {
							t.Fatalf("rebuilt an existing current image: %v", podmanArgs)
						}
						if podmanArgs[0] == "image" && strings.Contains(strings.Join(podmanArgs, " "), "toolchains-native-") {
							t.Fatalf("selected the recorded set instead of the requested set: %v", podmanArgs)
						}
					}
					if !inspectedSelectedImage {
						t.Fatalf("did not inspect selected image %q", selection.tag)
					}
				})
			}
		}
	}
}

func TestUpdateRejectsInvalidToolchainOptionsBeforeHostQueries(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, example := range []struct {
			options          []string
			message          string
			listsValidValues bool
		}{
			{[]string{"agent01", "--with", "nosuch"}, `invalid toolchain "nosuch"`, true},
			{[]string{"missing", "--with=nosuch"}, `invalid toolchain "nosuch"`, true},
			{[]string{"agent01", "--with", "none,dotnet"}, "none must stand alone", true},
			{[]string{"agent01", "--with="}, "invalid toolchain", true},
			{[]string{"agent01", "--with"}, "missing value", true},
			{[]string{"agent01", "--with=native", "--with", "dotnet"}, "duplicate option", false},
			{[]string{"--all", "--with", "native"}, "does not take --with", false},
			{[]string{"--all", "--with=none"}, "does not take --with", false},
			{[]string{"agent01", "--with=native", "--all"}, "exactly one target", false},
		} {
			t.Run(host.name+"/"+strings.Join(example.options, " "), func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
				stdout, stderr, status := runCLI(t, fixture, append([]string{"update"}, example.options...)...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, example.message) || !strings.Contains(stderr, "Usage:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if example.listsValidValues {
					for _, name := range []string{"none", "dotnet", "azure", "native"} {
						if !strings.Contains(stderr, name) {
							t.Errorf("diagnostic omits valid value %q: %q", name, stderr)
						}
					}
				}
				for _, program := range []string{"podman", "getent", "ssh", "ssh-keygen", "ssh-keyscan"} {
					if calls := fakes.Calls(program); len(calls) != 0 {
						t.Fatalf("invalid usage ran %s: %v", program, calls)
					}
				}
			})
		}
	}
}

func TestUpdateWithTheCurrentToolchainSetTouchesNoContainer(t *testing.T) {
	for _, selection := range []struct{ recorded, given string }{
		{"native", "native,native"}, {"", "none"}, {"dotnet,native", "native,dotnet"},
	} {
		t.Run(selection.given, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := updateObjectResponses(t, true, "current-image", selection.recorded, "")
			baseID := "current-image"
			if selection.recorded != "" {
				baseID = "current-base"
			}
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"` + baseID + `"}]`})
			if selection.recorded != "" {
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-image","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`})
			}
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01", "--with", selection.given)
			if status != 0 || stderr != "" || !strings.Contains(stdout, "already up to date") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); len(changes) != 0 {
				t.Fatalf("no-op changed container: %v", changes)
			}
			for _, call := range fakes.Calls("podman") {
				if slices.Contains([]string{"build", "exec"}, call.Args[0]) {
					t.Fatalf("no-op built or probed: %v", call.Args)
				}
			}
			if len(fakes.Calls("ssh-keyscan")) != 0 {
				t.Fatal("no-op probed SSH")
			}
		})
	}
}

func TestUpdateChangesTheToolchainLabelEvenWhenTheSelectedImageIDMatches(t *testing.T) {
	fakes := linuxHost(t)
	responses := updateObjectResponses(t, true, "current-image", "native", "")
	responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-image"}]`})
	responses = append(responses, successfulRunningUpdateResponses()...)
	scriptUpdate(t, fakes, false, responses)
	stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01", "--with=none")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") || strings.Contains(stdout, "already up to date") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if create := assertUpdateCreatedFromImage(t, fakes, "current-image"); !slices.Contains(create, "io.github.sandboxed-agents.toolchains=") {
		t.Fatalf("replacement kept the old toolchain label: %v", create)
	}
}

func TestUpdateBuildsTheReplacementToolchainImageBeforeChangingTheSandbox(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		initial, ensure []testutil.Response
		builds          []string
	}{
		{
			name:    "missing-toolchain",
			initial: updateMissingNativeImageResponses(),
			ensure:  append(updateMissingNativeImageResponses(), testutil.Response{}, updateCurrentNativeImageResponse()),
			builds:  []string{"native"},
		},
		{
			name:    "stale-toolchain",
			initial: updateStaleNativeImageResponses(),
			ensure:  append(updateStaleNativeImageResponses(), testutil.Response{}, updateCurrentNativeImageResponse()),
			builds:  []string{"native"},
		},
		{
			name:    "missing-base-and-toolchain",
			initial: []testutil.Response{{ExitCode: 1}},
			ensure:  []testutil.Response{{ExitCode: 1}, {}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}, {}, {Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`}},
			builds:  []string{"", "native"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fakes := linuxHost(t)
			hash := imageBuildAssetHash(t)
			responses := updateObjectResponses(t, true, "current-dotnet", "dotnet", "")
			responses = append(responses, scenario.initial...)
			responses = append(responses, scenario.ensure...)
			responses = append(responses, updateCurrentNativeImageResponses()...)
			responses = append(responses, successfulRunningUpdateResponses()...)
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01", "--with", "native")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			assertUpdateBuildsBeforeReplacement(t, fakes, hash, scenario.builds...)
			if create := assertUpdateCreatedFromImage(t, fakes, "current-native"); !slices.Contains(create, "io.github.sandboxed-agents.toolchains=native") {
				t.Fatalf("replacement retained the previous toolchain set: %v", create)
			}
			assertUpdateChangesPreserveData(t, fakes)
		})
	}
}

func TestUpdateToolchainBuildFailureKeepsTheOriginalSandbox(t *testing.T) {
	for _, running := range []bool{true, false} {
		for _, stale := range []bool{true, false} {
			t.Run(fmt.Sprintf("running-%t/stale-%t", running, stale), func(t *testing.T) {
				fakes, _, sshDir, state := sshSetupHost(t, false)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				defer checkSSH()
				hash := imageBuildAssetHash(t)
				missing := updateMissingNativeImageResponses()
				if stale {
					missing = updateStaleNativeImageResponses()
				}
				responses := updateObjectResponses(t, running, "current-dotnet", "dotnet", "")
				responses = append(responses, missing...)
				responses = append(responses, missing...)
				responses = append(responses, testutil.Response{ExitCode: 42})
				scriptUpdate(t, fakes, false, responses)
				stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01", "--with=native")
				if status == 0 || !strings.Contains(stderr, "42") || strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); len(changes) != 0 {
					t.Fatalf("build failure changed sandbox: %v", changes)
				}
				var builds [][]string
				for _, call := range fakes.Calls("podman") {
					if call.Args[0] == "exec" {
						t.Fatalf("build failure probed manager: %v", call.Args)
					}
					if call.Args[0] == "build" {
						builds = append(builds, call.Args)
					}
				}
				if len(builds) != 1 {
					t.Fatalf("builds=%v", builds)
				}
				assertImageBuild(t, builds[0], hash, "localhost/sandboxed-agents:toolchains-native-"+hash, "native", "current-base")
				if len(fakes.Calls("ssh-keyscan")) != 0 {
					t.Fatal("build failure probed SSH")
				}
			})
		}
	}
}

func TestUpdateToolchainFailureRestoresTheOriginalContainerAndRunningState(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, scenario := range []struct {
			name, failure     string
			running           bool
			successfulChanges int
			want              [][]string
		}{
			{
				name: "running/create", failure: "create", running: true, successfulChanges: 1,
				want: [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
				},
			},
			{
				name: "stopped/create", failure: "create", successfulChanges: 1,
				want: [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
				},
			},
			{
				name: "running/stop", failure: "stop", running: true, successfulChanges: 2,
				want: [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"stop", "sandboxed-agents-backup.default.agent01"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
					{"start", "sandboxed-agents.default.agent01"},
				},
			},
			{
				name: "running/start", failure: "start", running: true, successfulChanges: 3,
				want: [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"stop", "sandboxed-agents-backup.default.agent01"}, {"start", "sandboxed-agents.default.agent01"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
					{"start", "sandboxed-agents.default.agent01"},
				},
			},
			{
				name: "stopped/start", failure: "start", successfulChanges: 2,
				want: [][]string{
					{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
					{"start", "sandboxed-agents.default.agent01"},
					{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
					{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
				},
			},
		} {
			t.Run(host.name+"/"+scenario.name, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				defer checkSSH()
				responses := updateObjectResponses(t, scenario.running, "current-dotnet", "dotnet", "")
				responses = append(responses, updateCurrentNativeImageResponses()...)
				responses = append(responses, make([]testutil.Response, scenario.successfulChanges)...)
				responses = append(responses, testutil.Response{ExitCode: 42})
				responses = append(responses, make([]testutil.Response, len(scenario.want)-scenario.successfulChanges-1)...)
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--with", "native")
				if status == 0 || !strings.Contains(stderr, scenario.failure) || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "42") || strings.Contains(stdout, "updated") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, scenario.want) {
					t.Fatalf("rollback did not restore the original container with its dotnet label: changes=%v want=%v", changes, scenario.want)
				}
				if create := assertUpdateCreatedFromImage(t, fakes, "current-native"); !slices.Contains(create, "io.github.sandboxed-agents.toolchains=native") {
					t.Fatalf("replacement did not request the new set: %v", create)
				}
			})
		}
	}
}

func TestUpdateRetryKeepsTheRequestedToolchainSetWhenTheImageChanges(t *testing.T) {
	for _, selection := range []string{"native", "none"} {
		t.Run(selection, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := updateObjectResponses(t, true, "old-dotnet", "dotnet", "")
			if selection == "native" {
				missing := updateMissingNativeImageResponses()
				responses = append(responses, missing...)
				responses = append(responses, missing...)
				successfulBuild := testutil.Response{}
				builtNativeImage := updateCurrentNativeImageResponse()
				responses = append(responses, successfulBuild, builtNativeImage)
				baseImageExists := testutil.Response{}
				changedBaseImage := testutil.Response{Stdout: `[{"Id":"changed-base"}]`}
				nativeImageExists := testutil.Response{}
				nativeFromPreviousBase := updateCurrentNativeImageResponse()
				responses = append(responses, baseImageExists, changedBaseImage, nativeImageExists, nativeFromPreviousBase)
			} else {
				missingDuringPlanning := testutil.Response{ExitCode: 1}
				missingBeforeBuild := testutil.Response{ExitCode: 1}
				successfulBuild := testutil.Response{}
				missingAfterBuild := testutil.Response{ExitCode: 1}
				responses = append(responses, missingDuringPlanning, missingBeforeBuild, successfulBuild, missingAfterBuild)
			}
			scriptUpdate(t, fakes, false, responses)
			stdout, stderr, status := runCLI(t, "linux-build", "update", "agent01", "--with", selection)
			if status == 0 || !strings.Contains(stderr, "was not changed") || !strings.Contains(stderr, "retry sandboxed-agents update agent01 --with "+selection) || strings.Contains(stdout, "updated") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); len(changes) != 0 {
				t.Fatalf("image change mutated sandbox: %v", changes)
			}
		})
	}
}
