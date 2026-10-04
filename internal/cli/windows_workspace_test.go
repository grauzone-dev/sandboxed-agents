package cli_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestWindowsUpRefusesACustomAutomountRootAfterPreflight(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	workspace := workspaceFixture(t)
	t.Setenv("LOCALAPPDATA", filepath.Join(workspaceFixture(t), "state"))
	responses := healthyWindowsPodman()
	responses[6].Stdout = "[automount]\nroot=/windows-drives/\n"
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
	if status == 0 || !strings.Contains(stderr, "automount") || !strings.Contains(stderr, "/windows-drives/") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 7 {
		t.Fatalf("workspace refusal did not follow preflight: %v", calls)
	}
	assertReadOnlyPodmanCalls(t, calls)
	assertNoSSH(t, fakes)
}

func TestWindowsUpReportsAStoppedMachineBeforeWorkspaceTranslation(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	workspace := workspaceFixture(t)
	t.Setenv("LOCALAPPDATA", filepath.Join(workspaceFixture(t), "state"))
	fakes.Script("podman", stoppedWindowsPodman()[:3]...)
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
	if status == 0 || !strings.Contains(stdout, "No running Podman machine") || !strings.Contains(stderr, "prerequisites") || strings.Contains(stderr, "automount") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 3 {
		t.Fatalf("stopped machine was queried or started: %v", calls)
	}
	assertReadOnlyPodmanCalls(t, calls)
	assertNoSSH(t, fakes)
}

func TestWindowsUpRefusesUnknownOrDisabledAutomountBeforeSandboxLookups(t *testing.T) {
	for _, config := range []testutil.Response{{ExitCode: 1, Stderr: "Permission denied"}, {Stdout: "[automount]\nenabled=false\n"}, {Stdout: "[automount]\nroot=relative\n"}} {
		t.Run(fmt.Sprintf("status-%d-config-%q", config.ExitCode, config.Stdout), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			workspace := workspaceFixture(t)
			t.Setenv("LOCALAPPDATA", filepath.Join(workspaceFixture(t), "state"))
			responses := healthyWindowsPodman()
			responses[6] = config
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
			if status == 0 || !strings.Contains(stderr, "automount") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != 7 {
				t.Fatalf("unknown automount reached sandbox lookups: %v", calls)
			}
			assertReadOnlyPodmanCalls(t, calls)
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsUpTranslatesResolvedWorkspacesOnTheCheckedMachine(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, spelling := range []string{"absolute", "relative", "uppercase", "symlink", "junction", "nested-symlink", "nested-junction"} {
		t.Run(spelling, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			root := workspaceFixture(t)
			configureWindowsWorkspacePaths(t, root)
			directory := filepath.Join(root, "project, with spaces")
			if err := os.MkdirAll(filepath.Join(directory, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			workspace := directory
			resolved := directory
			switch spelling {
			case "relative":
				workspace = ".\\" + filepath.Base(directory)
			case "uppercase":
				workspace = strings.ToUpper(directory)
			case "symlink", "junction", "nested-symlink", "nested-junction":
				kind := strings.TrimPrefix(spelling, "nested-")
				workspace = filepath.Join(root, "alias")
				makeWindowsWorkspaceAlias(t, kind, directory, workspace)
				if strings.HasPrefix(spelling, "nested-") {
					workspace = filepath.Join(workspace, "nested")
					resolved = filepath.Join(directory, "nested")
				}
			}
			t.Setenv("CONTAINER_CONNECTION", "unchecked-default")
			t.Setenv("CONTAINER_HOST", "ssh://unchecked-host/run/podman.sock")
			t.Setenv("CONTAINER_SSHKEY", "unchecked-key")
			responses := healthyWindowsPodman()
			responses[1].Stdout = `[{"Name":"unchecked-default","Default":false,"Running":true,"VMType":"wsl"},{"Name":"checked-machine","Default":true,"Running":true,"VMType":"wsl"}]`
			responses[2].Stdout = `[{"Name":"checked-machine","State":"running","Rootful":false}]`
			responses = append(responses, upObjectResponses(nil, false, nil, nil)[1:]...)
			responses = append(responses, make([]testutil.Response, 5)...)
			for index := range responses {
				responses[index].AbsentEnv = []string{"CONTAINER_CONNECTION", "CONTAINER_HOST", "CONTAINER_SSHKEY"}
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLIAt(t, root, "windows", "up", "agent01", workspace)
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
				t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
			}
			calls := fakes.Calls("podman")
			if len(calls) < 9 || len(calls[6].Args) < 3 || !reflect.DeepEqual(calls[6].Args[:3], []string{"machine", "ssh", "checked-machine"}) {
				t.Fatalf("automount root came from another machine: %v", calls)
			}
			operations := windowsOperationCalls(t, calls[7:], "checked-machine")
			create := operations[len(operations)-2].Args
			if create[0] != "create" {
				t.Fatalf("missing create call: %v", operations)
			}
			mounts := windowsWorkspaceMounts(t, create)
			want := [][]string{{"type=bind", "source=" + windowsWorkspaceSource(t, resolved), "target=/workspace"}, {"type=volume", "source=sandboxed-agents.default.agent01.home", "target=/home/agent"}, {"type=volume", "source=sandboxed-agents.default.agent01.ssh", "target=/etc/ssh"}}
			if !reflect.DeepEqual(mounts, want) {
				t.Fatalf("mounts=%v want=%v", mounts, want)
			}
			for _, call := range operations {
				if len(call.Args) > 1 && call.Args[0] == "volume" && call.Args[1] == "create" && strings.HasSuffix(call.Args[len(call.Args)-1], ".workspace") {
					t.Fatalf("bound workspace created a workspace volume: %v", call.Args)
				}
			}
			if !strings.Contains(strings.Join(create, "\n"), "io.github.sandboxed-agents.workspace-kind=bind") {
				t.Fatalf("missing bind workspace label: %v", create)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsUpProtectsHostPathsThroughSymlinksAndJunctionsBeforePodman(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, role := range []string{"state", "temp", "ssh"} {
		for _, relation := range []string{"equal", "inside", "contains"} {
			for _, alias := range []string{"direct", "symlink", "junction"} {
				t.Run(role+"/"+relation+"/"+alias, func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					root := workspaceFixture(t)
					protected := configureWindowsWorkspacePaths(t, root)[role]
					workspace := protected
					switch relation {
					case "inside":
						workspace = filepath.Join(protected, "project")
						if err := os.Mkdir(workspace, 0700); err != nil {
							t.Fatal(err)
						}
					case "contains":
						workspace = filepath.Dir(protected)
					}
					if alias != "direct" {
						path := filepath.Join(root, "workspace-alias")
						makeWindowsWorkspaceAlias(t, alias, workspace, path)
						workspace = path
					}
					assertWindowsProtectedWorkspace(t, fakes, workspace, protected)
				})
			}
		}
	}
}

func TestWindowsUpProtectsAliasedHostLocationsAndNestedWorkspaceAliases(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, role := range []string{"state", "temp", "ssh"} {
		for _, kind := range []string{"symlink", "junction"} {
			for _, location := range []string{"protected-alias", "nested-workspace-alias", "future-protected-alias"} {
				if role == "temp" && location == "future-protected-alias" {
					continue
				}
				t.Run(role+"/"+kind+"/"+location, func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					root := workspaceFixture(t)
					protected := configureWindowsWorkspacePaths(t, root)[role]
					project := filepath.Join(root, "project")
					if err := os.Mkdir(project, 0700); err != nil {
						t.Fatal(err)
					}
					workspace := project
					wantProtected := protected
					switch location {
					case "protected-alias":
						if err := os.Remove(protected); err != nil {
							t.Fatal(err)
						}
						makeWindowsWorkspaceAlias(t, kind, project, protected)
						wantProtected = project
					case "nested-workspace-alias":
						makeWindowsWorkspaceAlias(t, kind, protected, filepath.Join(project, "protected-alias"))
					case "future-protected-alias":
						parent := filepath.Dir(protected)
						if err := os.RemoveAll(parent); err != nil {
							t.Fatal(err)
						}
						makeWindowsWorkspaceAlias(t, kind, project, parent)
						wantProtected = filepath.Join(project, filepath.Base(protected))
					}
					assertWindowsProtectedWorkspace(t, fakes, workspace, wantProtected)
				})
			}
		}
	}
}

func TestWindowsUpNeverExposesItsExecutableAndItsAliases(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, alias := range []string{"direct-file", "direct-parent", "symlink-file", "symlink-parent", "junction-parent", "nested-symlink", "nested-junction"} {
		t.Run(alias, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			root := workspaceFixture(t)
			configureWindowsWorkspacePaths(t, root)
			executable, err := filepath.EvalSymlinks(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			workspace := executable
			switch alias {
			case "direct-parent":
				workspace = filepath.Dir(executable)
			case "symlink-file":
				workspace = filepath.Join(root, "executable-link")
				makeWindowsWorkspaceAlias(t, "symlink", executable, workspace)
			case "symlink-parent", "junction-parent":
				workspace = filepath.Join(root, "executable-parent-link")
				makeWindowsWorkspaceAlias(t, strings.TrimSuffix(alias, "-parent"), filepath.Dir(executable), workspace)
			case "nested-symlink", "nested-junction":
				workspace = filepath.Join(root, "project")
				if err := os.Mkdir(workspace, 0700); err != nil {
					t.Fatal(err)
				}
				makeWindowsWorkspaceAlias(t, strings.TrimPrefix(alias, "nested-"), filepath.Dir(executable), filepath.Join(workspace, "executable-alias"))
			}
			stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
			if status == 0 || stdout != "" || !strings.Contains(stderr, fmt.Sprintf("%q", executable)) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("executable workspace called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestWindowsUpRejectsExecutableHardlinkInsideWorkspaceBeforePodman(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	fakes := testutil.NewFakePrograms(t)
	root := workspaceFixture(t)
	configureWindowsWorkspacePaths(t, root)
	executable := filepath.Join(root, "sandboxed-agents-test.exe")
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "project")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(executable, filepath.Join(workspace, "executable-alias.exe")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestCLIProcess$", "--", "up", "agent01", workspace)
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=windows")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if _, ok := err.(*exec.ExitError); !ok {
		t.Fatalf("expected unsuccessful CLI exit: error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "protected host path") || !strings.Contains(stderr.String(), fmt.Sprintf("%q", executable)) {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("hardlinked executable workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestWindowsUpKeepsRecordedWorkspaceAndNamesWindowsPathsOnConflict(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, given := range []string{"same", "uppercase", "junction", "other"} {
		for _, running := range []bool{false, true} {
			t.Run(given+"/"+strconv.FormatBool(running), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				root := workspaceFixture(t)
				configureWindowsWorkspacePaths(t, root)
				recorded := filepath.Join(root, "recorded")
				other := filepath.Join(root, "other")
				for _, path := range []string{recorded, other} {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				workspace := recorded
				switch given {
				case "uppercase":
					workspace = strings.ToUpper(recorded)
				case "junction":
					workspace = filepath.Join(root, "alias")
					makeWindowsWorkspaceAlias(t, "junction", recorded, workspace)
				case "other":
					workspace = other
				}
				owned := "default"
				objects := upObjectResponses(&owned, running, nil, nil)
				objects[2].Stdout = fmt.Sprintf(`[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.workspace-kind":"bind","io.github.sandboxed-agents.ssh-port":%q}},"State":{"Running":%t},"Mounts":[{"Type":"bind","Source":%q,"Destination":"/workspace"}]}]`, strconv.Itoa(unusedSSHPort(t)), running, windowsWorkspaceSource(t, recorded))
				responses := append(healthyWindowsPodman(), objects[1:]...)
				responses = append(responses, testutil.Response{})
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
				calls := fakes.Calls("podman")
				if len(calls) < 7 {
					t.Fatalf("missing preflight: %v", calls)
				}
				operations := windowsOperationCalls(t, calls[7:], "podman-machine-default")
				if given == "other" {
					if status == 0 || !strings.Contains(stdout, "ok: Windows 11 x64") || strings.Contains(stdout, "Sandbox agent01 is running") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					for _, value := range []string{recorded, other, "remove agent01", "up agent01"} {
						expected := value
						if value == recorded || value == other {
							expected = fmt.Sprintf("%q", value)
						}
						if !strings.Contains(stderr, expected) {
							t.Fatalf("missing %q in conflict: %q", value, stderr)
						}
					}
					if strings.Contains(stderr, "/mnt/") {
						t.Fatalf("conflict reports machine path instead of Windows paths: %q", stderr)
					}
					assertWindowsWorkspaceLookupsOnly(t, operations)
				} else {
					if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
						t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, calls)
					}
					if running {
						assertWindowsWorkspaceLookupsOnly(t, operations)
					} else {
						if len(operations) == 0 || !reflect.DeepEqual(operations[len(operations)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
							t.Fatalf("recorded workspace was not started: %v", operations)
						}
						assertWindowsWorkspaceLookupsOnly(t, operations[:len(operations)-1])
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func assertWindowsProtectedWorkspace(t *testing.T, fakes *testutil.FakePrograms, workspace, protected string) {
	t.Helper()
	stdout, stderr, status := runCLI(t, "windows", "up", "agent01", workspace)
	if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(stderr, fmt.Sprintf("%q", protected)) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("protected workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func assertWindowsWorkspaceLookupsOnly(t *testing.T, calls []testutil.Call) {
	t.Helper()
	for _, call := range calls {
		if len(call.Args) != 3 || call.Args[0] != "container" && call.Args[0] != "volume" || call.Args[1] != "exists" && call.Args[1] != "inspect" {
			t.Fatalf("workspace conflict changed sandbox objects: %v", call.Args)
		}
	}
}

func TestWindowsUpRejectsMissingAndFileWorkspacesBeforePodman(t *testing.T) {
	requireNativeWindowsWorkspace(t)
	for _, kind := range []string{"missing", "file"} {
		for _, spelling := range []string{"absolute", "relative", "junction-parent"} {
			t.Run(kind+"/"+spelling, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				root := workspaceFixture(t)
				configureWindowsWorkspacePaths(t, root)
				path := filepath.Join(root, kind)
				if kind == "file" {
					if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				workspace := path
				if spelling == "relative" {
					workspace = ".\\" + kind
				} else if spelling == "junction-parent" {
					alias := filepath.Join(workspaceFixture(t), "alias")
					makeWindowsWorkspaceAlias(t, "junction", root, alias)
					workspace = filepath.Join(alias, kind)
				}
				stdout, stderr, status := runCLIAt(t, root, "windows", "up", "agent01", workspace)
				if status == 0 || stdout != "" || !strings.Contains(stderr, fmt.Sprintf("%q", path)) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("invalid workspace called Podman")
				}
				if kind == "missing" {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("missing workspace was created: %v", err)
					}
				} else if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
					t.Fatalf("file workspace changed: data=%q error=%v", data, err)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func requireNativeWindowsWorkspace(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("requires the native Windows filesystem")
	}
}

func configureWindowsWorkspacePaths(t *testing.T, root string) map[string]string {
	t.Helper()
	home := filepath.Join(root, "user")
	local := filepath.Join(root, "local-state")
	temp := filepath.Join(root, "build", "temp")
	paths := map[string]string{"state": filepath.Join(local, "sandboxed-agents", "group-default"), "temp": temp, "ssh": filepath.Join(home, ".ssh")}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	t.Setenv("TMPDIR", temp)
	return paths
}

func makeWindowsWorkspaceAlias(t *testing.T, kind, target, alias string) {
	t.Helper()
	if kind == "junction" {
		command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("create junction %s -> %s: %v output=%s", alias, target, err, output)
		}
		return
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
}

func windowsWorkspaceSource(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(resolved)
	if len(volume) != 2 || volume[1] != ':' {
		t.Fatalf("fixture needs a drive-letter path: %q", resolved)
	}
	return "/mnt/" + strings.ToLower(volume[:1]) + strings.ReplaceAll(strings.TrimPrefix(resolved, volume), `\`, "/")
}

func windowsWorkspaceMounts(t *testing.T, args []string) [][]string {
	t.Helper()
	var mounts [][]string
	for index, arg := range args {
		if arg == "--volume" || arg == "-v" {
			t.Fatalf("unexpected mount: %v", args)
		}
		if arg == "--mount" {
			if index+1 >= len(args) {
				t.Fatalf("missing mount argument: %v", args)
			}
			records, err := csv.NewReader(strings.NewReader(args[index+1])).ReadAll()
			if err != nil || len(records) != 1 {
				t.Fatalf("invalid Podman mount %q: %v", args[index+1], err)
			}
			mounts = append(mounts, records[0])
		}
	}
	return mounts
}
