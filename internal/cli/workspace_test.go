package cli_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func workspaceFixture(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll("../../.scratch", 0700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("../../.scratch", "workspace-test-")
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	return directory
}

func TestUpBindsOneResolvedWorkspaceAndOnlyTheTwoStateVolumes(t *testing.T) {
	fakes := linuxHost(t)
	directory := workspaceFixture(t)
	responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 5)...)
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLIAt(t, filepath.Dir(directory), "linux-preflight", "up", "agent01", "./"+filepath.Base(directory))
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	var mounts, created []string
	var labels []string
	for _, call := range fakes.Calls("podman") {
		for index, arg := range call.Args {
			if arg == "--mount" {
				mounts = append(mounts, call.Args[index+1])
			}
			if arg == "--volume" || arg == "-v" {
				t.Fatalf("unexpected mount: %v", call.Args)
			}
			if arg == "--label" {
				labels = append(labels, call.Args[index+1])
			}
		}
		if len(call.Args) > 1 && call.Args[0] == "volume" && call.Args[1] == "create" {
			created = append(created, call.Args[len(call.Args)-1])
		}
	}
	wantMounts := []string{"type=bind,source=" + directory + ",target=/workspace", "type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent", "type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh"}
	if !reflect.DeepEqual(mounts, wantMounts) {
		t.Fatalf("mounts=%v want=%v", mounts, wantMounts)
	}
	if !reflect.DeepEqual(created, []string{"sandboxed-agents.default.agent01.home", "sandboxed-agents.default.agent01.ssh"}) {
		t.Fatalf("created=%v", created)
	}
	if !strings.Contains(strings.Join(labels, "\n"), "io.github.sandboxed-agents.workspace-kind=bind") {
		t.Fatalf("labels=%v", labels)
	}
	assertNoSSH(t, fakes)
}

func TestUpRejectsMissingAndFileWorkspacesBeforePreflight(t *testing.T) {
	for _, kind := range []string{"missing", "file"} {
		t.Run(kind, func(t *testing.T) {
			fakes := linuxHost(t)
			directory := workspaceFixture(t)
			path := filepath.Join(directory, kind)
			if kind == "file" {
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
			stdout, stderr, status := runCLIAt(t, directory, "linux-preflight", "up", "agent01", "./"+kind)
			if status == 0 || stdout != "" || !strings.Contains(stderr, path) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("invalid workspace called Podman")
			}
			if kind == "missing" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("missing workspace was created: %v", err)
				}
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpRejectsEveryProtectedHostPathAndItsAliasesBeforePreflight(t *testing.T) {
	for _, role := range []string{"state", "temp", "ssh"} {
		for _, relation := range []string{"equal", "inside", "contains", "workspace-alias", "protected-alias", "future"} {
			t.Run(role+"/"+relation, func(t *testing.T) {
				fakes := linuxHost(t)
				root := workspaceFixture(t)
				home := filepath.Join(root, "user")
				state := filepath.Join(root, "state")
				temp := filepath.Join(root, "build", "temp")
				for _, directory := range []string{home, state, temp} {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("HOME", home)
				t.Setenv("XDG_STATE_HOME", state)
				t.Setenv("TMPDIR", temp)
				protected := map[string]string{"state": filepath.Join(state, "sandboxed-agents", "group-default"), "temp": temp, "ssh": filepath.Join(home, ".ssh")}[role]
				if relation == "future" {
					if err := os.MkdirAll(filepath.Dir(protected), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if relation != "future" {
					if err := os.MkdirAll(protected, 0700); err != nil {
						t.Fatal(err)
					}
				}
				workspace := protected
				switch relation {
				case "inside":
					workspace = filepath.Join(protected, "project")
					if err := os.Mkdir(workspace, 0700); err != nil {
						t.Fatal(err)
					}
				case "contains", "future":
					workspace = filepath.Dir(protected)
				case "workspace-alias":
					workspace = filepath.Join(root, "alias")
					if err := os.Symlink(protected, workspace); err != nil {
						t.Fatal(err)
					}
				case "protected-alias":
					target := filepath.Join(root, "real-protected")
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(protected); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, protected); err != nil {
						t.Fatal(err)
					}
					workspace = target
				}
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
				stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", workspace)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if !strings.Contains(stderr, protected) && relation != "protected-alias" {
					t.Fatalf("missing protected path %s: %s", protected, stderr)
				}
				if relation == "protected-alias" && !strings.Contains(stderr, workspace) {
					t.Fatalf("missing resolved protected path: %s", stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("protected workspace called Podman")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpNeverExposesItsExecutableOrTheSystemTemporaryDirectory(t *testing.T) {
	for _, path := range []string{os.Args[0], filepath.Dir(os.Args[0]), os.TempDir(), filepath.Dir(os.TempDir())} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			fakes := linuxHost(t)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", path)
			if status == 0 || stdout != "" || !strings.Contains(stderr, path) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("protected workspace called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpAcceptsAbsoluteRelativeAndAliasedWorkspaces(t *testing.T) {
	for _, spelling := range []string{"absolute", "relative", "symlink", "symlink-parent", "direct-symlink"} {
		t.Run(spelling, func(t *testing.T) {
			fakes := linuxHost(t)
			root := workspaceFixture(t)
			directory := filepath.Join(root, "project")
			if err := os.MkdirAll(filepath.Join(directory, "nested"), 0700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(root, "alias")
			if err := os.Symlink(filepath.Join(directory, "nested"), alias); err != nil {
				t.Fatal(err)
			}
			workspace := directory
			switch spelling {
			case "direct-symlink":
				workspace = filepath.Join(root, "project-link")
				if err := os.Symlink(directory, workspace); err != nil {
					t.Fatal(err)
				}
			case "relative":
				workspace = "./project"
			case "symlink":
				workspace = alias + "/.."
			case "symlink-parent":
				workspace = "./alias/.."
			}
			fakes.Script("podman", append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 5)...)...)
			_, stderr, status := runCLIAt(t, root, "linux-preflight", "up", "agent01", workspace, "--cpus", "2")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			calls := fakes.Calls("podman")
			create := calls[len(calls)-2].Args
			if !strings.Contains(strings.Join(create, "\n"), "type=bind,source="+directory+",target=/workspace") {
				t.Fatalf("create=%v", create)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpLeavesAKeptWorkspaceVolumeUnusedBesideABind(t *testing.T) {
	fakes := linuxHost(t)
	directory := workspaceFixture(t)
	fakes.Script("podman", append(upObjectResponses(nil, false, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil), make([]testutil.Response, 3)...)...)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", directory)
	if status != 0 || stderr != "" || !strings.Contains(stdout, "unused") || !strings.Contains(stdout, "sandboxed-agents.default.agent01.workspace") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, call := range fakes.Calls("podman") {
		if call.Args[0] == "volume" && (call.Args[1] == "create" || call.Args[1] == "rm") {
			t.Fatalf("changed kept volumes: %v", call.Args)
		}
		for _, arg := range call.Args {
			if strings.Contains(arg, "source=sandboxed-agents.default.agent01.workspace") {
				t.Fatalf("mounted unused volume: %v", call.Args)
			}
		}
	}
	assertNoSSH(t, fakes)
}

func TestUpKeepsTheRecordedWorkspaceOnAnExistingSandbox(t *testing.T) {
	for _, kind := range []string{"bind", "volume"} {
		for _, given := range []string{"omitted", "same", "other", "alias"} {
			for _, running := range []bool{false, true} {
				t.Run(kind+"/"+given+"/"+fmt.Sprint(running), func(t *testing.T) {
					fakes := linuxHost(t)
					root := workspaceFixture(t)
					recorded := filepath.Join(root, "recorded")
					other := filepath.Join(root, "other")
					for _, directory := range []string{recorded, other} {
						if err := os.Mkdir(directory, 0700); err != nil {
							t.Fatal(err)
						}
					}
					alias := filepath.Join(root, "alias")
					if err := os.Symlink(recorded, alias); err != nil {
						t.Fatal(err)
					}
					owned := "default"
					responses := upObjectResponses(&owned, running, nil, nil)
					source := recorded
					mountType := "bind"
					if kind == "volume" {
						source = "sandboxed-agents.default.agent01.workspace"
						mountType = "volume"
					}
					responses[2].Stdout = fmt.Sprintf(`[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.workspace-kind":%q}},"State":{"Running":%t},"Mounts":[{"Type":%q,"Source":%q,"Destination":"/workspace"}]}]`, kind, running, mountType, source)
					responses = withRecordedContainerLabels(t, responses, map[string]string{"ssh-port": strconv.Itoa(unusedSSHPort(t))})
					responses = append(responses, testutil.Response{})
					fakes.Script("podman", responses...)
					args := []string{"up", "agent01"}
					workspace := map[string]string{"same": recorded, "other": other, "alias": alias}[given]
					if given != "omitted" {
						args = append(args, workspace)
					}
					stdout, stderr, status := runCLI(t, "linux-preflight", args...)
					conflict := given != "omitted" && (kind == "volume" || given == "other")
					if conflict {
						resolvedGiven := workspace
						if given == "alias" {
							resolvedGiven = recorded
						}
						for _, message := range []string{source, resolvedGiven, "remove agent01", "up agent01"} {
							if status == 0 || !strings.Contains(stderr, message) {
								t.Fatalf("missing %q status=%d stdout=%q stderr=%q", message, status, stdout, stderr)
							}
						}
						assertPodmanReadOnly(t, fakes)
					} else {
						if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						calls := fakes.Calls("podman")
						if running {
							assertPodmanReadOnly(t, fakes)
						} else if !reflect.DeepEqual(calls[len(calls)-1].Args, []string{"start", "sandboxed-agents.default.agent01"}) {
							t.Fatalf("calls=%v", calls)
						}
					}
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestUpReportsPreflightOwnershipAndInterruptedUpdateBeforeAWorkspaceConflict(t *testing.T) {
	for _, failure := range []string{"preflight", "owner", "backup"} {
		t.Run(failure, func(t *testing.T) {
			fakes := linuxHost(t)
			workspace := workspaceFixture(t)
			owned, foreign := "default", "other"
			owner := &owned
			var backup *string
			if failure == "owner" {
				owner = &foreign
				backup = &owned
			}
			if failure == "backup" {
				backup = &owned
			}
			if failure == "preflight" {
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
			}
			fakes.Script("podman", upObjectResponses(owner, false, nil, backup)...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", workspace)
			want := map[string]string{"preflight": "host prerequisites", "owner": "owner conflict", "backup": "interrupted update"}[failure]
			if status == 0 || !strings.Contains(stderr, want) || strings.Contains(stderr, "workspace does not match") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertPodmanReadOnly(t, fakes)
		})
	}
}

func TestListShowsABoundWorkspaceFromItsRecordedKind(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	responses := listOneSandboxResponses("default", "agent01", &owned, true, nil, nil)
	for index := range responses {
		responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"io.github.sandboxed-agents.workspace-kind":"volume"`, `"io.github.sandboxed-agents.workspace-kind":"bind","io.github.sandboxed-agents.ssh-port":"2300"`)
	}
	fakes.Script("podman", responses...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 running bind 2300 - - -") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertListReadOnly(t, fakes, false)
}

func TestUpQuotesWorkspacePathsInThePodmanMountGrammar(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	directory := filepath.Join(root, "project,with\"quotes\r\nand=values")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	fakes.Script("podman", append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 5)...)...)
	_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", directory)
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	calls := fakes.Calls("podman")
	create := calls[len(calls)-2].Args
	var parsed [][]string
	for index, arg := range create {
		if arg == "--mount" {
			records, err := csv.NewReader(strings.NewReader(create[index+1])).ReadAll()
			if err != nil || len(records) != 1 {
				t.Fatalf("invalid Podman mount %q: %v", create[index+1], err)
			}
			parsed = append(parsed, records[0])
		}
	}
	want := [][]string{{"type=bind", "source=" + directory, "target=/workspace"}, {"type=volume", "source=sandboxed-agents.default.agent01.home", "target=/home/agent"}, {"type=volume", "source=sandboxed-agents.default.agent01.ssh", "target=/etc/ssh"}}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("mounts=%v want=%v", parsed, want)
	}
	assertNoSSH(t, fakes)
}

func TestUpProtectsAFutureHostStateReachedThroughADanglingSymlink(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	state := filepath.Join(root, "state")
	project := filepath.Join(root, "project")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "future-state"), filepath.Join(state, "sandboxed-agents")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", state)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", project)
	if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(stderr, filepath.Join(project, "future-state")) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("protected workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpDoesNotTreatAMissingWorkspaceComponentAsAnExistingDirectory(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	stdout, stderr, status := runCLIAt(t, root, "linux-preflight", "up", "agent01", "missing/..")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "workspace") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("missing workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpProtectsAnExecutableInstalledOutsideTheTemporaryDirectory(t *testing.T) {
	for _, alias := range []string{"none", "symlink", "hardlink", "unreadable"} {
		t.Run(alias, func(t *testing.T) {
			fakes := linuxHost(t)
			root := workspaceFixture(t)
			directory := filepath.Join(root, "bin")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(directory, "sandboxed-agents")
			contents, err := os.ReadFile(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, contents, 0700); err != nil {
				t.Fatal(err)
			}
			executable := binary
			workspace := directory
			if alias == "symlink" {
				executable = filepath.Join(root, "launch-link")
				workspace = filepath.Join(root, "workspace-link")
				if err := os.Symlink(binary, executable); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(directory, workspace); err != nil {
					t.Fatal(err)
				}
			}
			if alias == "hardlink" {
				workspace = filepath.Join(root, "project")
				if err := os.Mkdir(workspace, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(binary, filepath.Join(workspace, "executable-alias")); err != nil {
					t.Fatal(err)
				}
			}
			if alias == "unreadable" {
				if err := os.Link(binary, filepath.Join(root, "outside-link")); err != nil {
					t.Fatal(err)
				}
				workspace = filepath.Join(root, "project")
				blocked := filepath.Join(workspace, "unreadable")
				if err := os.MkdirAll(blocked, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(blocked, 0000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(blocked, 0700) })
			}
			command := exec.Command(executable, "-test.run=^TestCLIProcess$", "--", "up", "agent01", workspace)
			command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE=linux-preflight")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err == nil {
				t.Fatal("bound the executable directory")
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "protected host path") || !strings.Contains(stderr.String(), binary) {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if alias == "unreadable" && !strings.Contains(stderr.String(), "permission denied") {
				t.Fatalf("missing scan failure: %s", stderr.String())
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("protected executable called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpNamesTheResolvedWorkspaceWhenAnIntermediateComponentIsAFile(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runCLIAt(t, root, "linux-preflight", "up", "agent01", "./alias/child")
	if status == 0 || stdout != "" || !strings.Contains(stderr, filepath.Join(root, "file", "child")) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("invalid workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpProtectsHostPathsReachedThroughLinuxBindMountAliases(t *testing.T) {
	for _, role := range []string{"state", "ssh", "temp", "executable"} {
		for _, relation := range []string{"equal", "inside", "contains", "protected-alias"} {
			if role == "executable" && relation == "inside" {
				continue
			}
			t.Run(role+"/"+relation, func(t *testing.T) {
				fakes := linuxHost(t)
				root := workspaceFixture(t)
				home := filepath.Join(root, "home")
				state := filepath.Join(root, "state")
				temp := filepath.Join(root, "build-temp")
				project := filepath.Join(root, "project")
				for _, directory := range []string{home, state, temp, project} {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("HOME", home)
				t.Setenv("XDG_STATE_HOME", state)
				t.Setenv("TMPDIR", temp)
				executable, err := filepath.EvalSymlinks(os.Args[0])
				if err != nil {
					t.Fatal(err)
				}
				protected := map[string]string{"state": filepath.Join(state, "sandboxed-agents", "group-default"), "ssh": filepath.Join(home, ".ssh"), "temp": temp, "executable": executable}[role]
				source := protected
				if role == "executable" {
					source = filepath.Dir(executable)
				} else if err := os.MkdirAll(protected, 0700); err != nil {
					t.Fatal(err)
				}
				target := project
				workspace := project
				switch relation {
				case "inside":
					workspace = filepath.Join(project, "nested")
					if err := os.Mkdir(workspace, 0700); err != nil {
						t.Fatal(err)
					}
				case "contains":
					target = filepath.Join(project, "alias")
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				case "protected-alias":
					if role == "executable" {
						target = filepath.Dir(executable)
						source = project
					} else {
						target = protected
						source = project
					}
				}
				escape := strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)
				mountinfo := fmt.Sprintf("1 0 0:1 / / rw - overlay overlay rw\n2 1 0:1 %s %s rw - overlay overlay rw\n32 1 0:28 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", escape.Replace(source), escape.Replace(target))
				hostFile(t, os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE"), "/proc/self/mountinfo", mountinfo)
				stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", workspace)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(stderr, protected) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("aliased protected workspace called Podman")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestUpRefusesUnreadableMountAliasesBeforePodman(t *testing.T) {
	for _, data := range []string{"", "not mountinfo\n", "32 1 0:28 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n"} {
		t.Run(fmt.Sprintf("bytes-%d", len(data)), func(t *testing.T) {
			fakes := linuxHost(t)
			workspace := workspaceFixture(t)
			hostFile(t, os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE"), "/proc/self/mountinfo", data)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", workspace)
			if status == 0 || stdout != "" || stderr == "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("unreadable aliases called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpUsesTheVisibleMountWhenAMountTargetIsStacked(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "project")
	for _, directory := range []string{home, filepath.Join(project, "alias")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	mountinfo := fmt.Sprintf("1 0 0:1 / / rw - overlay overlay rw\n2 1 0:40 / %s rw - autofs autofs rw\n3 2 8:3 / %s rw - ext4 /dev/sda rw\n4 1 8:3 /.ssh %s rw - ext4 /dev/sda rw\n32 1 0:28 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", home, home, filepath.Join(project, "alias"))
	hostFile(t, os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE"), "/proc/self/mountinfo", mountinfo)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", project)
	if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(stderr, filepath.Join(home, ".ssh")) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("shadowed mount hid a protected path")
	}
	assertNoSSH(t, fakes)
}

func TestUpNamesAnUnresolvableWorkspaceSymlink(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	path := filepath.Join(root, "loop")
	if err := os.Symlink("loop", path); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", path)
	if status == 0 || stdout != "" || !strings.Contains(stderr, path) || strings.Contains(stderr, `cannot resolve ""`) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("unresolved workspace called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpNamesTheUnsupportedWorkspaceHostWithoutCallingItWindows(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	stdout, stderr, status := runCLI(t, "unsupported-preflight", "up", "agent01", "/project")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "darwin") || strings.Contains(stderr, "Windows") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("unsupported host called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpBindsWorkspaceWithTheInspectedNativeImageAndLimits(t *testing.T) {
	fakes := linuxHost(t)
	directory := workspaceFixture(t)
	port := unusedSSHPort(t)
	responses := append(upObjectResponses(nil, false, nil, nil),
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:current-base"}]`},
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"sha256:validated-native","Labels":{"io.github.sandboxed-agents.base-image":"sha256:current-base"}}]`},
	)
	responses = append(responses, make([]testutil.Response, 4)...)
	fakes.Script("podman", responses...)
	_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", directory, "--cpus", "2", "--with", "native", "--port", strconv.Itoa(port))
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	calls := fakes.Calls("podman")
	create := calls[len(calls)-2].Args
	if create[len(create)-1] != "sha256:validated-native" {
		t.Fatalf("create did not use the inspected image: %v", create)
	}
	assertSSHPublication(t, create, port)
	var mounts []string
	for index, arg := range create {
		if arg == "--mount" {
			mounts = append(mounts, create[index+1])
		}
	}
	wantMounts := []string{"type=bind,source=" + directory + ",target=/workspace", "type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent", "type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh"}
	if !reflect.DeepEqual(mounts, wantMounts) || !strings.Contains(strings.Join(create, "\n"), "io.github.sandboxed-agents.toolchains=native") || !strings.Contains(strings.Join(create, "\n"), "--cpus=2") {
		t.Fatalf("create=%v", create)
	}
	for _, call := range calls {
		if len(call.Args) > 1 && call.Args[0] == "volume" && call.Args[1] == "create" && call.Args[len(call.Args)-1] == "sandboxed-agents.default.agent01.workspace" {
			t.Fatalf("created workspace volume: %v", call.Args)
		}
	}
	assertNoSSH(t, fakes)
}

func TestUpRefusesBusyExplicitSSHPortWithABoundWorkspaceBeforeSelectedImageWork(t *testing.T) {
	fakes := linuxHost(t)
	directory := workspaceFixture(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	fakes.Script("podman", upObjectResponses(nil, false, nil, nil)...)
	_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", directory, "--with", "native", "--port="+strconv.Itoa(port))
	if status == 0 || !strings.Contains(stderr, strconv.Itoa(port)) || !strings.Contains(stderr, "unavailable") {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	assertSSHReadOnly(t, fakes)
}

func TestUpRejectsWorkspaceAfterToolchainOptionsBeforePodman(t *testing.T) {
	for _, options := range [][]string{{"--with", "native"}, {"--with=native"}, {"--cpus", "2", "--with", "native"}} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			directory := workspaceFixture(t)
			args := append([]string{"up", "agent01"}, options...)
			args = append(args, directory)
			stdout, stderr, status := runCLI(t, "linux-preflight", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "unexpected argument") || !strings.Contains(stderr, directory) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("workspace after options called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}
