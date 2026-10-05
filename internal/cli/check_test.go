package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestCheckReportsAnIntactRunningSandboxAndRecordedLimits(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, overridden := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/overridden-%t", host.name, overridden), func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				limits := map[string]string{"memory": "8589934592", "cpus": "4", "pids-limit": "2048", "shm-size": "1073741824"}
				if overridden {
					limits = map[string]string{"memory": "12884901888", "cpus": "2.5", "pids-limit": "512", "shm-size": "268435456"}
				}
				responses := checkObjectResponses(t, "default", "agent01", "default", true, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil, limits, "")
				scriptCheckObjects(fakes, host.windows, append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}))
				beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				lines := strings.Split(stdout, "\n")
				for _, want := range []string{
					"Sandbox agent01: state running",
					"Container sandboxed-agents.default.agent01: running, owner default",
					"Manager: the manager answered through podman exec",
					"SSH: the SSH setup is not installed; no SSH connection was attempted",
				} {
					if !slices.Contains(lines, want) {
						t.Errorf("output=%q lacks %q", stdout, want)
					}
				}
				for option, value := range limits {
					want := "Resource limit " + option + ": " + value
					if !slices.Contains(lines, want) {
						t.Errorf("output=%q lacks %q", stdout, want)
					}
				}
				for _, suffix := range []string{"workspace", "home", "ssh"} {
					want := "Volume sandboxed-agents.default.agent01." + suffix + ": present, owner default"
					if !slices.Contains(lines, want) {
						t.Errorf("missing volume %s in %q", suffix, stdout)
					}
				}
				assertCheckReadOnly(t, fakes, host.windows, true)
				assertNoSSH(t, fakes)
				if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
					t.Fatal("check changed host files")
				}
			})
		}
	}
}

func checkObjectResponses(t *testing.T, group, name, owner string, exists, running bool, volumes map[string]string, backup *string, limits map[string]string, bind string) []testutil.Response {
	t.Helper()
	responses := sandboxObjectResponses(nil, running, volumes, backup)
	if exists {
		responses = sandboxObjectResponses(&owner, running, volumes, backup)
	}
	for i := range responses {
		responses[i].Stdout = strings.ReplaceAll(responses[i].Stdout, "default.agent01", group+"."+name)
	}
	if exists {
		var records []map[string]any
		if err := json.Unmarshal([]byte(responses[1].Stdout), &records); err != nil {
			t.Fatal(err)
		}
		labels := records[0]["Config"].(map[string]any)["Labels"].(map[string]any)
		labels["io.github.sandboxed-agents.workspace-kind"] = "volume"
		for k, v := range limits {
			labels["io.github.sandboxed-agents."+k] = v
		}
		mounts := []map[string]string{}
		for _, suffix := range []string{"workspace", "home", "ssh"} {
			target := map[string]string{"workspace": "/workspace", "home": "/home/agent", "ssh": "/etc/ssh"}[suffix]
			if suffix == "workspace" && bind != "" {
				labels["io.github.sandboxed-agents.workspace-kind"] = "bind"
				mounts = append(mounts, map[string]string{"Type": "bind", "Source": bind, "Destination": target})
				continue
			}
			if _, present := volumes[suffix]; present {
				mounts = append(mounts, map[string]string{"Type": "volume", "Name": "sandboxed-agents." + group + "." + name + "." + suffix, "Destination": target})
			}
		}
		records[0]["Mounts"] = mounts
		data, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		responses[1].Stdout = string(data)
	}
	return responses
}

func scriptCheckObjects(fakes *testutil.FakePrograms, windows bool, responses []testutil.Response) {
	if windows {
		responses = append(append([]testutil.Response{}, healthyWindowsPodman()[1:3]...), responses...)
	}
	fakes.Script("podman", responses...)
}

func assertCheckReadOnly(t *testing.T, fakes *testutil.FakePrograms, windows, manager bool) {
	t.Helper()
	assertCheckCallsReadOnly(t, fakes.Calls("podman"), windows, manager)
	if len(fakes.Calls("ssh-keygen")) != 0 || len(fakes.Calls("ssh-keyscan")) != 0 {
		t.Fatal("check generated keys or scanned host keys")
	}
}

func assertCheckCallsReadOnly(t *testing.T, calls []testutil.Call, windows, manager bool) {
	t.Helper()
	var managerCalls int
	for _, call := range calls {
		args := call.Args
		if windows {
			if len(args) >= 2 && args[0] == "machine" && (args[1] == "list" || args[1] == "inspect") {
				continue
			}
			if len(args) < 2 || !reflect.DeepEqual(args[:2], []string{"--connection", "podman-machine-default"}) {
				t.Fatalf("unbound call: %v", args)
			}
			args = args[2:]
		}
		if len(args) == 3 && slices.Contains([]string{"container", "volume"}, args[0]) && slices.Contains([]string{"exists", "inspect"}, args[1]) {
			continue
		}
		if len(args) == 5 && args[0] == "exec" && args[1] == "--user=0:0" && args[3] == "/usr/local/bin/sandboxed-agents-manager" && args[4] == "version" {
			managerCalls++
			continue
		}
		t.Fatalf("check made a state-changing Podman call: %v", args)
	}
	if managerCalls > 1 || (manager && managerCalls != 1) || (!manager && managerCalls != 0) {
		t.Fatalf("manager calls=%d want attempted=%t", managerCalls, manager)
	}
}

func TestCheckReportsEveryProblemWithoutHidingOtherItems(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, answer := range []testutil.Response{{ExitCode: 1}, {Stdout: "not a manager version\n"}} {
			t.Run(fmt.Sprintf("%s/%s", host.name, answer.Stdout), func(t *testing.T) {
				fakes, fixture, _, _ := sshSetupHost(t, host.windows)
				responses := checkObjectResponses(t, "default", "agent01", "default", true, true, map[string]string{"workspace": "default", "ssh": "default"}, nil, map[string]string{"memory": "8589934592", "cpus": "4", "pids-limit": "2048", "shm-size": "1073741824"}, "")
				scriptCheckObjects(fakes, host.windows, append(responses, answer))
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				if status == 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, want := range []string{"problem", "sandboxed-agents.default.agent01.home", "missing", "manager does not answer", "cpus: 4", "sandboxed-agents.default.agent01.ssh", "not installed"} {
					if !strings.Contains(stdout, want) {
						t.Errorf("output=%q lacks %q", stdout, want)
					}
				}
				assertCheckReadOnly(t, fakes, host.windows, true)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestCheckStoppedAndVolumesOnlyAreSuccessfulStates(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, test := range []struct {
			name      string
			container bool
			volumes   map[string]string
		}{
			{"stopped", true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}},
			{"all volumes", false, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}},
			{"home only", false, map[string]string{"home": "default"}},
			{"workspace only", false, map[string]string{"workspace": "default"}},
			{"ssh only", false, map[string]string{"ssh": "default"}},
		} {
			t.Run(host.name+"/"+test.name, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", "default", test.container, false, test.volumes, nil, nil, ""))
				beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				want := "volumes only"
				if test.container {
					want = "stopped"
				}
				if status != 0 || stderr != "" || !strings.Contains(stdout, want) || strings.Contains(stdout, "problem:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for suffix := range test.volumes {
					if !strings.Contains(stdout, "sandboxed-agents.default.agent01."+suffix+": present, owner default") {
						t.Errorf("volume %s missing in %q", suffix, stdout)
					}
				}
				if !strings.Contains(stdout, "running sandbox") {
					t.Errorf("skipped manager lacks reason: %q", stdout)
				}
				assertCheckReadOnly(t, fakes, host.windows, false)
				assertNoSSH(t, fakes)
				if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
					t.Fatal("check changed files")
				}
			})
		}
	}
}

func TestCheckReportsOwnerConflictsAndBackupsTogether(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, container := range []bool{false, true} {
			for _, object := range []string{"container", "home", "workspace", "ssh", "backup"} {
				if object == "container" && !container {
					continue
				}
				for _, owner := range []string{"", "foreign"} {
					t.Run(fmt.Sprintf("%s/container-%t/%s/owner-%s", host.name, container, object, owner), func(t *testing.T) {
						fakes, fixture, _, _ := sshSetupHost(t, host.windows)
						volumes := map[string]string{"workspace": "default", "home": "default", "ssh": "default"}
						containerOwner, backupOwner := "default", "default"
						switch object {
						case "container":
							containerOwner = owner
						case "backup":
							backupOwner = owner
						default:
							volumes[object] = owner
						}
						scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", containerOwner, container, true, volumes, &backupOwner, nil, ""))
						stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
						affected := "sandboxed-agents.default.agent01"
						if object == "backup" {
							affected = "sandboxed-agents-backup.default.agent01"
						} else if object != "container" {
							affected += "." + object
						}
						for _, want := range []string{"owner conflict", affected, "Podman", "remove or rename", "interrupted update", "update agent01"} {
							if !strings.Contains(stdout, want) {
								t.Errorf("output=%q lacks %q", stdout, want)
							}
						}
						if status == 0 || !strings.Contains(stderr, "agent01") || strings.Contains(stdout, "state volumes only") {
							t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
						}
						assertCheckReadOnly(t, fakes, host.windows, false)
						assertNoSSH(t, fakes)
					})
				}
			}
		}
	}
}

func TestCheckReportsInterruptedUpdatesWithAndWithoutTheOriginalContainer(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, container := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/container-%t", host.name, container), func(t *testing.T) {
				fakes, fixture, _, _ := sshSetupHost(t, host.windows)
				owner := "default"
				responses := checkObjectResponses(t, "default", "agent01", owner, container, false, nil, &owner, nil, "")
				scriptCheckObjects(fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				if status == 0 || !strings.Contains(stderr, "agent01") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, want := range []string{"update interrupted", "sandboxed-agents-backup.default.agent01", "update agent01", "backup.default.agent01: present, owner default"} {
					if !strings.Contains(stdout, want) {
						t.Errorf("output=%q lacks %q", stdout, want)
					}
				}
				assertCheckReadOnly(t, fakes, host.windows, false)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestCheckNamesWorkspaceBindsAndUnusedVolumes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, kept := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/kept-%t", host.name, kept), func(t *testing.T) {
				fakes, fixture, _, _ := sshSetupHost(t, host.windows)
				volumes := map[string]string{"home": "default", "ssh": "default"}
				if kept {
					volumes["workspace"] = "default"
				}
				scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", "default", true, false, volumes, nil, nil, "/projects/my workspace"))
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				if status != 0 || stderr != "" || !strings.Contains(stdout, "bind of /projects/my workspace") || strings.Contains(stdout, "problem:") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if kept && !strings.Contains(stdout, "sandboxed-agents.default.agent01.workspace: unused, owner default") {
					t.Errorf("unused workspace volume not named: %q", stdout)
				}
				assertCheckReadOnly(t, fakes, host.windows, false)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestCheckRefusesUsageErrorsBeforePodmanAndNamesUnknownSandboxes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, args := range [][]string{{"agent01", "extra"}, {"agent01", "--force"}, {"--all"}, {"bad/name"}, {""}} {
			t.Run(host.name+"/"+strings.Join(args, "+"), func(t *testing.T) {
				fakes, fixture, _, _ := sshSetupHost(t, host.windows)
				stdout, stderr, status := runCLI(t, fixture, append([]string{"check"}, args...)...)
				if status == 0 || !strings.Contains(stderr, "Usage:") || stdout != "" || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				assertNoSSH(t, fakes)
			})
		}
		t.Run(host.name+"/invalid group", func(t *testing.T) {
			fakes, fixture, _, _ := sshSetupHost(t, host.windows)
			t.Setenv("SANDBOXED_AGENTS_GROUP", "Invalid")
			stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
			if status == 0 || stdout != "" || !strings.Contains(stderr, "controller group") || len(fakes.Calls("podman")) != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
		})
		t.Run(host.name+"/unknown sandbox", func(t *testing.T) {
			fakes, fixture, _, _ := sshSetupHost(t, host.windows)
			scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "nosuch", "default", false, false, nil, nil, nil, ""))
			stdout, stderr, status := runCLI(t, fixture, "check", "nosuch")
			if status == 0 || !strings.Contains(stderr, "nosuch does not exist in this controller group") || stdout != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertCheckReadOnly(t, fakes, host.windows, false)
			assertNoSSH(t, fakes)
		})
	}
}

func TestCheckSSHUsesInstalledHostEntryAndLeavesFilesUnchanged(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, group := range []string{"default", "team-a"} {
			for _, test := range []struct {
				name                          string
				exists, running, managerFails bool
				sshStatus                     int
			}{
				{name: "connection succeeds", exists: true, running: true},
				{name: "connection fails", exists: true, running: true, sshStatus: 255},
				{name: "manager fails but SSH still checked", exists: true, running: true, managerFails: true},
				{name: "stopped", exists: true},
				{name: "stale setup without container"},
			} {
				t.Run(host.name+"/"+group+"/"+test.name, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", group)
					beforePodman, beforeSSHCalls := len(fakes.Calls("podman")), len(fakes.Calls("ssh"))
					responses := checkObjectResponses(t, group, "agent01", group, test.exists, test.running, map[string]string{"workspace": group, "home": group, "ssh": group}, nil, nil, "")
					if test.running {
						answer := testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}
						if test.managerFails {
							answer.ExitCode = 1
						}
						responses = append(responses, answer)
					}
					scriptCheckObjects(fakes, host.windows, responses)
					fakes.Script("ssh", testutil.Response{ExitCode: test.sshStatus, Stderr: "connection refused"})
					beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
					stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
					problem := test.sshStatus != 0 || test.managerFails || !test.exists
					if (status != 0) != problem {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					calls := fakes.Calls("ssh")[beforeSSHCalls:]
					if !test.running {
						if len(calls) != 0 {
							t.Fatalf("SSH connection attempted: %v", calls)
						}
						want := "running sandbox"
						if !test.exists {
							want = "ssh-config agent01 --remove"
						}
						if !strings.Contains(stdout, want) {
							t.Errorf("output=%q lacks %q", stdout, want)
						}
					} else {
						alias := "agent01"
						if group != "default" {
							alias += "." + group
						}
						if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args[len(calls[0].Args)-2:], []string{alias, "true"}) {
							t.Fatalf("SSH did not use sandbox host entry: %v", calls)
						}
						args := strings.Join(calls[0].Args, " ")
						for _, want := range []string{"BatchMode=yes", "ConnectTimeout=5", "StrictHostKeyChecking=yes", "UpdateHostKeys=no", "ControlMaster=no", "ControlPath=none", "ClearAllForwardings=yes", "PermitLocalCommand=no"} {
							if !strings.Contains(args, want) {
								t.Errorf("SSH lacks %q: %s", want, args)
							}
						}
						want := "succeeded"
						if test.sshStatus != 0 {
							want = "failed"
						}
						if !strings.Contains(stdout, "host entry "+alias) || !strings.Contains(stdout, want) {
							t.Errorf("SSH result not reported: %q", stdout)
						}
					}
					assertCheckCallsReadOnly(t, fakes.Calls("podman")[beforePodman:], host.windows, test.running)
					if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
						t.Fatal("check changed SSH setup or host state")
					}
				})
			}
		}
	}
}

func TestCheckReportsAnExistingVolumeThatTheContainerDoesNotMount(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, _, _ := sshSetupHost(t, host.windows)
			responses := checkObjectResponses(t, "default", "agent01", "default", true, true, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil, nil, "")
			var records []map[string]any
			if err := json.Unmarshal([]byte(responses[1].Stdout), &records); err != nil {
				t.Fatal(err)
			}
			mounts := records[0]["Mounts"].([]any)
			records[0]["Mounts"] = []any{mounts[0], mounts[2]}
			data, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			responses[1].Stdout = string(data)
			scriptCheckObjects(fakes, host.windows, append(responses, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}))
			stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
			if status == 0 || !strings.Contains(stdout, "required volume sandboxed-agents.default.agent01.home is missing") || !strings.Contains(stdout, "manager answered") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertCheckReadOnly(t, fakes, host.windows, true)
		})
	}
}

func TestCheckOwnerConflictsWithoutABackupRemainFindings(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, container := range []bool{false, true} {
			for _, owner := range []string{"", "other"} {
				t.Run(fmt.Sprintf("%s/container-%t/owner-%s", host.name, container, owner), func(t *testing.T) {
					fakes, fixture, _, _ := sshSetupHost(t, host.windows)
					scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", "default", container, true, map[string]string{"home": owner}, nil, nil, ""))
					stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
					if status == 0 || !strings.Contains(stdout, "state owner conflict") || !strings.Contains(stdout, "sandboxed-agents.default.agent01.home") || !strings.Contains(stdout, "Podman") || strings.Contains(stdout, "state volumes only") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					assertCheckReadOnly(t, fakes, host.windows, false)
					assertNoSSH(t, fakes)
				})
			}
		}
	}
}

func TestCheckUsesOnlyTheCurrentGroupsPodmanNames(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, name := range []string{"check", "agent.with.dots", "Agent-01"} {
			t.Run(host.name+"/"+name, func(t *testing.T) {
				fakes, fixture, _, _ := sshSetupHost(t, host.windows)
				t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
				responses := checkObjectResponses(t, "team-a", name, "team-a", false, false, map[string]string{"home": "team-a"}, nil, nil, "")
				if host.windows {
					for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
						t.Setenv(key, "ambient-other-target")
					}
					for i := range responses {
						responses[i].AbsentEnv = []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"}
					}
				}
				scriptCheckObjects(fakes, host.windows, responses)
				stdout, stderr, status := runCLI(t, fixture, "check", name)
				if status != 0 || stderr != "" || !strings.Contains(stdout, "sandboxed-agents.team-a."+name+".home: present, owner team-a") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, call := range fakes.Calls("podman") {
					if strings.Contains(strings.Join(call.Args, " "), "sandboxed-agents.default.") {
						t.Fatalf("queried another group's sandbox: %v", call.Args)
					}
				}
				assertCheckReadOnly(t, fakes, host.windows, false)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestCheckSSHCleanupAdviceRespectsBackupsAndOwnerConflicts(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, test := range []struct {
			name            string
			backup, foreign bool
		}{
			{name: "backup", backup: true}, {name: "foreign volume", foreign: true}, {name: "both", backup: true, foreign: true},
		} {
			t.Run(host.name+"/"+test.name, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
				beforePodman, beforeSSHCalls := len(fakes.Calls("podman")), len(fakes.Calls("ssh"))
				volumes := map[string]string{"home": "default"}
				if test.foreign {
					volumes["home"] = "other"
				}
				var backup *string
				if test.backup {
					owner := "default"
					backup = &owner
				}
				scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", "default", false, false, volumes, backup, nil, ""))
				beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
				stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
				if status == 0 || !strings.Contains(stdout, "SSH setup") || !strings.Contains(stdout, "no container") || strings.Contains(stdout, "ssh-config agent01 --remove") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				want := "update agent01"
				if test.foreign {
					want = "remove or rename each foreign object with Podman"
				}
				if !strings.Contains(stdout, want) {
					t.Errorf("output=%q lacks %q", stdout, want)
				}
				if len(fakes.Calls("ssh")) != beforeSSHCalls {
					t.Fatal("check attempted SSH without a container")
				}
				assertCheckCallsReadOnly(t, fakes.Calls("podman")[beforePodman:], host.windows, false)
				if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
					t.Fatal("check changed files")
				}
			})
		}
	}
}

func TestCheckStoppedSandboxDoesNotAddAnSSHIntegrityCheck(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
			if err := os.Remove(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031", "entry")); err != nil {
				t.Fatal(err)
			}
			beforePodman, beforeSSHCalls := len(fakes.Calls("podman")), len(fakes.Calls("ssh"))
			scriptCheckObjects(fakes, host.windows, checkObjectResponses(t, "default", "agent01", "default", true, false, map[string]string{"workspace": "default", "home": "default", "ssh": "default"}, nil, nil, ""))
			beforeSSH, beforeState := sshDirectoryContents(t, sshDir), managedSSHFiles(t, state)
			stdout, stderr, status := runCLI(t, fixture, "check", "agent01")
			if status != 0 || stderr != "" || !strings.Contains(stdout, "running sandbox") || strings.Contains(stdout, "problem:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("ssh")) != beforeSSHCalls {
				t.Fatal("SSH connection attempted on stopped sandbox")
			}
			assertCheckCallsReadOnly(t, fakes.Calls("podman")[beforePodman:], host.windows, false)
			if !reflect.DeepEqual(beforeSSH, sshDirectoryContents(t, sshDir)) || !reflect.DeepEqual(beforeState, managedSSHFiles(t, state)) {
				t.Fatal("check changed files")
			}
		})
	}
}
