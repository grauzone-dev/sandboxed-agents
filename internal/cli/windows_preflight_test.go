package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func healthyWindowsPodman() []testutil.Response {
	return []testutil.Response{
		{Stdout: "podman version 6.0.0\n"},
		{Stdout: `[{"Name":"podman-machine-default","Default":true,"Running":true,"VMType":"wsl"}]`},
		{Stdout: `[{"Name":"podman-machine-default","State":"running","Rootful":false}]`},
		{Stdout: `{"Client":{"Version":"6.0.0"},"Server":{"Version":"6.0.0"}}`},
		{Stdout: `{"host":{"cgroupVersion":"v2","cgroupControllers":["cpu","memory","pids"],"security":{"rootless":true}}}`},
		{Stdout: "delegated\n"},
		{Stdout: "[automount]\nroot=/mnt/\n"},
	}
}

func TestCheckReportsWindowsPrerequisites(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", healthyWindowsPodman()...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, prerequisite := range []string{"Windows 11 x64", "Podman client", "Podman machine runs Podman", "machine is running", "WSL2", "rootless", "cgroups v2", "ssh is available", "ssh-keygen is available", "Windows drives"} {
		if !strings.Contains(stdout, prerequisite) {
			t.Errorf("missing %q from %q", prerequisite, stdout)
		}
	}
	if strings.Contains(stdout, "missing:") {
		t.Fatalf("healthy host reported missing prerequisites: %s", stdout)
	}
	if len(fakes.Calls("ssh")) != 0 || len(fakes.Calls("ssh-keygen")) != 0 {
		t.Fatal("check executed OpenSSH instead of checking availability")
	}
}

func TestCheckRejectsMalformedPodmanVersions(t *testing.T) {
	for _, version := range []string{"6.bad.0", "6.0", "6.0.0.1", "6.+1.0", "6.0.0-dev"} {
		t.Run(version, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := healthyWindowsPodman()
			responses[0].Stdout = "podman version " + version + "\n"
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "windows", "check")
			if status == 0 || !strings.Contains(stdout, "unknown: Could not read the Podman client version") || !strings.Contains(stderr, "prerequisites") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
		})
	}
}

func TestCheckReportsEachMissingWindowsPrerequisite(t *testing.T) {
	missingMessages := map[string]string{}
	cases := []struct {
		name          string
		fixture       string
		change        func([]testutil.Response)
		remove        string
		missing       string
		skipAutomount bool
	}{
		{name: "Windows 10", fixture: "windows-10", missing: "Windows 11 x64"},
		{name: "ARM64", fixture: "windows-arm64", missing: "Windows 11 x64"},
		{name: "Windows Server", fixture: "windows-server", missing: "Windows 11 x64"},
		{name: "unknown host", fixture: "windows-unknown", missing: "Windows 11 x64"},
		{name: "Podman absent", remove: "podman", missing: "Podman client 5.0.0"},
		{name: "old client", change: func(r []testutil.Response) { r[0].Stdout = "podman version 4.9.9\n" }, missing: "Podman client 5.0.0"},
		{name: "old machine", change: func(r []testutil.Response) { r[3].Stdout = `{"Server":{"Version":"4.9.9"}}` }, missing: "Podman machine must run Podman 5.0.0"},
		{name: "stopped machine", skipAutomount: true, change: func(r []testutil.Response) {
			copy(r, stoppedWindowsPodman())
		}, missing: "No running Podman machine"},
		{name: "missing machine", skipAutomount: true, change: func(r []testutil.Response) { r[1].Stdout = `[]` }, missing: "No running Podman machine"},
		{name: "non WSL machine", change: func(r []testutil.Response) {
			r[1].Stdout = `[{"Name":"podman-machine-default","Default":true,"Running":true,"VMType":"hyperv"}]`
		}, missing: "Podman machine must use WSL2"},
		{name: "rootful machine", change: func(r []testutil.Response) {
			r[2].Stdout = `[{"Name":"podman-machine-default","State":"running","Rootful":true}]`
		}, missing: "Podman machine must run rootless"},
		{name: "rootful service", change: func(r []testutil.Response) {
			r[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":["cpu","memory","pids"],"security":{"rootless":false}}}`
		}, missing: "Podman machine must run rootless"},
		{name: "cgroups v1", change: func(r []testutil.Response) {
			r[4].Stdout = `{"host":{"cgroupVersion":"v1","cgroupControllers":["cpu","memory","pids"],"security":{"rootless":true}}}`
		}, missing: "Podman machine must use cgroups v2"},
		{name: "cpu delegation", change: func(r []testutil.Response) {
			r[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":["memory","pids"],"security":{"rootless":true}}}`
		}, missing: "Podman machine must use cgroups v2"},
		{name: "memory delegation", change: func(r []testutil.Response) {
			r[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":["cpu","pids"],"security":{"rootless":true}}}`
		}, missing: "Podman machine must use cgroups v2"},
		{name: "pids delegation", change: func(r []testutil.Response) {
			r[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":["cpu","memory"],"security":{"rootless":true}}}`
		}, missing: "Podman machine must use cgroups v2"},
		{name: "ssh absent", remove: "ssh", missing: "ssh was not found on PATH"},
		{name: "ssh-keygen absent", remove: "ssh-keygen", missing: "ssh-keygen was not found on PATH"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			t.Setenv("PATH", filepath.Dir(fakes.Podman))
			responses := healthyWindowsPodman()
			if test.change != nil {
				test.change(responses)
			}
			fakes.Script("podman", responses...)
			if test.remove != "" {
				programs := map[string]string{"podman": fakes.Podman, "ssh": fakes.SSH, "ssh-keygen": fakes.SSHKeygen}
				if err := os.Remove(programs[test.remove]); err != nil {
					t.Fatal(err)
				}
			}
			fixture := test.fixture
			if fixture == "" {
				fixture = "windows"
			}
			stdout, stderr, status := runCLI(t, fixture, "check")
			if status == 0 || !strings.Contains(stdout, "missing: "+test.missing) || !strings.Contains(stderr, "prerequisites") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if count := strings.Count(stdout, "missing:"); count != 1 {
				t.Fatalf("host lacks exactly one prerequisite but reported %d: %s", count, stdout)
			}
			for _, line := range strings.Split(stdout, "\n") {
				if strings.HasPrefix(line, "missing:") {
					missingMessages[test.missing] = line
				}
			}
			assertReadOnlyPodmanCalls(t, fakes.Calls("podman"))
			if test.skipAutomount {
				for _, call := range fakes.Calls("podman") {
					if len(call.Args) >= 2 && call.Args[0] == "machine" && call.Args[1] == "ssh" {
						t.Fatalf("read automount on unavailable machine: %v", call.Args)
					}
				}
			}
		})
	}
	seen := map[string]string{}
	for prerequisite, message := range missingMessages {
		if previous, duplicate := seen[message]; duplicate {
			t.Errorf("%q and %q have identical missing-prerequisite message: %s", previous, prerequisite, message)
		}
		seen[message] = prerequisite
	}
	if len(missingMessages) != 9 {
		t.Fatalf("covered %d prerequisite messages; want 9", len(missingMessages))
	}

}

func TestCheckReportsSeveralMissingPrerequisitesTogether(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	t.Setenv("PATH", filepath.Dir(fakes.Podman))
	responses := healthyWindowsPodman()
	responses[0].Stdout = "podman version 4.0.0\n"
	responses[3].Stdout = `{"Server":{"Version":"4.0.0"}}`
	responses[4].Stdout = `{"host":{"cgroupVersion":"v1","security":{"rootless":false}}}`
	fakes.Script("podman", responses...)
	if err := os.Remove(fakes.SSH); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fakes.SSHKeygen); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runCLI(t, "windows-10", "check")
	if status == 0 || stderr == "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, message := range []string{"Windows 11 x64", "Podman client 5.0.0", "Podman machine must run Podman 5.0.0", "Podman machine must run rootless", "Podman machine must use cgroups v2", "ssh was not found", "ssh-keygen was not found"} {
		if !strings.Contains(stdout, "missing: "+message) {
			t.Errorf("missing %q in %q", message, stdout)
		}
	}
}

func TestCheckUsesOnlyReadOnlyPodmanCalls(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", healthyWindowsPodman()...)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := []testutil.Call{
		{Args: []string{"--version"}},
		{Args: []string{"machine", "list", "--format", "json"}},
		{Args: []string{"machine", "inspect", "podman-machine-default"}},
		{Args: []string{"--connection", "podman-machine-default", "version", "--format", "json"}},
		{Args: []string{"--connection", "podman-machine-default", "info", "--format", "json"}},
		{Args: []string{"machine", "ssh", "podman-machine-default", "sh", "-c", "'if [ -e /etc/wsl.conf ] || [ -L /etc/wsl.conf ]; then cat /etc/wsl.conf; fi'"}},
	}
	got := fakes.Calls("podman")
	if len(got) != len(want)+1 {
		t.Fatalf("calls=%v want one additional delegation probe", got)
	}
	delegation := got[5]
	if len(delegation.Args) != 6 || !reflect.DeepEqual(delegation.Args[:5], []string{"machine", "ssh", "podman-machine-default", "sh", "-c"}) {
		t.Fatalf("delegation probe=%v", delegation)
	}
	script := delegation.Args[5]
	for _, query := range []string{"-p Delegate --value", "-p ActiveState --value", "-p ControlGroup --value", "cgroup.procs cgroup.threads cgroup.subtree_control", "cgroup.controllers"} {
		if !strings.Contains(script, query) {
			t.Errorf("delegation probe lacks read-only query %q: %s", query, script)
		}
	}
	for _, mutation := range []string{"mkdir", "chmod", "chown", "systemctl start", "systemctl set-property", "sudo", " > "} {
		if strings.Contains(script, mutation) {
			t.Errorf("delegation probe mutates guest with %q", mutation)
		}
	}
	withoutDelegation := append(append([]testutil.Call{}, got[:5]...), got[6:]...)
	if !reflect.DeepEqual(withoutDelegation, want) {
		t.Fatalf("calls=%v want=%v", withoutDelegation, want)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("check changed host files: %v", entries)
	}
	if len(fakes.Calls("ssh")) != 0 || len(fakes.Calls("ssh-keygen")) != 0 {
		t.Fatal("check ran OpenSSH")
	}
}

func TestCheckRejectsUsageBeforeReadingTheHost(t *testing.T) {
	for _, arg := range []string{"sandbox01", "--unknown"} {
		t.Run(arg, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "windows", "check", arg)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage: sandboxed-agents check") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("invalid usage called Podman")
			}
		})
	}
}

func TestCheckFailsClosedWhenHostInformationCannotBeRead(t *testing.T) {
	for _, probe := range []struct {
		index    int
		expected string
		required bool
	}{
		{0, "unknown: Could not read the Podman client version", true},
		{1, "unknown: Could not determine whether a Podman machine", true},
		{2, "unknown: Could not determine whether a Podman machine", true},
		{3, "unknown: Could not read the Podman version of the Podman machine", true},
		{4, "unknown: Could not determine whether cgroups v2 delegates", true},
		{5, "unknown: Could not determine whether cgroups v2 delegates", true},
		{6, "unknown: WSL automount root", false},
	} {
		for _, failure := range []string{"exit", "json"} {
			if failure == "json" && (probe.index == 0 || probe.index == 6) {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", probe.index, failure), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := healthyWindowsPodman()
				if failure == "exit" {
					responses[probe.index].ExitCode = 9
				} else {
					responses[probe.index].Stdout = "not JSON"
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "windows", "check")
				if (status != 0) != probe.required || !strings.Contains(stdout, probe.expected) || (stderr != "") != probe.required {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
			})
		}
	}
}

func TestCheckRejectsInvalidAutomountConfiguration(t *testing.T) {
	for _, config := range []string{"[automount\nroot=/custom/\n", "[automount]\nroot=relative\n", "[automount]\nroot=\n", "[automount]\nenabled=maybe\n", "[automount]\nroot=/mnt/\x00\n", "[automount]\nenabled=false\n"} {
		t.Run(config, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := healthyWindowsPodman()
			responses[6].Stdout = config
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "windows", "check")
			if status != 0 || !strings.Contains(stdout, "unknown: WSL automount root") || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
		})
	}
}

func TestCheckAcceptsTheConfirmedPodmanFloor(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[0].Stdout = "podman version 5.0.0\n"
	responses[3].Stdout = `{"Server":{"Version":"5.0.0"}}`
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status != 0 || stderr != "" || strings.Contains(stdout, "missing:") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestPreflightMakesTheAutomountRootAvailableToCallers(t *testing.T) {
	for _, test := range []struct{ name, config, root string }{
		{"unset", "", "/mnt/"},
		{"another section", "[network]\ngenerateResolvConf=false\n", "/mnt/"},
		{"default", "[automount]\nroot=/mnt/\n", "/mnt/"},
		{"custom", "[automount]\nroot=/windows-drives/\n", "/windows-drives/"},
		{"quoted custom", "[automount]\nroot = \"/windows drives/\"\n", "/windows drives/"},
		{"filesystem root", "[automount]\nroot=/\n", "/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := healthyWindowsPodman()
			responses[6].Stdout = test.config
			fakes.Script("podman", responses...)
			host := platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22000, WindowsWorkstation: true}
			report := preflight.CheckWindows(context.Background(), host, platform.Run)
			if err := report.Err(); err != nil || report.AutomountRoot != test.root || report.PodmanConnection != "podman-machine-default" {
				t.Fatalf("error=%v root=%q want=%q connection=%q", err, report.AutomountRoot, test.root, report.PodmanConnection)
			}
		})
	}
}

func TestCheckDoesNotStartOrQueryAStoppedMachine(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := stoppedWindowsPodman()[:3]
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || !strings.Contains(stdout, "No running Podman machine") || stderr == "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := []testutil.Call{
		{Args: []string{"--version"}},
		{Args: []string{"machine", "list", "--format", "json"}},
		{Args: []string{"machine", "inspect", "podman-machine-default"}},
	}
	if got := fakes.Calls("podman"); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
}

func TestCheckUsesTheSelectedMachineInsteadOfAnotherConnection(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[1].Stdout = `[{"Name":"other","Default":false,"Running":true,"VMType":"wsl"},{"Name":"chosen-machine","Default":true,"Running":true,"VMType":"wsl"}]`
	responses[2].Stdout = `[{"Name":"chosen-machine","State":"running","Rootful":false}]`
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if !reflect.DeepEqual(calls[3].Args, []string{"--connection", "chosen-machine", "version", "--format", "json"}) || !reflect.DeepEqual(calls[4].Args, []string{"--connection", "chosen-machine", "info", "--format", "json"}) || calls[5].Args[2] != "chosen-machine" || calls[6].Args[2] != "chosen-machine" {
		t.Fatalf("probed another machine: %v", calls)
	}
}

func TestCheckDoesNotInventMissingRequirementsForAStoppedMachine(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := stoppedWindowsPodman()[:3]
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || strings.Count(stdout, "missing:") != 1 || !strings.Contains(stdout, "unknown:") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, incorrectRemedy := range []string{"upgrade or recreate", "set --rootful=false", "enable delegation"} {
		if strings.Contains(stdout, incorrectRemedy) {
			t.Errorf("unverified remedy %q in %q", incorrectRemedy, stdout)
		}
	}
}

func TestCheckKeepsUnreadableAutomountInformationSeparateFromRequirements(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[6] = testutil.Response{ExitCode: 1, Stderr: "Permission denied"}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "unknown:") || strings.Contains(stdout, "missing:") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func assertReadOnlyPodmanCalls(t *testing.T, calls []testutil.Call) {
	t.Helper()
	for _, call := range calls {
		args := call.Args
		if reflect.DeepEqual(args, []string{"--version"}) {
			continue
		}
		if len(args) >= 2 && args[0] == "machine" && (args[1] == "list" || args[1] == "inspect" || args[1] == "ssh") {
			continue
		}
		if len(args) >= 3 && args[0] == "--connection" && (args[2] == "version" || args[2] == "info") {
			continue
		}
		t.Fatalf("check changed host with Podman: %v", args)
	}
}

func TestPreflightReportsAnExpiredPodmanCheck(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", healthyWindowsPodman()...)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	host := platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22000, WindowsWorkstation: true}
	report := preflight.CheckWindows(ctx, host, platform.Run)
	if err := report.Err(); err == nil || !strings.Contains(err.Error(), "timed out waiting for the Podman connection") {
		t.Fatalf("error=%v", err)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("expired check executed Podman")
	}
}

func TestCheckReportsBothStoppedAndRootfulMachine(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()[:3]
	responses[1].Stdout = `[{"Name":"podman-machine-default","Default":true,"Running":false,"VMType":"wsl"}]`
	responses[2].Stdout = `[{"Name":"podman-machine-default","State":"stopped","Rootful":true}]`
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || strings.Count(stdout, "missing:") != 2 || !strings.Contains(stdout, "missing: Podman machine must run rootless") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertReadOnlyPodmanCalls(t, fakes.Calls("podman"))
}

func TestCheckNamesMissingCgroupControllersWhenPodmanReportsNull(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":null,"security":{"rootless":true}}}`
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || !strings.Contains(stdout, "missing: Podman machine must use cgroups v2") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestCheckDoesNotPrescribeInstallationWhenInstalledPodmanProbeFails(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[0].ExitCode = 1
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || !strings.Contains(stdout, "unknown:") || strings.Contains(stdout, "install or update Podman") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func stoppedWindowsPodman() []testutil.Response {
	responses := healthyWindowsPodman()
	responses[1].Stdout = `[{"Name":"podman-machine-default","Default":true,"Running":false,"VMType":"wsl"}]`
	responses[2].Stdout = `[{"Name":"podman-machine-default","State":"stopped","Rootful":false}]`
	return responses
}

func TestCheckRequiresCgroupDelegationAndWriteAccess(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[5].Stdout = "not-delegated\n"
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || !strings.Contains(stdout, "missing: Podman machine must use cgroups v2") || strings.Contains(stdout, "ok: cgroups v2") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if strings.Count(stdout, "missing:") != 1 {
		t.Fatalf("only delegation is missing: %q", stdout)
	}
}

func TestCheckDoesNotInferMissingDelegationFromRootfulMode(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[2].Stdout = `[{"Name":"podman-machine-default","State":"running","Rootful":true}]`
	responses[4].Stdout = `{"host":{"cgroupVersion":"v2","cgroupControllers":["cpu","memory","pids"],"security":{"rootless":false}}}`
	responses = append(responses[:5], responses[6:]...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "check")
	if status == 0 || stderr == "" || strings.Count(stdout, "missing:") != 1 || !strings.Contains(stdout, "unknown: Could not determine whether cgroups v2 delegates") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if calls := fakes.Calls("podman"); len(calls) != 6 {
		t.Fatalf("rootful machine must not run a rootless delegation probe: %v", calls)
	}
}

func TestWindowsBuildRetainsPreflightAndRemovesContext(t *testing.T) {
	for _, buildStatus := range []int{0, 42} {
		t.Run(fmt.Sprint(buildStatus), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			captured := filepath.Join(t.TempDir(), "captured")
			responses := append(healthyWindowsPodman(), testutil.Response{Stdout: "build log\n", ExitCode: buildStatus, CaptureBuildContext: captured})
			wantCalls := 8
			if buildStatus == 0 {
				responses = append(responses, testutil.Response{Stdout: "[]"})
				wantCalls++
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLIAt(t, t.TempDir(), "windows-build", "build")
			calls := fakes.Calls("podman")
			if len(calls) != wantCalls {
				t.Fatalf("Windows build calls=%v stdout=%q stderr=%q", calls, stdout, stderr)
			}
			assertReadOnlyPodmanCalls(t, calls[:7])
			args := windowsOperationCalls(t, calls[7:], "podman-machine-default")[0].Args
			if len(args) < 5 || !reflect.DeepEqual(args[:4], []string{"build", "--pull=always", "--no-cache", "--tag"}) {
				t.Fatalf("Windows build args=%v", args)
			}
			version, versionErr, versionStatus := runCLI(t, "windows-build", "version")
			if versionStatus != 0 || versionErr != "" {
				t.Fatalf("version status=%d stderr=%q", versionStatus, versionErr)
			}
			hash := strings.TrimSpace(strings.Split(version, "assets ")[1])
			if len(hash) != 64 || args[4] != "localhost/sandboxed-agents:base-"+hash {
				t.Fatalf("Windows build tag=%q version=%q", args[4], version)
			}
			if buildStatus == 0 {
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Built image "+args[4]) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
			} else if status == 0 || !strings.Contains(stderr, "podman build failed with exit status 42") || strings.Contains(stdout, "Built image") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			directory := args[len(args)-1]
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("Windows build context remains: %q err=%v", directory, err)
			}
			checkBaseContext(t, captured)
		})
	}
}

func TestWindowsBuildStopsWhenPreflightFails(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := healthyWindowsPodman()
	responses[0].Stdout = "podman version 4.9.9\n"
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows-build", "build")
	if status == 0 || stderr == "" || !strings.Contains(stdout, "missing: Podman client 5.0.0") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 7 {
		t.Fatalf("failed preflight build calls=%v", calls)
	}
	assertReadOnlyPodmanCalls(t, calls)
}

func TestWindowsUpRetainsSandboxFlowAndPlatformPreflight(t *testing.T) {
	for _, missingImage := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-image-%t", missingImage), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			version, stderr, status := runCLI(t, "windows-build", "version")
			if status != 0 || stderr != "" {
				t.Fatalf("version status=%d stderr=%q", status, stderr)
			}
			hash := strings.TrimSpace(strings.Split(version, "assets ")[1])
			tag := "localhost/sandboxed-agents:base-" + hash
			responses := append(healthyWindowsPodman(), upObjectResponses(nil, false, nil, nil)[1:]...)
			captured := filepath.Join(t.TempDir(), "context")
			if missingImage {
				responses = append(responses, testutil.Response{ExitCode: 1}, testutil.Response{CaptureBuildContext: captured})
			} else {
				responses = append(responses, testutil.Response{})
			}
			responses = append(responses, make([]testutil.Response, 5)...)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLIAt(t, t.TempDir(), "windows-build", "up", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running.") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			wantCount := 18
			if missingImage {
				wantCount++
			}
			if len(calls) != wantCount {
				t.Fatalf("calls=%v want count=%d", calls, wantCount)
			}
			assertReadOnlyPodmanCalls(t, calls[:7])
			calls = append(calls[:7:7], windowsOperationCalls(t, calls[7:], "podman-machine-default")...)
			wantLookups := []testutil.Call{
				{Args: []string{"container", "exists", "sandboxed-agents.default.agent01"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.workspace"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.home"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.ssh"}},
				{Args: []string{"container", "exists", "sandboxed-agents-backup.default.agent01"}},
				{Args: []string{"image", "exists", tag}},
			}
			if !reflect.DeepEqual(calls[7:13], wantLookups) {
				t.Fatalf("lookups=%v want=%v", calls[7:13], wantLookups)
			}
			if missingImage {
				args := calls[13].Args
				if len(args) < 5 || !reflect.DeepEqual(args[:5], []string{"build", "--pull=always", "--no-cache", "--tag", tag}) {
					t.Fatalf("build args=%v", args)
				}
				if _, err := os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
					t.Fatalf("build context remains: %v", err)
				}
				checkBaseContext(t, captured)
			}
			for index, role := range sandboxVolumeRoles {
				args := []string{"volume", "create", "--label", "io.github.sandboxed-agents.owner=default", "sandboxed-agents.default.agent01." + role}
				if !reflect.DeepEqual(calls[len(calls)-5+index].Args, args) {
					t.Fatalf("volume args=%v want=%v", calls[len(calls)-5+index].Args, args)
				}
			}
			create := calls[len(calls)-2].Args
			if len(create) < 4 || !reflect.DeepEqual(create[:3], []string{"create", "--name", "sandboxed-agents.default.agent01"}) || create[len(create)-1] != tag {
				t.Fatalf("create args=%v", create)
			}
			if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
				t.Fatalf("start args=%v", calls[len(calls)-1].Args)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsUpStopsBeforeSandboxChecksWhenPreflightFails(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown-%t", unknown), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := healthyWindowsPodman()
			responses[0].Stdout = "podman version 4.9.9\n"
			want := "missing: Podman client 5.0.0"
			if unknown {
				responses[0].ExitCode = 42
				want = "unknown: Could not read the Podman client version"
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "windows", "up", "agent01")
			if status == 0 || stderr == "" || !strings.Contains(stdout, want) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != 7 {
				t.Fatalf("failed preflight calls=%v", calls)
			}
			assertReadOnlyPodmanCalls(t, calls)
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsUpRejectsUsageBeforePreflight(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"up", ".invalid"}, {"up", "agent01", "--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "windows-arm64", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("invalid usage ran Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}
