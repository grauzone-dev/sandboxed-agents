package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

var sandboxVolumeRoles = []string{"workspace", "home", "ssh"}

func TestUpCreatesARunningSandboxWithSafeDefaults(t *testing.T) {
	fakes := linuxHost(t)
	fakes.Script("podman",
		testutil.Response{Stdout: "podman version 5.0.0\n"},
		testutil.Response{ExitCode: 1},
		testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1},
		testutil.Response{ExitCode: 1},
		testutil.Response{},
		testutil.Response{}, testutil.Response{}, testutil.Response{},
		testutil.Response{Stdout: "container-id\n"}, testutil.Response{},
	)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := []testutil.Call{
		{Args: []string{"--version"}},
		{Args: []string{"container", "exists", "sandboxed-agents.default.agent01"}},
		{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.workspace"}},
		{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.home"}},
		{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.ssh"}},
		{Args: []string{"container", "exists", "sandboxed-agents-backup.default.agent01"}},
		{Args: []string{"image", "exists", "localhost/sandboxed-agents:base-fixture-assets"}},
		{Args: []string{"volume", "create", "--label", "io.github.sandboxed-agents.owner=default", "sandboxed-agents.default.agent01.workspace"}},
		{Args: []string{"volume", "create", "--label", "io.github.sandboxed-agents.owner=default", "sandboxed-agents.default.agent01.home"}},
		{Args: []string{"volume", "create", "--label", "io.github.sandboxed-agents.owner=default", "sandboxed-agents.default.agent01.ssh"}},
		{Args: []string{"create", "--name", "sandboxed-agents.default.agent01",
			"--label", "io.github.sandboxed-agents.owner=default",
			"--label", "io.github.sandboxed-agents.sandbox-name=agent01",
			"--label", "io.github.sandboxed-agents.workspace-kind=volume",
			"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
			"--memory=8589934592", "--label", "io.github.sandboxed-agents.memory=8589934592",
			"--cpus=4", "--label", "io.github.sandboxed-agents.cpus=4",
			"--pids-limit=2048", "--label", "io.github.sandboxed-agents.pids-limit=2048",
			"--shm-size=1073741824", "--label", "io.github.sandboxed-agents.shm-size=1073741824",
			"--mount", "type=volume,source=sandboxed-agents.default.agent01.workspace,target=/workspace",
			"--mount", "type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent",
			"--mount", "type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh",
			"localhost/sandboxed-agents:base-fixture-assets"}},
		{Args: []string{"start", "sandboxed-agents.default.agent01"}},
	}
	if calls := fakes.Calls("podman"); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	for _, role := range sandboxVolumeRoles {
		if !strings.Contains(stdout, "Created volume sandboxed-agents.default.agent01."+role) {
			t.Fatalf("missing volume output: %q", stdout)
		}
	}
	assertNoSSH(t, fakes)
}

func TestUpResumesAnOwnedSandboxWithoutChangingItsConfiguration(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			fakes := linuxHost(t)
			owned := "default"
			responses := upObjectResponses(&owned, running, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil)
			if !running {
				responses = append(responses, testutil.Response{})
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			want := []testutil.Call{
				{Args: []string{"--version"}},
				{Args: []string{"container", "exists", "sandboxed-agents.default.agent01"}},
				{Args: []string{"container", "inspect", "sandboxed-agents.default.agent01"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.workspace"}},
				{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.workspace"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.home"}},
				{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.home"}},
				{Args: []string{"volume", "exists", "sandboxed-agents.default.agent01.ssh"}},
				{Args: []string{"volume", "inspect", "sandboxed-agents.default.agent01.ssh"}},
				{Args: []string{"container", "exists", "sandboxed-agents-backup.default.agent01"}},
			}
			if !running {
				want = append(want, testutil.Call{Args: []string{"start", "sandboxed-agents.default.agent01"}})
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpAdoptsKeptVolumesAndCreatesOnlyMissingOnes(t *testing.T) {
	for mask := 1; mask < 8; mask++ {
		t.Run(fmt.Sprintf("kept-%03b", mask), func(t *testing.T) {
			fakes := linuxHost(t)
			kept := map[string]string{}
			roles := sandboxVolumeRoles
			for index, role := range roles {
				if mask&(1<<index) != 0 {
					kept[role] = "default"
				}
			}
			responses := append(upObjectResponses(nil, false, kept, nil), testutil.Response{})
			for index := range roles {
				if mask&(1<<index) == 0 {
					responses = append(responses, testutil.Response{})
				}
			}
			responses = append(responses, testutil.Response{}, testutil.Response{})
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var created []string
			var container []string
			for _, call := range fakes.Calls("podman") {
				if len(call.Args) > 1 && call.Args[0] == "volume" && call.Args[1] == "create" {
					created = append(created, call.Args[len(call.Args)-1])
				}
				if call.Args[0] == "create" {
					container = call.Args
				}
			}
			var wantCreated []string
			for index, role := range roles {
				name := "sandboxed-agents.default.agent01." + role
				verb := "Adopted"
				if mask&(1<<index) == 0 {
					wantCreated = append(wantCreated, name)
					verb = "Created"
				}
				if !strings.Contains(stdout, verb+" volume "+name) {
					t.Fatalf("missing %s volume output: %q", verb, stdout)
				}
				if !strings.Contains(strings.Join(container, " "), "source="+name+",") {
					t.Fatalf("container does not use %s: %v", name, container)
				}
			}
			if !reflect.DeepEqual(created, wantCreated) {
				t.Fatalf("created=%v want=%v", created, wantCreated)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func upObjectResponses(containerOwner *string, running bool, volumes map[string]string, backupOwner *string) []testutil.Response {
	return append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, sandboxObjectResponses(containerOwner, running, volumes, backupOwner)...)
}

func sandboxObjectResponses(containerOwner *string, running bool, volumes map[string]string, backupOwner *string) []testutil.Response {
	var responses []testutil.Response
	container := func(name, owner string) testutil.Response {
		return testutil.Response{Stdout: fmt.Sprintf(`[{"Name":%q,"Config":{"Labels":{"io.github.sandboxed-agents.owner":%q}},"State":{"Running":%t}}]`, name, owner, running)}
	}
	if containerOwner == nil {
		responses = append(responses, testutil.Response{ExitCode: 1})
	} else {
		responses = append(responses, testutil.Response{}, container("sandboxed-agents.default.agent01", *containerOwner))
	}
	for _, role := range sandboxVolumeRoles {
		if owner, present := volumes[role]; present {
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: fmt.Sprintf(`[{"Name":"sandboxed-agents.default.agent01.%s","Labels":{"io.github.sandboxed-agents.owner":%q}}]`, role, owner)})
		} else {
			responses = append(responses, testutil.Response{ExitCode: 1})
		}
	}
	if backupOwner == nil {
		responses = append(responses, testutil.Response{ExitCode: 1})
	} else {
		responses = append(responses, testutil.Response{}, container("sandboxed-agents-backup.default.agent01", *backupOwner))
	}
	return responses
}

func assertNoSSH(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	if len(fakes.Calls("ssh")) != 0 || len(fakes.Calls("ssh-keygen")) != 0 {
		t.Fatal("command attempted SSH")
	}
}

func assertPodmanReadOnly(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	for _, call := range fakes.Calls("podman") {
		args := call.Args
		if reflect.DeepEqual(args, []string{"--version"}) {
			continue
		}
		if len(args) == 3 && (args[0] == "container" || args[0] == "volume") && (args[1] == "exists" || args[1] == "inspect") {
			continue
		}
		t.Fatalf("refused command changed state: %v", args)
	}
	assertNoSSH(t, fakes)
}

func TestUpRefusesEveryForeignObjectBeforeAnInterruptedUpdate(t *testing.T) {
	owned := "default"
	for _, owner := range []string{"", "another-group"} {
		for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
			for _, withContainer := range []bool{false, true} {
				if object == "container" && !withContainer {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/container-%t", owner, object, withContainer), func(t *testing.T) {
					fakes := linuxHost(t)
					var containerOwner *string
					if withContainer {
						containerOwner = &owned
					}
					volumes := map[string]string{"workspace": "default", "home": "default", "ssh": "default"}
					backupOwner := &owned
					name := "sandboxed-agents.default.agent01"
					switch object {
					case "container":
						containerOwner = &owner
					case "backup":
						backupOwner = &owner
						name = "sandboxed-agents-backup.default.agent01"
					default:
						volumes[object] = owner
						name += "." + object
					}
					fakes.Script("podman", upObjectResponses(containerOwner, true, volumes, backupOwner)...)
					stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
					if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, name) || !strings.Contains(stderr, "Podman") || !strings.Contains(stderr, "remove or rename") || strings.Contains(stderr, "interrupted update") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					assertPodmanReadOnly(t, fakes)
				})
			}
		}
	}
}

func TestUpNamesAllForeignSandboxObjects(t *testing.T) {
	fakes := linuxHost(t)
	foreign := ""
	fakes.Script("podman", upObjectResponses(&foreign, false, map[string]string{"workspace": "other", "home": "", "ssh": "default"}, nil)...)
	_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
	if status == 0 || !strings.Contains(stderr, "owner conflict") {
		t.Fatalf("status=%d stderr=%q", status, stderr)
	}
	for _, name := range []string{"sandboxed-agents.default.agent01", "sandboxed-agents.default.agent01.workspace", "sandboxed-agents.default.agent01.home"} {
		if !strings.Contains(stderr, name) {
			t.Fatalf("missing foreign object %s: %q", name, stderr)
		}
	}
	assertPodmanReadOnly(t, fakes)
}

func TestUpRefusesAnInterruptedUpdateWithoutChangingAnything(t *testing.T) {
	owned := "default"
	for _, withContainer := range []bool{false, true} {
		t.Run(fmt.Sprint(withContainer), func(t *testing.T) {
			fakes := linuxHost(t)
			var containerOwner *string
			if withContainer {
				containerOwner = &owned
			}
			fakes.Script("podman", upObjectResponses(containerOwner, false, map[string]string{"workspace": "default"}, &owned)...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
			if status == 0 || !strings.Contains(stderr, "sandboxed-agents-backup.default.agent01") || !strings.Contains(stderr, "update agent01") || !strings.Contains(stderr, "interrupted update") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertPodmanReadOnly(t, fakes)
		})
	}
}

func TestUpRejectsUsageAndInvalidNamesBeforePreflight(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"up", "-x"}, {"up", ".x"}, {"up", "a/b"}, {"up", "a b"}, {"up", ""}, {"up", "agent01", "extra"}, {"up", "agent01", "--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
			stdout, stderr, status := runCLI(t, "linux-preflight", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(args) == 2 && !strings.Contains(stderr, "invalid sandbox name") {
				t.Fatalf("missing name error: %q", stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("invalid usage ran an external program")
			}
		})
	}
}

func TestUpReportsPreflightBeforeForeignObjectsAndBackup(t *testing.T) {
	fakes := linuxHost(t)
	t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
	fakes.Script("podman", testutil.Response{Stdout: "podman version 5.0.0\n"}, testutil.Response{Stdout: "foreign objects"})
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
	if status == 0 || !strings.Contains(stdout, "MISSING: rootless") || !strings.Contains(stderr, "host prerequisites") || strings.Contains(stderr, "owner") || strings.Contains(stderr, "backup") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if calls := fakes.Calls("podman"); !reflect.DeepEqual(calls, []testutil.Call{{Args: []string{"--version"}}}) {
		t.Fatalf("calls=%v", calls)
	}
	assertNoSSH(t, fakes)
}

func TestUpBuildsOnlyAnAbsentSharedBaseImage(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-%t", missing), func(t *testing.T) {
			fakes := linuxHost(t)
			stdout, stderr, status := runCLI(t, "linux-build", "version")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			hash := strings.TrimSpace(strings.Split(stdout, "assets ")[1])
			tag := "localhost/sandboxed-agents:base-" + hash
			responses := upObjectResponses(nil, false, nil, nil)
			capture := filepath.Join(t.TempDir(), "context")
			if missing {
				responses = append(responses, testutil.Response{ExitCode: 1}, testutil.Response{CaptureBuildContext: capture})
			} else {
				responses = append(responses, testutil.Response{})
			}
			responses = append(responses, make([]testutil.Response, 5)...)
			fakes.Script("podman", responses...)
			stdout, stderr, status = runCLIAt(t, t.TempDir(), "linux-build", "up", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if !reflect.DeepEqual(calls[6].Args, []string{"image", "exists", tag}) {
				t.Fatalf("image check=%v", calls[6])
			}
			var builds int
			for index, call := range calls {
				if call.Args[0] != "build" {
					continue
				}
				builds++
				if !missing || index != 7 {
					t.Fatalf("unexpected build: %v", calls)
				}
				directory := call.Args[len(call.Args)-1]
				want := []string{"build", "--pull=always", "--no-cache", "--tag", tag,
					"--label", "io.github.sandboxed-agents.managed=true",
					"--label", "io.github.sandboxed-agents.asset-hash=" + hash,
					"--label", "io.github.sandboxed-agents.toolchains=",
					"--file", filepath.Join(directory, "Containerfile"), directory}
				if !reflect.DeepEqual(call.Args, want) {
					t.Fatalf("build=%v want=%v", call.Args, want)
				}
				if _, err := os.Stat(directory); !os.IsNotExist(err) {
					t.Fatalf("build context remains: %s: %v", directory, err)
				}
			}
			if missing {
				if builds != 1 {
					t.Fatalf("builds=%d", builds)
				}
				checkBaseContext(t, capture)
			} else if builds != 0 {
				t.Fatalf("builds=%d", builds)
			}
			create := calls[len(calls)-2].Args
			if create[0] != "create" || create[len(create)-1] != tag {
				t.Fatalf("create=%v", create)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpAcceptsCommandNamesAndKeepsVolumeNamesUnique(t *testing.T) {
	volumeNames := map[string]bool{}
	for _, name := range []string{"up", "list", "default", "backup", "a", "a.home", "a.workspace", "a.ssh", "Agent_01.x-", "0"} {
		t.Run(name, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", name)
			if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox "+name+" is running") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var created int
			for _, call := range fakes.Calls("podman") {
				if len(call.Args) < 2 || call.Args[0] != "volume" || call.Args[1] != "create" {
					continue
				}
				created++
				volume := call.Args[len(call.Args)-1]
				if volumeNames[volume] {
					t.Fatalf("volume name collision: %s", volume)
				}
				volumeNames[volume] = true
				if !strings.HasPrefix(volume, "sandboxed-agents.default."+name+".") {
					t.Fatalf("volume=%s", volume)
				}
			}
			if created != 3 {
				t.Fatalf("created=%d", created)
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpStopsWhenPodmanCannotReadItsObjects(t *testing.T) {
	for _, test := range []struct {
		name      string
		responses []testutil.Response
		want      string
	}{
		{"exists-failure", []testutil.Response{{ExitCode: 125, Stderr: "storage inaccessible"}}, "storage inaccessible"},
		{"container-inspect-failure", []testutil.Response{{}, {ExitCode: 42, Stderr: "inspect failed"}}, "inspect failed"},
		{"invalid-json", []testutil.Response{{}, {Stdout: "{"}}, "decode podman container inspect"},
		{"empty-container", []testutil.Response{{}, {Stdout: "[]"}}, "invalid podman container inspect"},
		{"wrong-container", []testutil.Response{{}, {Stdout: `[{"Name":"wrong","Config":{},"State":{}}]`}}, "invalid podman container inspect"},
		{"missing-state", []testutil.Response{{}, {Stdout: `[{"Name":"sandboxed-agents.default.agent01","Config":{"Labels":{"io.github.sandboxed-agents.owner":"default"}}}]`}}, "invalid podman container inspect"},
		{"volume-exists-failure", []testutil.Response{{ExitCode: 1}, {ExitCode: 125, Stderr: "volume storage inaccessible"}}, "volume storage inaccessible"},
		{"volume-inspect-failure", []testutil.Response{{ExitCode: 1}, {}, {ExitCode: 42, Stderr: "volume inspect failed"}}, "volume inspect failed"},
		{"invalid-volume", []testutil.Response{{ExitCode: 1}, {}, {Stdout: "[]"}}, "invalid podman volume inspect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, test.responses...)
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
			if status == 0 || !strings.Contains(stderr, test.want) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertPodmanReadOnly(t, fakes)
		})
	}
}

func TestUpStopsAfterImageAndCreationFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		responses []testutil.Response
		last      string
		want      string
		fixture   string
	}{
		{"image-check", []testutil.Response{{ExitCode: 125, Stderr: "image storage inaccessible"}}, "image", "image storage inaccessible", "linux-preflight"},
		{"build", []testutil.Response{{ExitCode: 1}, {ExitCode: 42}}, "build", "podman build failed with exit status 42", "linux-build"},
		{"volume", []testutil.Response{{}, {ExitCode: 42}}, "volume", "podman volume failed with exit status 42", "linux-preflight"},
		{"container", []testutil.Response{{}, {}, {}, {}, {ExitCode: 42}}, "create", "podman create failed with exit status 42", "linux-preflight"},
		{"start", []testutil.Response{{}, {}, {}, {}, {}, {ExitCode: 42}}, "start", "podman start failed with exit status 42", "linux-preflight"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakes := linuxHost(t)
			fakes.Script("podman", append(upObjectResponses(nil, false, nil, nil), test.responses...)...)
			stdout, stderr, status := runCLI(t, test.fixture, "up", "agent01")
			if status == 0 || !strings.Contains(stderr, test.want) || strings.Contains(stdout, "Sandbox agent01 is running") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if calls[len(calls)-1].Args[0] != test.last || len(calls) != 6+len(test.responses) {
				t.Fatalf("calls=%v", calls)
			}
			if test.name == "build" {
				args := calls[len(calls)-1].Args
				if _, err := os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
					t.Fatalf("build context remains: %v", err)
				}
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpRefusesObjectsWithoutAnOwnerLabel(t *testing.T) {
	owned := "default"
	for _, object := range []string{"container", "volume", "backup"} {
		t.Run(object, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := upObjectResponses(&owned, false, map[string]string{"workspace": "default"}, &owned)
			name := "sandboxed-agents.default.agent01"
			switch object {
			case "volume":
				name += ".workspace"
			case "backup":
				name = "sandboxed-agents-backup.default.agent01"
			}
			for index := range responses {
				if strings.Contains(responses[index].Stdout, fmt.Sprintf(`"Name":%q`, name)) {
					responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"io.github.sandboxed-agents.owner":"default"`, "")
				}
			}
			fakes.Script("podman", responses...)
			_, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
			if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, name) {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			assertPodmanReadOnly(t, fakes)
		})
	}
}
