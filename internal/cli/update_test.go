package cli_test

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpdateKeepsTheWindowsWorkspaceBindRecordedByPodman(t *testing.T) {
	fakes, fixture := resourceLimitHost(t, true)
	source := "/mnt/c/projects/team,workspace"
	responses := updateObjectResponses(t, true, "old-image", "", source)
	responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
	responses = append(responses, make([]testutil.Response, 4)...)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{})
	scriptUpdate(t, fakes, true, responses)
	stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	bound := false
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if args[0] != "create" {
			continue
		}
		if !slices.Contains(args, "io.github.sandboxed-agents.workspace-kind=bind") {
			t.Fatalf("kind lost: %v", args)
		}
		for index, arg := range args {
			if arg != "--mount" {
				continue
			}
			mount, err := csv.NewReader(strings.NewReader(args[index+1])).Read()
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(mount, "target=/workspace") {
				bound = reflect.DeepEqual(mount, []string{"type=bind", "source=" + source, "target=/workspace"})
			}
		}
	}
	if !bound {
		t.Fatal("Windows update did not keep the recorded Podman workspace bind")
	}
}

func TestUpdatePreservesStoppedSandboxesAndInstalledSSHFiles(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			for _, path := range []string{filepath.Join(sshDir, "config"), filepath.Join(state, "group-default", "agent01", "id_ed25519"), filepath.Join(state, "group-default", "agent01", "known_hosts")} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("unchanged SSH file\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
			responses := updateObjectResponses(t, false, "old-image", "", "")
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
			responses = append(responses, make([]testutil.Response, 3)...)
			responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var operations [][]string
			for _, call := range fakes.Calls("podman") {
				args := podmanUpdateArgs(call.Args)
				if slices.Contains([]string{"stop", "start", "rm"}, args[0]) {
					operations = append(operations, args)
				}
				if args[0] == "create" && !slices.Contains(args, "io.github.sandboxed-agents.update-was-running=false") {
					t.Fatalf("prior state absent: %v", args)
				}
			}
			want := [][]string{{"start", "sandboxed-agents.default.agent01"}, {"rm", "sandboxed-agents-backup.default.agent01"}, {"stop", "sandboxed-agents.default.agent01"}}
			if !reflect.DeepEqual(operations, want) {
				t.Fatalf("operations=%v", operations)
			}
			if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
				t.Fatal("update changed SSH files")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func scriptUpdate(t *testing.T, fakes *testutil.FakePrograms, windows bool, responses []testutil.Response) {
	t.Helper()
	if windows {
		responses = append(healthyWindowsPodman(), responses[1:]...)
	}
	fakes.Script("podman", responses...)
	fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t)})
}

func podmanUpdateArgs(args []string) []string {
	if len(args) > 2 && args[0] == "--connection" {
		return args[2:]
	}
	return args
}

func TestUpdateAlreadyCurrentSandboxTouchesNoContainerOrSSHFiles(t *testing.T) {
	for _, selection := range []string{"", "native"} {
		t.Run(selection, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := updateObjectResponses(t, true, "current-image", selection, "")
			baseID := "current-image"
			if selection != "" {
				baseID = "current-base"
			}
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"` + baseID + `"}]`})
			if selection != "" {
				responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-image","Labels":{"io.github.sandboxed-agents.base-image":"current-base"}}]`})
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "already up to date") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				if slices.Contains([]string{"build", "rename", "create", "start", "stop", "rm", "exec", "volume"}, call.Args[0]) && !(call.Args[0] == "volume" && slices.Contains([]string{"exists", "inspect"}, call.Args[1])) {
					t.Fatalf("no-op mutated or probed sandbox: %v", call)
				}
			}
			if len(fakes.Calls("ssh-keyscan")) != 0 {
				t.Fatal("no-op probed SSH")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpdateReportsMalformedRecordedConfigurationBeforeBuildingOrReplacing(t *testing.T) {
	for _, field := range []string{"toolchains", "memory", "cpus", "pids-limit", "shm-size", "ssh-port", "workspace-kind"} {
		t.Run(field, func(t *testing.T) {
			fakes := linuxHost(t)
			responses := updateObjectResponses(t, true, "old-image", "", "")
			var records []map[string]any
			if err := json.Unmarshal([]byte(responses[2].Stdout), &records); err != nil {
				t.Fatal(err)
			}
			labels := records[0]["Config"].(map[string]any)["Labels"].(map[string]any)
			labels["io.github.sandboxed-agents."+field] = "invalid-recorded-value"
			data, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			responses[2].Stdout = string(data)
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`})
			fakes.Script("podman", responses...)
			_, stderr, status := runCLI(t, "linux-preflight", "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "sandboxed-agents.default.agent01") {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				if slices.Contains([]string{"build", "create", "rename", "stop", "start", "rm"}, call.Args[0]) {
					t.Fatalf("malformed configuration changed sandbox: %v", call)
				}
			}
		})
	}
}

func TestLifecycleCommandsRefuseBeforeInspectingASandboxWhenAnotherCommandHoldsItsLock(t *testing.T) {
	for _, command := range []string{"up", "start", "stop", "restart", "remove", "update"} {
		t.Run(command, func(t *testing.T) {
			fakes := linuxHost(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			release, err := sandbox.LockLifecycle("linux", "default", "agent01")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			_, stderr, status := runCLI(t, "linux-preflight", command, "agent01")
			if status == 0 || !strings.Contains(stderr, "another lifecycle command") || !strings.Contains(stderr, "agent01") {
				t.Fatalf("status=%d stderr=%q calls=%v", status, stderr, fakes.Calls("podman"))
			}
			for _, call := range fakes.Calls("podman") {
				if !reflect.DeepEqual(call.Args, []string{"--version"}) {
					t.Fatalf("inspected sandbox before lock: %v", call)
				}
			}
		})
	}
}

func TestUpdateMovesARunningSandboxToTheCurrentImageWithoutChangingItsConfiguration(t *testing.T) {
	fakes := linuxHost(t)
	responses := updateObjectResponses(t, true, "old-image", "", "")
	responses = append(responses,
		testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`},
	)
	responses = append(responses, make([]testutil.Response, 4)...)
	responses = append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{})
	fakes.Script("podman", responses...)
	fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t)})
	stdout, stderr, status := runCLI(t, "linux-preflight", "update", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "updated") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	var changes [][]string
	for _, call := range calls {
		if slices.Contains([]string{"rename", "create", "stop", "start", "exec", "rm", "build"}, call.Args[0]) {
			changes = append(changes, call.Args)
		}
	}
	want := [][]string{
		{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"},
		nil,
		{"stop", "sandboxed-agents-backup.default.agent01"},
		{"start", "sandboxed-agents.default.agent01"},
		{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager", "version"},
		{"rm", "sandboxed-agents-backup.default.agent01"},
	}
	if len(changes) != len(want) {
		t.Fatalf("changes=%v", changes)
	}
	for i, args := range want {
		if i != 1 && !reflect.DeepEqual(changes[i], args) {
			t.Errorf("change %d=%v want=%v", i, changes[i], args)
		}
	}
	create := changes[1]
	for _, option := range []string{
		"--name", "sandboxed-agents.default.agent01", "io.github.sandboxed-agents.owner=default",
		"io.github.sandboxed-agents.toolchains=", "io.github.sandboxed-agents.update-was-running=true",
		"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
		"--memory=12884901888", "--cpus=2.5", "--pids-limit=512", "--shm-size=268435456",
		"type=volume,source=sandboxed-agents.default.agent01.workspace,target=/workspace",
		"type=volume,source=sandboxed-agents.default.agent01.home,target=/home/agent",
		"type=volume,source=sandboxed-agents.default.agent01.ssh,target=/etc/ssh",
	} {
		if !slices.Contains(create, option) {
			t.Errorf("create missing %q: %v", option, create)
		}
	}
	if create[len(create)-1] != "new-image" {
		t.Errorf("created from %q", create[len(create)-1])
	}
	assertSSHPublication(t, create, 2300)
	if scan := fakes.Calls("ssh-keyscan"); len(scan) != 1 || !slices.Contains(scan[0].Args, "2300") || scan[0].Args[len(scan[0].Args)-1] != "127.0.0.1" {
		t.Fatalf("SSH probes=%v", scan)
	}
	assertNoSSH(t, fakes)
	for _, call := range fakes.Calls("podman") {
		args := podmanUpdateArgs(call.Args)
		if args[0] == "volume" && !slices.Contains([]string{"exists", "inspect"}, args[1]) {
			t.Fatalf("update changed a named volume: %v", args)
		}
	}
}

func updateObjectResponses(t *testing.T, running bool, imageID, selection, workspace string) []testutil.Response {
	t.Helper()
	owner := "default"
	volumes := map[string]string{"workspace": owner, "home": owner, "ssh": owner}
	if workspace != "" {
		delete(volumes, "workspace")
	}
	responses := upObjectResponses(&owner, running, volumes, nil)
	mounts := []map[string]string{}
	for _, role := range sandboxVolumeRoles {
		target := map[string]string{"workspace": "/workspace", "home": "/home/agent", "ssh": "/etc/ssh"}[role]
		mount := map[string]string{"Type": "volume", "Name": "sandboxed-agents.default.agent01." + role, "Destination": target}
		if role == "workspace" && workspace != "" {
			mount = map[string]string{"Type": "bind", "Source": workspace, "Destination": target}
		}
		mounts = append(mounts, mount)
	}
	kind := "volume"
	if workspace != "" {
		kind = "bind"
	}
	record := map[string]any{"Name": "sandboxed-agents.default.agent01", "Image": imageID,
		"Config": map[string]any{"Labels": map[string]string{
			"io.github.sandboxed-agents.owner": "default", "io.github.sandboxed-agents.toolchains": selection,
			"io.github.sandboxed-agents.workspace-kind": kind, "io.github.sandboxed-agents.ssh-port": "2300",
			"io.github.sandboxed-agents.memory": "12884901888", "io.github.sandboxed-agents.cpus": "2.5",
			"io.github.sandboxed-agents.pids-limit": "512", "io.github.sandboxed-agents.shm-size": "268435456",
		}}, "State": map[string]bool{"Running": running}, "Mounts": mounts}
	data, err := json.Marshal([]any{record})
	if err != nil {
		t.Fatal(err)
	}
	responses[2] = testutil.Response{Stdout: string(data)}
	return responses
}

func updateSSHKey(t *testing.T) string {
	t.Helper()
	return "[127.0.0.1]:2300 " + sshHostPublicKey + "\n"
}
