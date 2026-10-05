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
				given, recorded, image string
				options                []string
			}{
				{"dotnet", "dotnet", "current-dotnet", []string{"--with", "dotnet"}},
				{"none", "", "current-base", []string{"--with=none"}},
				{"native,dotnet,native", "dotnet,native", "current-dotnet-native", []string{"--with=native,dotnet,native"}},
			} {
				t.Run(host.name+"/"+workspace+"/"+selection.given, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					responses := updateObjectResponses(t, true, "current-native", "native", workspace)
					responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-base"}]`})
					if selection.recorded != "" {
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
						"io.github.sandboxed-agents.toolchains=" + selection.recorded, "io.github.sandboxed-agents.owner=default",
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
					for _, call := range fakes.Calls("podman") {
						args := podmanUpdateArgs(call.Args)
						if args[0] == "build" {
							t.Fatalf("rebuilt an existing current image: %v", args)
						}
						if args[0] == "image" && strings.Contains(strings.Join(args, " "), "toolchains-native-") {
							t.Fatalf("selected the recorded set instead of the requested set: %v", args)
						}
					}
				})
			}
		}
	}
}

func TestUpdateRejectsInvalidToolchainOptionsBeforeHostQueries(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, example := range []struct {
			options []string
			message string
		}{
			{[]string{"agent01", "--with", "nosuch"}, `invalid toolchain "nosuch"`},
			{[]string{"missing", "--with=nosuch"}, `invalid toolchain "nosuch"`},
			{[]string{"agent01", "--with", "none,dotnet"}, "none must stand alone"},
			{[]string{"agent01", "--with="}, "invalid toolchain"},
			{[]string{"agent01", "--with"}, "missing value"},
			{[]string{"agent01", "--with=native", "--with", "dotnet"}, "duplicate option"},
			{[]string{"--all", "--with", "native"}, "does not take --with"},
			{[]string{"--all", "--with=none"}, "does not take --with"},
			{[]string{"agent01", "--with=native", "--all"}, "exactly one target"},
		} {
			t.Run(host.name+"/"+strings.Join(example.options, " "), func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
				stdout, stderr, status := runCLI(t, fixture, append([]string{"update"}, example.options...)...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, example.message) || !strings.Contains(stderr, "Usage:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if strings.Contains(example.message, "toolchain") || strings.Contains(example.message, "stand alone") {
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
			initial: []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}},
			ensure:  []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}, {}, {Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`}},
			builds:  []string{"native"},
		},
		{
			name:    "stale-toolchain",
			initial: []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {}, {Stdout: `[{"Id":"stale-native","Labels":{"io.github.sandboxed-agents.base-image":"old-base"}}]`}},
			ensure:  []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {}, {Stdout: `[{"Id":"stale-native","Labels":{"io.github.sandboxed-agents.base-image":"old-base"}}]`}, {}, {Stdout: `[{"Id":"current-native","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`}},
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
				missing := []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {ExitCode: 1}}
				if stale {
					missing = []testutil.Response{{}, {Stdout: `[{"Id":"current-base"}]`}, {}, {Stdout: `[{"Id":"stale-native","Labels":{"io.github.sandboxed-agents.base-image":"old-base"}}]`}}
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
		for _, running := range []bool{true, false} {
			for _, failure := range []string{"create", "start"} {
				t.Run(fmt.Sprintf("%s/running-%t/%s", host.name, running, failure), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					checkSSH := installUpdateSSHFixture(t, sshDir, state)
					defer checkSSH()
					responses := updateObjectResponses(t, running, "current-dotnet", "dotnet", "")
					responses = append(responses, updateCurrentNativeImageResponses()...)
					responses = append(responses, testutil.Response{})
					want := [][]string{{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"}}
					if failure == "start" {
						responses = append(responses, testutil.Response{})
						if running {
							responses = append(responses, testutil.Response{})
							want = append(want, []string{"stop", "sandboxed-agents-backup.default.agent01"})
						}
						want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
					}
					responses = append(responses, testutil.Response{ExitCode: 42}, testutil.Response{}, testutil.Response{})
					want = append(want, []string{"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"}, []string{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"})
					if failure == "start" && running {
						responses = append(responses, testutil.Response{})
						want = append(want, []string{"start", "sandboxed-agents.default.agent01"})
					}
					scriptUpdate(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "agent01", "--with", "native")
					if status == 0 || !strings.Contains(stderr, failure) || !strings.Contains(stderr, "restored") || !strings.Contains(stderr, "42") || strings.Contains(stdout, "updated") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
						t.Fatalf("rollback did not restore the original container with its dotnet label: changes=%v want=%v", changes, want)
					}
					if create := assertUpdateCreatedFromImage(t, fakes, "current-native"); !slices.Contains(create, "io.github.sandboxed-agents.toolchains=native") {
						t.Fatalf("replacement did not request the new set: %v", create)
					}
				})
			}
		}
	}
}
