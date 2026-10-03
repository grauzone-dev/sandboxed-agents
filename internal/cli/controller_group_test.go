package cli_test

import (
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestInvalidControllerGroupFailsEveryCommandBeforeExternalCalls(t *testing.T) {
	for _, group := range []string{"", "Team", "a.b", "-a", "a/b", "a b"} {
		for _, args := range [][]string{{"up", "agent01"}, {"up", "--help"}, {"start", "agent01"}, {"stop", "agent01"}, {"restart", "agent01"}, {"remove", "agent01"}, {"shell", "agent01"}, {"list"}, {"build"}, {"check"}, {"version"}} {
			t.Run(group+"/"+args[0], func(t *testing.T) {
				t.Setenv("SANDBOXED_AGENTS_GROUP", group)
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, "sandbox-host", args...)
				if status == 0 || !strings.Contains(stderr, "invalid controller group") || !strings.Contains(stderr, group) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(fakes.Calls("podman")) != 0 {
					t.Fatal("invalid group called Podman")
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestSelectedControllerGroupCreatesItsOwnSandboxAndVolumes(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
	fakes := linuxHost(t)
	fakes.Script("podman", testutil.Response{Stdout: "podman version 5.0.0\n"}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{})
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01")
	if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, call := range fakes.Calls("podman") {
		joined := strings.Join(call.Args, " ")
		if strings.Contains(joined, "sandboxed-agents.default.") || strings.Contains(joined, "owner=default") {
			t.Fatalf("other group accessed: %v", call.Args)
		}
		if len(call.Args) > 1 && (call.Args[0] == "create" || (call.Args[0] == "volume" && call.Args[1] == "create")) {
			if !strings.Contains(joined, "io.github.sandboxed-agents.owner=team-a") || !strings.Contains(joined, "sandboxed-agents.team-a.agent01") {
				t.Fatalf("incorrect group: %v", call.Args)
			}
		}
	}
	assertNoSSH(t, fakes)
}

func TestOtherControllerGroupSandboxIsUnknown(t *testing.T) {
	for _, command := range []string{"start", "stop", "restart", "remove", "shell"} {
		t.Run(command, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			fakes := testutil.NewFakePrograms(t)
			fakes.Script("podman", testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1}, testutil.Response{ExitCode: 1})
			stdout, stderr, status := runCLI(t, "sandbox-host", command, "agent01")
			if status == 0 || !strings.Contains(stderr, "agent01") || !strings.Contains(stderr, "exist") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, call := range fakes.Calls("podman") {
				if !strings.Contains(strings.Join(call.Args, " "), ".team-a.agent01") {
					t.Fatalf("another group accessed: %v", call.Args)
				}
			}
			assertPodmanReadOnly(t, fakes)
			assertNoSSH(t, fakes)
		})
	}
}

func TestSelectedControllerGroupChecksAllObjectOwners(t *testing.T) {
	for _, command := range []string{"up", "start", "stop", "restart", "remove", "shell"} {
		for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
			t.Run(command+"/"+object, func(t *testing.T) {
				t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
				fakes := linuxHost(t)
				owner := "team-a"
				foreign := "default"
				containerOwner := &owner
				volumes := map[string]string{"workspace": owner, "home": owner, "ssh": owner}
				var backupOwner *string
				expected := "sandboxed-agents.team-a.agent01"
				switch object {
				case "container":
					containerOwner = &foreign
				case "backup":
					backupOwner = &foreign
					expected = "sandboxed-agents-backup.team-a.agent01"
				default:
					volumes[object] = foreign
					expected += "." + object
				}
				responses := sandboxObjectResponses(containerOwner, true, volumes, backupOwner)
				for i := range responses {
					responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, "sandboxed-agents.default.", "sandboxed-agents.team-a.")
					responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, "sandboxed-agents-backup.default.", "sandboxed-agents-backup.team-a.")
				}
				fixture := "sandbox-host"
				if command == "up" {
					fixture = "linux-preflight"
					responses = append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, command, "agent01")
				if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, expected) || !strings.Contains(stderr, "Podman") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertPodmanReadOnly(t, fakes)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestBackupOnlySandboxRefusesChangesAndNamesRecovery(t *testing.T) {
	for _, command := range []string{"up", "start", "stop", "restart", "remove", "shell"} {
		for _, owner := range []string{"default", "foreign", ""} {
			t.Run(command+"/"+owner, func(t *testing.T) {
				fakes := linuxHost(t)
				responses := sandboxObjectResponses(nil, false, nil, &owner)
				fixture := "sandbox-host"
				if command == "up" {
					fixture = "linux-preflight"
					responses = append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, command, "agent01")
				want := "update agent01"
				if owner != "default" {
					want = "owner conflict"
				}
				if status == 0 || !strings.Contains(stderr, want) || !strings.Contains(stderr, "sandboxed-agents-backup.default.agent01") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if owner != "default" && !strings.Contains(stderr, "Podman") {
					t.Fatalf("missing conflict recovery: %q", stderr)
				}
				assertPodmanReadOnly(t, fakes)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestNamedCommandsReportEveryForeignObjectBeforeInterruptedUpdate(t *testing.T) {
	for _, command := range []string{"up", "start", "stop", "restart", "remove", "shell"} {
		t.Run(command, func(t *testing.T) {
			fakes := linuxHost(t)
			foreign := "foreign"
			responses := sandboxObjectResponses(&foreign, true, map[string]string{"workspace": "", "home": foreign, "ssh": "default"}, &foreign)
			fixture := "sandbox-host"
			if command == "up" {
				fixture = "linux-preflight"
				responses = append([]testutil.Response{{Stdout: "podman version 5.0.0\n"}}, responses...)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, fixture, command, "agent01")
			if status == 0 || !strings.Contains(stderr, "owner conflict") || strings.Contains(stderr, "interrupted update") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, name := range []string{"sandboxed-agents.default.agent01", "sandboxed-agents.default.agent01.workspace", "sandboxed-agents.default.agent01.home", "sandboxed-agents-backup.default.agent01"} {
				if !strings.Contains(stderr, name) {
					t.Fatalf("missing foreign object %s: %q", name, stderr)
				}
			}
			assertPodmanReadOnly(t, fakes)
			assertNoSSH(t, fakes)
		})
	}
}

func TestTwoControllerGroupsCreateSameSandboxNameFromDifferentDirectories(t *testing.T) {
	fakes := linuxHost(t)
	creation := []testutil.Response{{Stdout: "podman version 5.0.0\n"}, {ExitCode: 1}, {ExitCode: 1}, {ExitCode: 1}, {ExitCode: 1}, {ExitCode: 1}, {}, {}, {}, {}, {}, {}}
	responses := append(append([]testutil.Response{}, creation...), creation...)
	fakes.Script("podman", responses...)
	for _, group := range []string{"default", "team-a"} {
		t.Setenv("SANDBOXED_AGENTS_GROUP", group)
		stdout, stderr, status := runCLIAt(t, t.TempDir(), "linux-preflight", "up", "agent01")
		if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
			t.Fatalf("group=%s status=%d stdout=%q stderr=%q", group, status, stdout, stderr)
		}
	}
	var names []string
	for _, call := range fakes.Calls("podman") {
		if len(call.Args) > 2 && call.Args[0] == "create" {
			names = append(names, call.Args[2])
		}
	}
	if strings.Join(names, ",") != "sandboxed-agents.default.agent01,sandboxed-agents.team-a.agent01" {
		t.Fatalf("container names=%v", names)
	}
	assertNoSSH(t, fakes)
}
