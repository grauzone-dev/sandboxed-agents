package cli_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestListShowsKeptVolumesAndPreservesSandboxNameSuffixes(t *testing.T) {
	for _, name := range []string{"agent.workspace", "agent.home", "agent.ssh", "agent.backup", "agent.with.dots"} {
		for _, volumes := range [][]string{{"home"}, {"workspace"}, {"home", "ssh", "workspace"}} {
			t.Run(name+"/"+strings.Join(volumes, "+"), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				owners := map[string]string{}
				var names []string
				workspace := "-"
				for _, suffix := range volumes {
					owners[suffix] = "default"
					names = append(names, "sandboxed-agents.default."+name+"."+suffix)
					if suffix == "workspace" {
						workspace = "volume"
					}
				}
				fakes.Script("podman", listOneSandboxResponses("default", name, nil, false, owners, nil)...)
				stdout, stderr, status := runCLI(t, "sandbox-host", "list")
				want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES " + name + " volumes only " + workspace + " - - - " + strings.Join(names, ",")
				if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
					t.Fatalf("status=%d stdout=%q stderr=%q want=%q", status, stdout, stderr, want)
				}
				assertListReadOnly(t, fakes, false)
			})
		}
	}
}

func TestListReportsOwnerConflictsBeforeInterruptedUpdatesAndVolumesOnly(t *testing.T) {
	owned := "default"
	for _, containerExists := range []bool{false, true} {
		for _, conflict := range []string{"none", "container", "workspace", "home", "ssh", "backup"} {
			for _, owner := range []string{"", "other"} {
				t.Run(fmt.Sprintf("container-%t/%s/owner-%s", containerExists, conflict, owner), func(t *testing.T) {
					fakes := testutil.NewFakePrograms(t)
					containerOwner := owned
					var container *string
					if containerExists || conflict == "container" {
						container = &containerOwner
					}
					volumes := map[string]string{"home": owned, "workspace": owned}
					backupOwner := owned
					wantState := "update interrupted"
					if conflict != "none" {
						wantState = "owner conflict"
						switch conflict {
						case "container":
							containerOwner = owner
						case "backup":
							backupOwner = owner
						default:
							volumes[conflict] = owner
						}
					}
					fakes.Script("podman", listOneSandboxResponses("default", "agent01", container, true, volumes, &backupOwner)...)
					stdout, stderr, status := runCLI(t, "sandbox-host", "list")
					lines := strings.Split(strings.TrimSpace(stdout), "\n")
					if status != 0 || stderr != "" || len(lines) != 2 || !strings.HasPrefix(strings.Join(strings.Fields(lines[1]), " "), "agent01 "+wantState+" volume - - - ") || strings.Contains(stdout, "sandboxed-agents-backup") {
						t.Fatalf("status=%d stdout=%q stderr=%q want state=%q", status, stdout, stderr, wantState)
					}
					assertListReadOnly(t, fakes, false)
				})
			}
		}
	}
}

func TestListShowsAnInterruptedUpdateWhenOnlyItsBackupContainerRemains(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	owned := "default"
	fakes.Script("podman", listOneSandboxResponses("default", "agent.backup", nil, false, nil, &owned)...)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent.backup update interrupted volume - - - -"
	if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
		t.Fatalf("status=%d stdout=%q stderr=%q want=%q", status, stdout, stderr, want)
	}
	assertListReadOnly(t, fakes, false)
}

func TestListUsesTheSelectedGroupOnLinuxAndWindows(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			owned := "team-a"
			responses := listOneSandboxResponses("team-a", "agent01", &owned, true, nil, nil)
			responses = append(responses, listCurrentImageResponses("", "current-base", "current-base")...)
			if fixture == "windows" {
				for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
					t.Setenv(key, "ambient-other-target")
				}
				for index := range responses {
					responses[index].AbsentEnv = []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"}
				}
				responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, "list")
			want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 running volume - - - -"
			if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, fixture == "windows")
		})
	}
}

func TestListFindsOwnedContainersAfterTheirPodmanNameChanges(t *testing.T) {
	for _, name := range []string{"renamed-container", "sandboxed-agents.other.old-name"} {
		t.Run(name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			labels := map[string]string{"io.github.sandboxed-agents.owner": "default", "io.github.sandboxed-agents.sandbox-name": "agent01", "io.github.sandboxed-agents.workspace-kind": "bind"}
			fakes.Script("podman",
				listJSONResponse([]map[string]any{{"Names": []string{name}, "Labels": labels}}),
				testutil.Response{Stdout: `[]`},
				listJSONResponse([]map[string]any{{"Name": name, "Image": "current-base", "Config": map[string]any{"Labels": labels}, "State": map[string]bool{"Running": true}}}),
				testutil.Response{},
				testutil.Response{Stdout: `[{"Id":"current-base"}]`},
			)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 running bind - - - -"
			if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestListIncludesVolumesOnlyUnderTheCurrentControllerGroupsPodmanNames(t *testing.T) {
	for _, group := range []string{"default", "team-a"} {
		t.Run(group, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			t.Setenv("SANDBOXED_AGENTS_GROUP", group)
			home := map[string]any{"Name": "sandboxed-agents." + group + ".agent01.home", "Labels": map[string]string{"io.github.sandboxed-agents.owner": group}}
			workspace := map[string]any{"Name": "sandboxed-agents." + group + ".agent01.workspace", "Labels": map[string]string{"io.github.sandboxed-agents.owner": "foreign"}}
			fakes.Script("podman",
				testutil.Response{Stdout: `[]`},
				listJSONResponse([]map[string]any{
					{"Name": "sandboxed-agents.other.agent01.ssh", "Labels": map[string]string{"io.github.sandboxed-agents.owner": group}},
					{"Name": "sandboxed-agents.other.hidden.workspace", "Labels": map[string]string{"io.github.sandboxed-agents.owner": group}},
					workspace,
					home,
				}),
				listJSONResponse([]map[string]any{home}),
				listJSONResponse([]map[string]any{workspace}),
			)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			for _, call := range fakes.Calls("podman") {
				if len(call.Args) == 3 && call.Args[0] == "volume" && call.Args[1] == "inspect" && strings.HasPrefix(call.Args[2], "sandboxed-agents.other.") {
					t.Errorf("inspected a volume outside the current group's Podman names: %v", call.Args)
				}
			}
			want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES agent01 owner conflict volume - - - sandboxed-agents." + group + ".agent01.home,sandboxed-agents." + group + ".agent01.workspace"
			if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != want {
				t.Fatalf("status=%d stdout=%q stderr=%q want=%q", status, stdout, stderr, want)
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestListShowsAHeaderWhenNoSandboxesExist(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: `[]`}, testutil.Response{Stdout: `[]`})
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	if status != 0 || stderr != "" || strings.Join(strings.Fields(stdout), " ") != "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	assertListReadOnly(t, fakes, false)
}

func TestListReportsAnOwnerConflictForAContainerOrKeptVolumesWithoutABackupContainer(t *testing.T) {
	for _, object := range []string{"container", "workspace", "home", "ssh"} {
		for _, owner := range []string{"", "other"} {
			t.Run(object+"/owner-"+owner, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				var container *string
				volumes := map[string]string{}
				workspace := "-"
				if object == "container" {
					container = &owner
					workspace = "volume"
				} else {
					volumes[object] = owner
					if object == "workspace" {
						workspace = "volume"
					}
				}
				fakes.Script("podman", listOneSandboxResponses("default", "agent01", container, false, volumes, nil)...)
				stdout, stderr, status := runCLI(t, "sandbox-host", "list")
				if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 owner conflict "+workspace+" - - - ") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertListReadOnly(t, fakes, false)
			})
		}
	}
}

func TestListRejectsArgumentsBeforeCallingPodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, argument := range []string{"agent01", "--all", "--unknown"} {
			t.Run(fixture+"/"+argument, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, "list", argument)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestListRejectsFailedOrMalformedLookupsWithoutPrintingPartialRows(t *testing.T) {
	for _, test := range []struct {
		name     string
		index    int
		response testutil.Response
		message  string
	}{
		{"inventory failure", 0, testutil.Response{ExitCode: 125, Stderr: "engine unavailable"}, "engine unavailable"},
		{"malformed container inventory", 0, testutil.Response{Stdout: `not-json`}, "decode podman ps"},
		{"null container inventory", 0, testutil.Response{Stdout: `null`}, "invalid podman ps"},
		{"missing container names", 0, testutil.Response{Stdout: `[{}]`}, "invalid podman ps"},
		{"duplicate containers", 0, testutil.Response{Stdout: `[{"Names":["x"]},{"Names":["x"]}]`}, "invalid podman ps"},
		{"volume inventory failure", 1, testutil.Response{ExitCode: 125, Stderr: "volumes unavailable"}, "volumes unavailable"},
		{"malformed volume inventory", 1, testutil.Response{Stdout: `not-json`}, "decode podman volume ls"},
		{"null volume inventory", 1, testutil.Response{Stdout: `null`}, "invalid podman volume ls"},
		{"missing volume name", 1, testutil.Response{Stdout: `[{}]`}, "invalid podman volume ls"},
		{"duplicate volumes", 1, testutil.Response{Stdout: `[{"Name":"x"},{"Name":"x"}]`}, "invalid podman volume ls"},
		{"container inspection failure", 2, testutil.Response{ExitCode: 125, Stderr: "container unreadable"}, "container unreadable"},
		{"empty container inspection", 2, testutil.Response{Stdout: `[]`}, "invalid podman container inspect"},
		{"missing container state", 2, testutil.Response{Stdout: `[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{}}}]`}, "invalid podman container inspect"},
		{"volume inspection failure", 3, testutil.Response{ExitCode: 125, Stderr: "volume unreadable"}, "volume unreadable"},
		{"wrong volume inspected", 3, testutil.Response{Stdout: `[{"Name":"foreign"}]`}, "invalid podman volume inspect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owned := "default"
			responses := listOneSandboxResponses("default", "agent01", &owned, true, map[string]string{"workspace": owned}, nil)
			responses[test.index] = test.response
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			if status == 0 || stdout != "" || !strings.Contains(stderr, test.message) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func listOneSandboxResponses(group, name string, containerOwner *string, running bool, volumes map[string]string, backupOwner *string) []testutil.Response {
	var containers []map[string]any
	var volumeRecords []map[string]any
	var inspections []testutil.Response
	containerName := "sandboxed-agents." + group + "." + name
	container := func(podmanName, owner string) {
		labels := map[string]string{"io.github.sandboxed-agents.owner": owner, "io.github.sandboxed-agents.workspace-kind": "volume"}
		containers = append(containers, map[string]any{"Names": []string{podmanName}, "Labels": labels})
		inspections = append(inspections, listJSONResponse([]map[string]any{{"Name": podmanName, "Image": "current-base", "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": running}}}))
	}
	if containerOwner != nil {
		container(containerName, *containerOwner)
	}
	var suffixes []string
	for suffix := range volumes {
		suffixes = append(suffixes, suffix)
	}
	slices.Sort(suffixes)
	for _, suffix := range suffixes {
		record := map[string]any{"Name": containerName + "." + suffix, "Labels": map[string]string{"io.github.sandboxed-agents.owner": volumes[suffix]}}
		volumeRecords = append(volumeRecords, record)
		inspections = append(inspections, listJSONResponse([]map[string]any{record}))
	}
	if backupOwner != nil {
		container("sandboxed-agents-backup."+group+"."+name, *backupOwner)
	}
	if containers == nil {
		containers = []map[string]any{}
	}
	if volumeRecords == nil {
		volumeRecords = []map[string]any{}
	}
	return append([]testutil.Response{listJSONResponse(containers), listJSONResponse(volumeRecords)}, inspections...)
}

func listCurrentImageResponses(set, imageID, baseID string) []testutil.Response {
	responses := []testutil.Response{{}, listJSONResponse([]map[string]any{{"Id": baseID}})}
	if set != "" {
		responses = append(responses, testutil.Response{}, listJSONResponse([]map[string]any{{"Id": imageID, "Labels": map[string]string{"io.github.sandboxed-agents.base-image": baseID}}}))
	}
	return responses
}

func listJSONResponse(value any) testutil.Response {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return testutil.Response{Stdout: string(data)}
}

func TestListShowsOnlyCurrentControllerGroupWithUnavailableColumns(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman",
		testutil.Response{Stdout: `[{"Names":["sandboxed-agents.default.zeta"],"Labels":{"io.github.sandboxed-agents.owner":"default"}},{"Names":["sandboxed-agents.other.hidden"],"Labels":{"io.github.sandboxed-agents.owner":"other"}},{"Names":["sandboxed-agents.default.alpha"],"Labels":{"io.github.sandboxed-agents.owner":"default"}}]`},
		testutil.Response{Stdout: `[]`},
		testutil.Response{Stdout: `[{"Name":"sandboxed-agents.default.alpha","Image":"current-base","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.workspace-kind":"bind"}},"State":{"Running":false}}]`},
		testutil.Response{Stdout: `[{"Name":"sandboxed-agents.default.zeta","Image":"current-base","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default","io.github.sandboxed-agents.workspace-kind":"volume"}},"State":{"Running":true}}]`},
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"current-base"}]`},
	)
	stdout, stderr, status := runCLI(t, "sandbox-host", "list")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES alpha stopped bind - - - - zeta running volume - - - -"
	if got := strings.Join(strings.Fields(stdout), " "); got != want {
		t.Fatalf("list=%q want=%q", got, want)
	}
	assertListReadOnly(t, fakes, false)
}

func assertListReadOnly(t *testing.T, fakes *testutil.FakePrograms, windows bool) {
	t.Helper()
	calls := fakes.Calls("podman")
	if windows {
		calls = windowsOperationCalls(t, calls[2:], "podman-machine-default")
	}
	for _, call := range calls {
		if len(call.Args) == 0 {
			t.Fatal("empty Podman invocation")
		}
		switch call.Args[0] {
		case "exec":
			if len(call.Args) != 6 || call.Args[1] != "--user=0:0" || call.Args[3] != "/usr/local/bin/sandboxed-agents-manager" || call.Args[4] != "agents" || call.Args[5] != "list" {
				t.Fatalf("unexpected manager query: %v", call.Args)
			}
		case "ps":
			if !slices.Equal(call.Args, []string{"ps", "--all", "--format", "json"}) {
				t.Fatalf("unexpected container inventory: %v", call.Args)
			}
		case "container":
			if len(call.Args) != 3 || call.Args[1] != "inspect" {
				t.Fatalf("unexpected container operation: %v", call.Args)
			}
		case "image":
			if len(call.Args) != 3 || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
				t.Fatalf("unexpected image operation: %v", call.Args)
			}
		case "volume":
			if !slices.Equal(call.Args, []string{"volume", "ls", "--format", "json"}) && (len(call.Args) != 3 || call.Args[1] != "inspect") {
				t.Fatalf("unexpected volume operation: %v", call.Args)
			}
		default:
			t.Fatalf("list ran a non-inventory operation: %v", call.Args)
		}
	}
	assertNoSSH(t, fakes)
}
