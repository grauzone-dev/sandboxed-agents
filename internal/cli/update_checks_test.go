package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpdateReportsPreflightFailureBeforeUnknownSandboxOrOwnerConflict(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, sandboxState := range []string{"unknown", "foreign", "interrupted"} {
			t.Run(host.name+"/"+sandboxState, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				checkSSH := installUpdateSSHFixture(t, sshDir, state)
				defer checkSSH()
				var responses []testutil.Response
				if sandboxState == "unknown" {
					responses = upObjectResponses(nil, false, nil, nil)
				} else if sandboxState == "interrupted" {
					responses = recoveryObjectResponses(t, true, false, false, "true")
				} else {
					responses = updateObjectResponses(t, false, "old-image", "", "")
					responses = updateObjectOwner(t, responses, "sandboxed-agents.default.agent01", "another-group")
				}
				preflightCalls := 1
				if host.windows {
					preflight := healthyWindowsPodman()
					preflight[0].Stdout = "podman version 4.9.9\n"
					responses = append(preflight, responses[1:]...)
					preflightCalls = resourceWindowsPreflightCalls
				} else {
					t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, "prerequisites") || strings.Contains(stderr, "owner conflict") || strings.Contains(stderr, "does not exist") || strings.Contains(stderr, "has no container") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if calls := fakes.Calls("podman"); len(calls) != preflightCalls {
					t.Fatalf("failed preflight inspected sandbox: %v", calls)
				} else if host.windows {
					assertReadOnlyPodmanCalls(t, calls)
				} else if len(calls[0].Args) != 1 || calls[0].Args[0] != "--version" {
					t.Fatalf("failed preflight inspected sandbox: %v", calls)
				}
				assertNoSSHBesidesReadiness(t, fakes)
				if len(updateReadinessProbes(fakes)) != 0 {
					t.Fatal("failed preflight attempted SSH readiness")
				}
			})
		}
	}
}

func TestUpdateRejectsInvalidNamesAndExtraArgumentsBeforeHostQueries(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, args := range [][]string{{"update"}, {"update", ""}, {"update", "-x"}, {"update", ".invalid"}, {"update", "a/b"}, {"update", "a b"}, {"update", "agent01", "extra"}, {"update", "agent01", "--unknown"}, {"update", "agent01", "--ssh"}, {"update", "agent01", "--toolchain", "native"}} {
			t.Run(host.name+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT", "1")
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if len(args) == 2 && !strings.Contains(stderr, "invalid sandbox name") {
					t.Fatalf("missing name diagnostic: %q", stderr)
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

func TestUpdateRefusesUnknownAndVolumesOnlySandboxesWithoutBuilding(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, state := range []struct {
			name    string
			volumes map[string]string
			want    string
		}{{name: "unknown", want: "does not exist"}, {name: "owned volumes", volumes: map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, want: "sandboxed-agents up agent01"}, {name: "foreign volumes", volumes: map[string]string{"workspace": "default", "home": "another-group", "ssh": "default"}, want: "owner conflict"}, {name: "missing volume owner", volumes: map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, want: "owner conflict"}} {
			t.Run(host.name+"/"+state.name, func(t *testing.T) {
				fakes, fixture := resourceLimitHost(t, host.windows)
				responses := upObjectResponses(nil, false, state.volumes, nil)
				if state.name == "missing volume owner" {
					responses = updateObjectOwner(t, responses, "sandboxed-agents.default.agent01.ssh", "")
				}
				scriptUpdate(t, fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
				if status == 0 || !strings.Contains(stderr, state.want) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if state.want == "owner conflict" && strings.Contains(stderr, "has no container") {
					t.Fatalf("reported missing container before owner conflict: %q", stderr)
				}
				assertUpdateChecksReadOnly(t, fakes, host.windows, fakes.Calls("podman"))
			})
		}
	}
}

func TestUpdateRefusesMissingAndForeignOwnersOnEverySandboxObject(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, owner := range []string{"", "another-group"} {
			for _, object := range []string{"container", "workspace", "home", "ssh", "backup"} {
				t.Run(host.name+"/"+owner+"/"+object, func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					responses := updateObjectResponses(t, true, "old-image", "", "")
					name := "sandboxed-agents.default.agent01"
					if object == "backup" {
						owned := "default"
						backup := sandboxObjectResponses(nil, false, nil, &owned)
						responses = append(responses[:len(responses)-1], backup[len(backup)-2:]...)
						name = "sandboxed-agents-backup.default.agent01"
					} else if object != "container" {
						name += "." + object
					}
					responses = updateObjectOwner(t, responses, name, owner)
					scriptUpdate(t, fakes, host.windows, responses)
					stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
					if status == 0 || !strings.Contains(stderr, "owner conflict") || !strings.Contains(stderr, name) || strings.Contains(stderr, "interrupted update") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					assertUpdateChecksReadOnly(t, fakes, host.windows, fakes.Calls("podman"))
				})
			}
		}
	}
}

func TestUpdateReleasesItsLifecycleLockAfterFailureAndReadsCurrentSandboxState(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture := resourceLimitHost(t, host.windows)
			responses := updateObjectResponses(t, false, "old-image", "", "")
			responses = updateObjectOwner(t, responses, "sandboxed-agents.default.agent01", "another-group")
			scriptUpdate(t, fakes, host.windows, responses)
			_, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status == 0 || !strings.Contains(stderr, "owner conflict") {
				t.Fatalf("first status=%d stderr=%q", status, stderr)
			}
			before := len(fakes.Calls("podman"))
			responses = updateObjectResponses(t, false, "current-image", "", "")
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"current-image"}]`})
			scriptUpdate(t, fakes, host.windows, responses)
			stdout, stderr, status := runCLI(t, fixture, "update", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "already up to date") {
				t.Fatalf("second status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")[before:]
			if host.windows {
				if len(calls) <= resourceWindowsPreflightCalls {
					t.Fatalf("second command omitted sandbox lookup: %v", calls)
				}
				calls = windowsOperationCalls(t, calls[resourceWindowsPreflightCalls:], "podman-machine-default")
			} else {
				if len(calls) <= 1 {
					t.Fatalf("second command omitted sandbox lookup: %v", calls)
				}
				calls = calls[1:]
			}
			if !slices.Equal(calls[0].Args, []string{"container", "exists", "sandboxed-agents.default.agent01"}) || !slices.Equal(calls[1].Args, []string{"container", "inspect", "sandboxed-agents.default.agent01"}) {
				t.Fatalf("second command did not read current sandbox state: %v", calls)
			}
			for _, call := range calls {
				if len(call.Args) != 3 || !slices.Contains([]string{"container", "volume", "image"}, call.Args[0]) || !slices.Contains([]string{"exists", "inspect"}, call.Args[1]) {
					t.Fatalf("already-current sandbox mutated: %v", call.Args)
				}
			}
			assertNoSSHBesidesReadiness(t, fakes)
			if len(updateReadinessProbes(fakes)) != 0 {
				t.Fatal("already-current sandbox attempted SSH readiness")
			}
		})
	}
}

func assertUpdateChecksReadOnly(t *testing.T, fakes *testutil.FakePrograms, windows bool, calls []testutil.Call) {
	t.Helper()
	if windows {
		if len(calls) <= resourceWindowsPreflightCalls {
			t.Fatalf("sandbox checks did not follow preflight: %v", calls)
		}
		assertReadOnlyPodmanCalls(t, calls[:resourceWindowsPreflightCalls])
		calls = windowsOperationCalls(t, calls[resourceWindowsPreflightCalls:], "podman-machine-default")
	} else {
		if len(calls) <= 1 || !slices.Equal(calls[0].Args, []string{"--version"}) {
			t.Fatalf("sandbox checks did not follow preflight: %v", calls)
		}
		calls = calls[1:]
	}
	for _, call := range calls {
		if len(call.Args) != 3 || !slices.Contains([]string{"container", "volume"}, call.Args[0]) || !slices.Contains([]string{"exists", "inspect"}, call.Args[1]) {
			t.Fatalf("refused update changed or built objects: %v", call.Args)
		}
	}
	assertNoSSHBesidesReadiness(t, fakes)
	if len(updateReadinessProbes(fakes)) != 0 {
		t.Fatal("refused update attempted SSH readiness")
	}
}

func updateObjectOwner(t *testing.T, responses []testutil.Response, name, owner string) []testutil.Response {
	t.Helper()
	return updateObjectLabels(t, responses, name, func(labels map[string]any) {
		if owner == "" {
			delete(labels, "io.github.sandboxed-agents.owner")
		} else {
			labels["io.github.sandboxed-agents.owner"] = owner
		}
	})
}

func updateObjectLabels(t *testing.T, responses []testutil.Response, name string, modify func(map[string]any)) []testutil.Response {
	t.Helper()
	responses = slices.Clone(responses)
	found := false
	for index := range responses {
		if !strings.HasPrefix(strings.TrimSpace(responses[index].Stdout), "[") {
			continue
		}
		var records []map[string]any
		if err := json.Unmarshal([]byte(responses[index].Stdout), &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record["Name"] != name {
				continue
			}
			labels, ok := record["Labels"].(map[string]any)
			if !ok {
				config, ok := record["Config"].(map[string]any)
				if !ok {
					t.Fatalf("object %s has no config", name)
				}
				labels, ok = config["Labels"].(map[string]any)
				if !ok {
					t.Fatalf("object %s has no labels", name)
				}
			}
			modify(labels)
			data, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			responses[index].Stdout = string(data)
			found = true
		}
	}
	if !found {
		t.Fatalf("object %s absent from responses", name)
	}
	return responses
}
