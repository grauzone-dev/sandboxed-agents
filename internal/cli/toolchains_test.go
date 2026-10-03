package cli_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestToolchainSelectionRejectsUndeliveredNamesBeforeExternalCalls(t *testing.T) {
	for _, selection := range []string{"nosuch", "dotnet", "playwright", "azure", "none,native", "native,none", "none,none", "", "native,", ",native", " native", "native ", "native, native"} {
		for _, command := range [][]string{{"build"}, {"up", "agent01"}} {
			t.Run(strings.Join(command, " ")+"/"+selection, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, "unsupported-preflight", append(command, "--with", selection)...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "native") || !strings.Contains(stderr, "none") || !strings.Contains(stderr, "Usage:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
					t.Fatal("invalid toolchain selection called an external program")
				}
			})
		}
	}
}

func TestUpBuildsOnlyTheSelectedImageOnTheCurrentBase(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, imageState := range []string{"missing", "base missing", "stale", "current"} {
			t.Run(fixture+"/"+imageState, func(t *testing.T) {
				var fakes *testutil.FakePrograms
				var responses []testutil.Response
				preflightCalls := 1
				if fixture == "windows" {
					fakes = testutil.NewFakePrograms(t)
					responses = healthyWindowsPodman()
					preflightCalls = len(responses)
				} else {
					fakes = linuxHost(t)
					responses = []testutil.Response{{Stdout: "podman version 5.0.0\n"}}
				}
				responses = append(responses, upObjectResponses(nil, false, nil, nil)[1:]...)
				baseExists := testutil.Response{}
				if imageState == "base missing" {
					baseExists.ExitCode = 1
				}
				responses = append(responses, baseExists)
				if imageState == "base missing" {
					responses = append(responses, testutil.Response{})
				}
				responses = append(responses, testutil.Response{Stdout: `[{"Id":"sha256:current-base"}]`})
				if imageState == "missing" || imageState == "base missing" {
					responses = append(responses, testutil.Response{ExitCode: 1})
				} else {
					base := "sha256:current-base"
					if imageState == "stale" {
						base = "sha256:old-base"
					}
					responses = append(responses, testutil.Response{}, testutil.Response{Stdout: fmt.Sprintf(`[{"Id":"sha256:native","Labels":{"io.github.sandboxed-agents.base-image":%q}}]`, base)})
				}
				if imageState != "current" {
					responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"sha256:native","Labels":{"io.github.sandboxed-agents.base-image":"sha256:current-base"}}]`})
				}
				responses = append(responses, make([]testutil.Response, 5)...)
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "up", "agent01", "--with", "native,native", "--memory", "2g")
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				calls := fakes.Calls("podman")[preflightCalls:]
				if fixture == "windows" {
					calls = windowsOperationCalls(t, calls, "podman-machine-default")
				}
				var builds []testutil.Call
				for _, call := range calls {
					if call.Args[0] == "images" {
						t.Fatal("up discovered or rebuilt unrelated images")
					}
					if call.Args[0] == "build" {
						builds = append(builds, call)
						if _, err := os.Stat(call.Args[len(call.Args)-1]); !os.IsNotExist(err) {
							t.Fatalf("build context remains: %v", err)
						}
					}
				}
				wantBuilds := 1
				if imageState == "base missing" {
					wantBuilds = 2
				} else if imageState == "current" {
					wantBuilds = 0
				}
				if len(builds) != wantBuilds {
					t.Fatalf("builds=%v want=%d", builds, wantBuilds)
				}
				if imageState == "base missing" && !strings.Contains(strings.Join(builds[0].Args, " "), "--tag localhost/sandboxed-agents:base-fixture-assets") {
					t.Fatalf("first build is not base: %v", builds)
				}
				if len(builds) > 0 && !strings.Contains(strings.Join(builds[len(builds)-1].Args, " "), "BASE_IMAGE=sha256:current-base") {
					t.Fatalf("toolchain build is not pinned to current base: %v", builds)
				}
				create := calls[len(calls)-2].Args
				if create[len(create)-1] != "sha256:native" || !strings.Contains(strings.Join(create, " "), "--label io.github.sandboxed-agents.toolchains=native") || !strings.Contains(strings.Join(create, " "), "--memory=2147483648") {
					t.Fatalf("container arguments=%v", create)
				}
			})
		}
	}
}

func TestUpWithNoneCreatesASandboxFromTheBaseImage(t *testing.T) {
	fakes := linuxHost(t)
	responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--with", "none")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	create := calls[len(calls)-2].Args
	if create[len(create)-1] != "localhost/sandboxed-agents:base-fixture-assets" || !strings.Contains(strings.Join(create, " "), "--label io.github.sandboxed-agents.toolchains=") {
		t.Fatalf("container arguments=%v", create)
	}
}

func TestUpCreatesFromTheInspectedNativeImageID(t *testing.T) {
	fakes := linuxHost(t)
	responses := append(upObjectResponses(nil, false, nil, nil),
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:current-base"}]`},
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:validated-native","Labels":{"io.github.sandboxed-agents.base-image":"sha256:current-base"}}]`},
	)
	responses = append(responses, make([]testutil.Response, 5)...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--with", "native")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	create := calls[len(calls)-2].Args
	if create[len(create)-1] != "sha256:validated-native" {
		t.Fatalf("create did not pin the inspected image: %v", create)
	}
}

func TestUpRefusesAToolchainImageReplacedDuringItsBuild(t *testing.T) {
	fakes := linuxHost(t)
	responses := append(upObjectResponses(nil, false, nil, nil),
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:current-base"}]`},
		testutil.Response{ExitCode: 1}, testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:replaced-native","Labels":{"io.github.sandboxed-agents.base-image":"sha256:old-base"}}]`},
	)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--with", "native")
	if status == 0 || !strings.Contains(stderr, "changed during its build") || strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, call := range fakes.Calls("podman") {
		if call.Args[0] == "create" || call.Args[0] == "start" || (call.Args[0] == "volume" && call.Args[1] == "create") {
			t.Fatalf("provisioned from a replaced toolchain image: %v", call.Args)
		}
	}
}

func TestUpKeepsRecordedToolchainsUnlessAnExplicitSelectionConflicts(t *testing.T) {
	for _, example := range []struct {
		recorded string
		args     []string
		conflict bool
	}{
		{"native", nil, false},
		{"native", []string{"--with", "native"}, false},
		{"native", []string{"--with=native,native"}, false},
		{"native", []string{"--with", "none"}, true},
		{"", []string{"--with", "native"}, true},
		{"", []string{"--with", "none"}, false},
	} {
		t.Run(example.recorded+"/"+strings.Join(example.args, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			owner := "default"
			responses := upObjectResponses(&owner, false, nil, nil)
			responses = withRecordedContainerLabels(t, responses, map[string]string{"toolchains": example.recorded, "ssh-port": fmt.Sprint(unusedSSHPort(t))})
			if !example.conflict {
				responses = append(responses, testutil.Response{})
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", append([]string{"up", "agent01"}, example.args...)...)
			if example.conflict {
				if status == 0 || !strings.Contains(stderr, "recorded") || !strings.Contains(stderr, "given") || !strings.Contains(stderr, "native") || !strings.Contains(stderr, "none") || !strings.Contains(stderr, "update agent01 --with") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertPodmanReadOnly(t, fakes)
			} else {
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")
				if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
					t.Fatalf("calls=%v", calls)
				}
				for _, call := range calls {
					if call.Args[0] == "build" || call.Args[0] == "image" || call.Args[0] == "create" {
						t.Fatalf("existing sandbox changed image: %v", calls)
					}
				}
			}
		})
	}
}

func TestUpChecksOwnerAndInterruptedUpdateBeforeToolchainConflicts(t *testing.T) {
	for _, failure := range []string{"owner", "backup"} {
		t.Run(failure, func(t *testing.T) {
			fakes := linuxHost(t)
			owner := "default"
			var backup *string
			if failure == "owner" {
				owner = "other"
			} else {
				backup = &owner
			}
			fakes.Script("podman", upObjectResponses(&owner, false, nil, backup)...)
			_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", "--with", "native")
			if status == 0 || strings.Contains(stderr, "toolchain set conflict") {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			if failure == "owner" && !strings.Contains(stderr, "owner conflict") {
				t.Fatal(stderr)
			}
			if failure == "backup" && !strings.Contains(stderr, "update agent01") {
				t.Fatal(stderr)
			}
			assertPodmanReadOnly(t, fakes)
		})
	}
}

func TestListReadsTheToolchainSetOfTheSandbox(t *testing.T) {
	for _, backupOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(backupOnly), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			container, backup := &owner, (*string)(nil)
			state := "running"
			if backupOnly {
				container, backup = nil, &owner
				state = "update interrupted"
			}
			responses := listOneSandboxResponses("default", "agent01", container, true, map[string]string{"workspace": owner}, backup)
			for index := range responses {
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"io.github.sandboxed-agents.workspace-kind":"volume"`, `"io.github.sandboxed-agents.workspace-kind":"volume","io.github.sandboxed-agents.toolchains":"native"`)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 "+state+" volume - native -") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}
