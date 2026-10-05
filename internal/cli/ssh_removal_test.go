package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func installSSHForRemoval(t *testing.T, fakes *testutil.FakePrograms, fixture string, windows bool, name, group string) {
	t.Helper()
	t.Setenv("SANDBOXED_AGENTS_GROUP", group)
	scriptSSHInstall(t, fakes, windows, name, group, true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{Stdout: sshHostPublicKey + "\n"}, testutil.Response{WantStdin: sshClientPublicKey + "\n"})
	hostName := name
	if group != "default" {
		hostName += "." + group
	}
	scriptSSHDefaults(fakes, hostName)
	stdout, stderr, status := runCLI(t, fixture, "ssh-config", name, "--install")
	if status != 0 {
		t.Fatalf("install: status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestSSHRemovalPreservesOtherSandboxAndRevokesRunningAuthorization(t *testing.T) {
	for _, host := range resourceLimitHosts {
		t.Run(host.name, func(t *testing.T) {
			fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
			installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
			installSSHForRemoval(t, fakes, fixture, host.windows, "agent02", "default")
			keyDir := filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")
			otherBefore := managedSSHFiles(t, filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743032"))
			userBefore := sshDirectoryContents(t, sshDir)
			otherEntry, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743032", "entry"))
			if err != nil {
				t.Fatal(err)
			}
			beforeCalls := len(fakes.Calls("podman"))
			scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{})
			stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if _, err := os.Stat(keyDir); !os.IsNotExist(err) {
				t.Fatalf("key directory remains: %v", err)
			}
			if !reflect.DeepEqual(otherBefore, managedSSHFiles(t, filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743032"))) || !reflect.DeepEqual(userBefore, sshDirectoryContents(t, sshDir)) {
				t.Fatal("other sandbox or Include changed")
			}
			config, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "config"))
			if err != nil || string(config) != string(otherEntry)+"Host *\n" {
				t.Fatalf("managed config=%q err=%v", config, err)
			}
			var mutations [][]string
			for _, call := range fakes.Calls("podman")[beforeCalls:] {
				if strings.Contains(strings.Join(call.Args, " "), " exec ") || call.Args[0] == "exec" {
					mutations = append(mutations, call.Args)
				}
				for _, arg := range call.Args {
					if arg == "rm" || arg == "stop" || arg == "start" || arg == "create" {
						t.Fatalf("unexpected mutation: %v", call.Args)
					}
				}
			}
			prefix := []string{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "/usr/local/bin/sandboxed-agents-manager"}
			if host.windows {
				prefix = append([]string{"--connection", "podman-machine-default"}, prefix...)
			}
			want := [][]string{append(append([]string{}, prefix...), "version"), append(append([]string{}, prefix...), "ssh", "deauthorize")}
			if !reflect.DeepEqual(mutations, want) {
				t.Fatalf("manager calls=%v want=%v", mutations, want)
			}
		})
	}
}

func TestRemoveSandboxCleansSSHWithoutRevokingAuthorization(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, running := range []bool{false, true} {
			for _, volumes := range []bool{false, true} {
				t.Run(host.name+"/running-"+strconv.FormatBool(running)+"/volumes-"+strconv.FormatBool(volumes), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					owned := "default"
					responses := sandboxObjectResponses(&owned, running, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
					if running {
						responses = append(responses, testutil.Response{Stdout: "[]"}, testutil.Response{})
					}
					responses = append(responses, testutil.Response{})
					if volumes {
						responses = append(responses, make([]testutil.Response, 3)...)
					}
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					before := len(fakes.Calls("podman"))
					args := []string{"remove", "agent01"}
					if volumes {
						args = append(args, "--volumes")
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if _, err := os.Stat(filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")); !os.IsNotExist(err) {
						t.Fatalf("SSH state remains: %v", err)
					}
					config, err := os.ReadFile(filepath.Join(sshDir, "config"))
					if err != nil || len(config) != 0 {
						t.Fatalf("Include remains: %q err=%v", config, err)
					}
					if !volumes && (!strings.Contains(stdout, "remains authorized") || !strings.Contains(stdout, "--install")) {
						t.Fatalf("missing kept authorization note: %q", stdout)
					}
					for _, call := range fakes.Calls("podman")[before:] {
						if slices.Contains(call.Args, "deauthorize") {
							t.Fatalf("sandbox remove revoked authorization: %v", call.Args)
						}
					}
				})
			}
		}
	}
}

func TestSSHRemovalCleansHostWhenStoppedOrManagerFails(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, condition := range []string{"stopped", "manager unavailable", "deauthorization failed"} {
			t.Run(host.name+"/"+condition, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
				running := condition != "stopped"
				var extra []testutil.Response
				if condition == "manager unavailable" {
					extra = []testutil.Response{{ExitCode: 1}}
				}
				if condition == "deauthorization failed" {
					extra = []testutil.Response{{Stdout: "sandboxed-agents-manager v1\n"}, {ExitCode: 1, Stderr: "fixture failed"}}
				}
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", running, extra...)
				before := len(fakes.Calls("podman"))
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
				if (status == 0) != !running {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if running && (!strings.Contains(stderr, "could not remove the authorization") || !strings.Contains(stderr, "check agent01") || !strings.Contains(stderr, "restart agent01")) {
					t.Fatalf("missing authorization failure: %q", stderr)
				}
				if !running && (!strings.Contains(stdout, "remains authorized") || !strings.Contains(stdout, "--install")) {
					t.Fatalf("missing retained authorization: %q", stdout)
				}
				if managedSSHFiles(t, state) != nil {
					t.Fatalf("state remains: %v", managedSSHFiles(t, state))
				}
				user, err := os.ReadFile(filepath.Join(sshDir, "config"))
				if err != nil || len(user) != 0 {
					t.Fatalf("config=%q err=%v", user, err)
				}
				for _, call := range fakes.Calls("podman")[before:] {
					if slices.Contains(call.Args, "rm") || slices.Contains(call.Args, "start") || slices.Contains(call.Args, "stop") || (!running && slices.Contains(call.Args, "exec")) {
						t.Fatalf("unexpected mutation: %v", call.Args)
					}
				}
			})
		}
	}
}

func TestSSHRemovalRestoresPersonalConfigAndPreservesOtherControllerGroup(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, otherGroup := range []bool{false, true} {
			t.Run(host.name+"/other-"+strconv.FormatBool(otherGroup), func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				if err := os.MkdirAll(sshDir, 0700); err != nil {
					t.Fatal(err)
				}
				personal := []byte("# Personal configuration\r\nHost personal\r\n  HostName personal.example\r\n# End without newline")
				userPath := filepath.Join(sshDir, "config")
				if err := os.WriteFile(userPath, personal, 0640); err != nil {
					t.Fatal(err)
				}
				if otherGroup {
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "live")
				}
				expected, err := os.ReadFile(userPath)
				if err != nil {
					t.Fatal(err)
				}
				other := managedSSHFiles(t, filepath.Join(state, "group-live"))
				installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", false)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				actual, err := os.ReadFile(userPath)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("config=%q want=%q err=%v", actual, expected, err)
				}
				if !reflect.DeepEqual(other, managedSSHFiles(t, filepath.Join(state, "group-live"))) {
					t.Fatal("other controller group changed")
				}
				if _, err := os.Stat(filepath.Join(state, "group-default", "ssh", "config")); !os.IsNotExist(err) {
					t.Fatalf("last managed configuration remains: %v", err)
				}
				if runtime.GOOS != "windows" {
					info, err := os.Stat(userPath)
					if err != nil || info.Mode().Perm() != 0640 {
						t.Fatalf("user mode=%v err=%v", info, err)
					}
				}
			})
		}
	}
}

func TestSSHCleanupWorksForEverySubsetOfOwnedVolumes(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for mask := 1; mask < 8; mask++ {
			for _, command := range []string{"ssh-config", "remove", "remove volumes"} {
				t.Run(fmt.Sprintf("%s/%03b/%s", host.name, mask, command), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					volumes := map[string]string{}
					for index, role := range sandboxVolumeRoles {
						if mask&(1<<index) != 0 {
							volumes[role] = "default"
						}
					}
					responses := sandboxObjectResponses(nil, false, volumes, nil)
					if command == "remove volumes" {
						responses = append(responses, make([]testutil.Response, len(volumes))...)
					}
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					before := len(fakes.Calls("podman"))
					args := []string{"ssh-config", "agent01", "--remove"}
					if command != "ssh-config" {
						args = []string{"remove", "agent01"}
						if command == "remove volumes" {
							args = append(args, "--volumes")
						}
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if managedSSHFiles(t, state) != nil {
						t.Fatalf("SSH files remain: %v", managedSSHFiles(t, state))
					}
					user, err := os.ReadFile(filepath.Join(sshDir, "config"))
					if err != nil || len(user) != 0 {
						t.Fatalf("user config=%q err=%v", user, err)
					}
					for _, call := range fakes.Calls("podman")[before:] {
						if slices.Contains(call.Args, "exec") || slices.Contains(call.Args, "create") || slices.Contains(call.Args, "start") {
							t.Fatalf("unexpected container call: %v", call.Args)
						}
					}
				})
			}
		}
	}
}

func TestRemoveCleansSSHAfterRemovingContainerDespiteForeignVolume(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, foreign := range []string{"", "other"} {
			for _, role := range sandboxVolumeRoles {
				t.Run(host.name+"/"+role+"/"+foreign, func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					owned := "default"
					volumes := map[string]string{"workspace": owned, "home": owned, "ssh": owned}
					volumes[role] = foreign
					responses := append(sandboxObjectResponses(&owned, false, volumes, nil), make([]testutil.Response, 3)...)
					if host.windows {
						responses = append(healthyWindowsPodman()[1:3], responses...)
					}
					fakes.Script("podman", responses...)
					stdout, stderr, status := runCLI(t, fixture, "remove", "agent01", "--volumes")
					if status == 0 || !strings.Contains(stdout, "Removed container") || !strings.Contains(stdout, "Kept volume sandboxed-agents.default.agent01."+role) {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if role == "ssh" && strings.Contains(stdout, "--install") {
						t.Fatalf("promised installation through owner conflict: %q", stdout)
					}

					if managedSSHFiles(t, state) != nil {
						t.Fatal("SSH files remain after container removed")
					}
					user, err := os.ReadFile(filepath.Join(sshDir, "config"))
					if err != nil || len(user) != 0 {
						t.Fatalf("user config=%q err=%v", user, err)
					}
				})
			}
		}
	}
}

func TestSSHRemovalRecoversMissingManagedEntryAsInstallationRecommends(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, missing := range []string{"entry", "configuration"} {
			t.Run(host.name+"/"+missing, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
				config := filepath.Join(state, "group-default", "ssh", "config")
				if missing == "configuration" {
					if err := os.Remove(config); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(config, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", false)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if managedSSHFiles(t, state) != nil {
					t.Fatal("partial state remains")
				}
				user, err := os.ReadFile(filepath.Join(sshDir, "config"))
				if err != nil || len(user) != 0 {
					t.Fatalf("config=%q err=%v", user, err)
				}
			})
		}
	}
}

func TestSSHRemovalRecoversMissingEntryFileAndPreservesOtherSetup(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"ssh-config", "remove", "volumes only"} {
			for _, blockPresent := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/block=%t", host.name, command, blockPresent), func(t *testing.T) {
					fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
					installSSHForRemoval(t, fakes, fixture, host.windows, "agent02", "default")
					keyDir := filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")
					otherDir := filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743032")
					otherBefore := managedSSHFiles(t, otherDir)
					userBefore := sshDirectoryContents(t, sshDir)
					otherEntry, err := os.ReadFile(filepath.Join(otherDir, "entry"))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(keyDir, "entry")); err != nil {
						t.Fatal(err)
					}
					if !blockPresent {
						configPath := filepath.Join(state, "group-default", "ssh", "config")
						if err := os.WriteFile(configPath, append(otherEntry, []byte("Host *\n")...), 0600); err != nil {
							t.Fatal(err)
						}
					}
					beforeCalls := len(fakes.Calls("podman"))
					args := []string{"ssh-config", "agent01", "--remove"}
					if command == "ssh-config" {
						scriptSSHInstall(t, fakes, host.windows, "agent01", "default", true, testutil.Response{Stdout: "sandboxed-agents-manager v1\n"}, testutil.Response{})
					} else {
						owned := "default"
						var owner *string
						if command == "remove" {
							owner = &owned
						}
						responses := sandboxObjectResponses(owner, false, map[string]string{"workspace": owned, "home": owned, "ssh": owned}, nil)
						if owner != nil {
							responses = append(responses, testutil.Response{})
						}
						if host.windows {
							responses = append(healthyWindowsPodman()[1:3], responses...)
						}
						fakes.Script("podman", responses...)
						if command == "remove" {
							args = []string{"remove", "agent01"}
						}
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if _, err := os.Stat(keyDir); !os.IsNotExist(err) {
						t.Fatalf("partial state remains: %v", err)
					}
					config, err := os.ReadFile(filepath.Join(state, "group-default", "ssh", "config"))
					if err != nil || string(config) != string(otherEntry)+"Host *\n" {
						t.Fatalf("config=%q err=%v", config, err)
					}
					if !reflect.DeepEqual(otherBefore, managedSSHFiles(t, otherDir)) || !reflect.DeepEqual(userBefore, sshDirectoryContents(t, sshDir)) {
						t.Fatal("other setup changed")
					}
					var deauthorize int
					for _, call := range fakes.Calls("podman")[beforeCalls:] {
						if slices.Contains(call.Args, "deauthorize") {
							deauthorize++
						}
					}
					want := 0
					if command == "ssh-config" {
						want = 1
					}
					if deauthorize != want {
						t.Fatalf("deauthorize=%d want=%d", deauthorize, want)
					}
				})
			}
		}
	}
}

func TestSSHRemovalRecoversLastPartialSetup(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, partial := range []string{"missing entry", "empty entry", "missing directory"} {
			t.Run(host.name+"/"+partial, func(t *testing.T) {
				fakes, fixture, sshDir, state := sshSetupHost(t, host.windows)
				installSSHForRemoval(t, fakes, fixture, host.windows, "agent01", "default")
				keyDir := filepath.Join(state, "group-default", "ssh", "sandbox-6167656e743031")
				var err error
				switch partial {
				case "missing entry":
					err = os.Remove(filepath.Join(keyDir, "entry"))
				case "empty entry":
					err = os.WriteFile(filepath.Join(keyDir, "entry"), nil, 0600)
				case "missing directory":
					err = os.RemoveAll(keyDir)
				}
				if err != nil {
					t.Fatal(err)
				}
				scriptSSHInstall(t, fakes, host.windows, "agent01", "default", false)
				stdout, stderr, status := runCLI(t, fixture, "ssh-config", "agent01", "--remove")
				if status != 0 || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if managedSSHFiles(t, state) != nil {
					t.Fatalf("partial setup remains: %v", managedSSHFiles(t, state))
				}
				user, err := os.ReadFile(filepath.Join(sshDir, "config"))
				if err != nil || len(user) != 0 {
					t.Fatalf("Include remains: %q err=%v", user, err)
				}
			})
		}
	}
}
