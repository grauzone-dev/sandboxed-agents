package cli_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const knownHostFingerprints = "ssh-ed25519 SHA256:u2aGVsip9n5vVo+NJ1FcmLQplauZNkyVoBB3B+624wk\n" +
	"ecdsa-sha2-nistp256 SHA256:oAlWEmNwo9ekfMKkBIkFebvkP3oKTIBpE/TgcEEni38\n" +
	"ssh-rsa SHA256:I5cO+2Y3Deazh9cw52oQ6IGOyRAJAxjnMwOsNKLomRI\n"

func TestFingerprintPrintsKnownHostKeysWithoutSSHSetup(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			fakes.Script("podman", append(shellResponses(fixture), hostKeyResponses(t)...)...)
			stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
			if status != 0 || stdout != knownHostFingerprints || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertHostKeyReads(t, fixture, fakes)
			assertNoSSH(t, fakes)
		})
	}
}

func TestFingerprintRefusesAndReportsTheFirstFailure(t *testing.T) {
	owned, foreign, missing := "default", "other", ""
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name, message string
			container     *string
			running       bool
			volumes       map[string]string
			backup        *string
			absent        []string
		}{
			{name: "unknown", message: "does not exist in this controller group"},
			{name: "stopped", container: &owned, message: "start agent01"},
			{name: "foreign container", container: &foreign, message: "owner conflict on sandboxed-agents.default.agent01", absent: []string{"start agent01"}},
			{name: "unlabelled container", container: &missing, message: "owner conflict on sandboxed-agents.default.agent01", absent: []string{"start agent01"}},
			{name: "foreign workspace", container: &owned, volumes: map[string]string{"workspace": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.workspace", absent: []string{"start agent01"}},
			{name: "unlabelled home", container: &owned, volumes: map[string]string{"home": missing}, message: "owner conflict on sandboxed-agents.default.agent01.home", absent: []string{"start agent01"}},
			{name: "foreign SSH volume", container: &owned, volumes: map[string]string{"ssh": foreign}, message: "owner conflict on sandboxed-agents.default.agent01.ssh", absent: []string{"start agent01"}},
			{name: "unlabelled SSH volume", container: &owned, running: true, volumes: map[string]string{"ssh": missing}, message: "owner conflict on sandboxed-agents.default.agent01.ssh"},
			{name: "interrupted running", container: &owned, running: true, backup: &owned, message: "update agent01", absent: []string{"start agent01"}},
			{name: "interrupted stopped", container: &owned, backup: &owned, message: "update agent01", absent: []string{"start agent01"}},
			{name: "backup only", backup: &owned, message: "update agent01", absent: []string{"start agent01"}},
			{name: "foreign backup", container: &owned, backup: &foreign, message: "owner conflict on sandboxed-agents-backup.default.agent01", absent: []string{"update agent01", "start agent01"}},
			{name: "conflict before interrupted", container: &owned, volumes: map[string]string{"home": foreign}, backup: &owned, message: "owner conflict on sandboxed-agents.default.agent01.home", absent: []string{"update agent01", "start agent01"}},
			{name: "all volumes only", volumes: map[string]string{"workspace": owned, "home": owned, "ssh": owned}, message: "up agent01"},
			{name: "partial volumes only", volumes: map[string]string{"home": owned}, message: "up agent01"},
			{name: "foreign volumes only", volumes: map[string]string{"workspace": foreign, "home": missing}, message: "owner conflict on sandboxed-agents.default.agent01.workspace, sandboxed-agents.default.agent01.home", absent: []string{"up agent01"}},
			{name: "foreign backup only", backup: &foreign, message: "owner conflict on sandboxed-agents-backup.default.agent01", absent: []string{"update agent01"}},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := sandboxObjectResponses(test.container, test.running, test.volumes, test.backup)
				if fixture == "windows" {
					responses = append(healthyWindowsPodman()[1:3:3], responses...)
				}
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
				if status != 1 || stdout != "" || !strings.Contains(stderr, test.message) {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				for _, absent := range test.absent {
					if strings.Contains(stderr, absent) {
						t.Fatalf("reported a later failure %q: %q", absent, stderr)
					}
				}
				if strings.Contains(test.message, "owner conflict") && !strings.Contains(stderr, "Podman") {
					t.Fatalf("no Podman repair hint: %q", stderr)
				}
				for _, call := range shellOperationCalls(t, fixture, fakes.Calls("podman")) {
					if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
						t.Fatalf("refused fingerprint opened or changed something: %v", call.Args)
					}
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestFingerprintRejectsInvalidUsageBeforePodman(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, args := range [][]string{{"fingerprint"}, {"fingerprint", ".bad"}, {"fingerprint", "--help"}, {"fingerprint", "agent01", "extra"}, {"fingerprint", "agent01", "--force"}} {
			t.Run(fixture+"/"+strings.Join(args, " "), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				stdout, stderr, status := runCLI(t, fixture, args...)
				if status != 1 || stdout != "" || !strings.Contains(stderr, "Usage: sandboxed-agents fingerprint") || len(fakes.Calls("podman")) != 0 {
					t.Fatalf("status=%d stdout=%q stderr=%q calls=%v", status, stdout, stderr, fakes.Calls("podman"))
				}
				if len(args) == 1 && !strings.Contains(stderr, "fingerprint NAME") {
					t.Fatalf("missing name did not explain usage: %q", stderr)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestFingerprintLeavesHostStateAndSSHFilesUntouched(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/populated-%t", fixture, populated), func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				root := t.TempDir()
				t.Setenv("HOME", root)
				t.Setenv("USERPROFILE", root)
				t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
				t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
				if populated {
					for _, name := range []string{".ssh/config", ".ssh/id_ed25519", ".ssh/known_hosts", "state/sandboxed-agents/group-default/ssh/key", "local/sandboxed-agents/group-default/ssh/config"} {
						path := filepath.Join(root, filepath.FromSlash(name))
						if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte("keep "+name+"\x00\r\n"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				before := fingerprintHostFiles(t, root)
				fakes.Script("podman", append(shellResponses(fixture), hostKeyResponses(t)...)...)
				stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
				if status != 0 || stdout != knownHostFingerprints || stderr != "" {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				if after := fingerprintHostFiles(t, root); !reflect.DeepEqual(before, after) {
					t.Fatalf("host files changed: before=%v after=%v", before, after)
				}
				assertHostKeyReads(t, fixture, fakes)
				assertNoSSH(t, fakes)
			})
		}
	}
}

func fingerprintHostFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[filepath.ToSlash(name)+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		files[filepath.ToSlash(name)] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestFingerprintReadsOnlyTheSelectedControllerGroup(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", "team-a")
			fakes := testutil.NewFakePrograms(t)
			responses := shellResponses(fixture)
			for index := range responses {
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, ".default.", ".team-a.")
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"default"`, `"team-a"`)
			}
			fakes.Script("podman", append(responses, hostKeyResponses(t)...)...)
			stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
			if status != 0 || stdout != knownHostFingerprints || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			for _, call := range shellOperationCalls(t, fixture, fakes.Calls("podman")) {
				if !strings.Contains(strings.Join(call.Args, " "), ".team-a.agent01") {
					t.Fatalf("another group accessed: %v", call.Args)
				}
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestFingerprintPrintsNothingWhenAnyHostKeyCannotBeRead(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for index, keyType := range []string{"ed25519", "ecdsa", "rsa"} {
			t.Run(fixture+"/"+keyType, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := hostKeyResponses(t)[:index]
				responses = append(responses, testutil.Response{Stdout: "partial public key", Stderr: "key file missing\n", ExitCode: 42})
				fakes.Script("podman", append(shellResponses(fixture), responses...)...)
				stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
				if status != 1 || stdout != "" || !strings.Contains(stderr, "/etc/ssh/ssh_host_"+keyType+"_key.pub") || !strings.Contains(stderr, "exit status 42: key file missing") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
				if len(calls) != 10+index {
					t.Fatalf("kept reading after failure: %v", calls)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestFingerprintRejectsInvalidPublicKeysWithoutPartialOutput(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		for _, test := range []struct {
			name string
			key  int
			data string
		}{
			{name: "empty", key: 0},
			{name: "invalid Base64", key: 1, data: "ecdsa-sha2-nistp256 invalid\n"},
			{name: "truncated wire string", key: 0, data: "ssh-ed25519 AAAAEg==\n"},
			{name: "type does not match file", key: 2, data: hostKeyResponses(t)[0].Stdout},
			{name: "text type does not match blob", key: 0, data: strings.Replace(hostKeyResponses(t)[2].Stdout, "ssh-rsa", "ssh-ed25519", 1)},
			{name: "multiple keys in file", key: 0, data: strings.Repeat(hostKeyResponses(t)[0].Stdout, 2)},
			{name: "ECDSA point off curve", key: 1, data: "ecdsa-sha2-nistp256 " + base64.StdEncoding.EncodeToString(append([]byte("\x00\x00\x00\x13ecdsa-sha2-nistp256\x00\x00\x00\x08nistp256\x00\x00\x00\x41\x04"), make([]byte, 64)...)) + "\n"},
		} {
			t.Run(fixture+"/"+test.name, func(t *testing.T) {
				fakes := testutil.NewFakePrograms(t)
				responses := hostKeyResponses(t)[:test.key]
				responses = append(responses, testutil.Response{Stdout: test.data})
				fakes.Script("podman", append(shellResponses(fixture), responses...)...)
				stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
				if status != 1 || stdout != "" || !strings.Contains(stderr, "invalid SSH host public key /etc/ssh/ssh_host_") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				assertNoSSH(t, fakes)
			})
		}
	}
}

func TestFingerprintIgnoresPublicKeyCommentsAndLineEndings(t *testing.T) {
	for _, fixture := range []string{"sandbox-host", "windows"} {
		t.Run(fixture, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := hostKeyResponses(t)
			for index := range responses {
				fields := strings.Fields(responses[index].Stdout)
				responses[index].Stdout = fields[0] + " " + fields[1] + " unrelated comment with spaces\r\n"
			}
			fakes.Script("podman", append(shellResponses(fixture), responses...)...)
			stdout, stderr, status := runCLI(t, fixture, "fingerprint", "agent01")
			if status != 0 || stdout != knownHostFingerprints || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertHostKeyReads(t, fixture, fakes)
			assertNoSSH(t, fakes)
		})
	}
}

func hostKeyResponses(t *testing.T) []testutil.Response {
	t.Helper()
	var responses []testutil.Response
	for _, keyType := range []string{"ed25519", "ecdsa", "rsa"} {
		data, err := os.ReadFile(filepath.Join("testdata", "host-keys", keyType+".pub"))
		if err != nil {
			t.Fatal(err)
		}
		responses = append(responses, testutil.Response{Stdout: string(data)})
	}
	return responses
}

func assertHostKeyReads(t *testing.T, fixture string, fakes *testutil.FakePrograms) {
	t.Helper()
	calls := shellOperationCalls(t, fixture, fakes.Calls("podman"))
	var reads [][]string
	for _, call := range calls {
		if call.Args[0] == "exec" {
			reads = append(reads, call.Args)
		} else if len(call.Args) != 3 || (call.Args[0] != "container" && call.Args[0] != "volume") || (call.Args[1] != "exists" && call.Args[1] != "inspect") {
			t.Fatalf("fingerprint changed something: %v", call.Args)
		}
	}
	want := [][]string{
		{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "cat", "/etc/ssh/ssh_host_ed25519_key.pub"},
		{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "cat", "/etc/ssh/ssh_host_ecdsa_key.pub"},
		{"exec", "--user=0:0", "sandboxed-agents.default.agent01", "cat", "/etc/ssh/ssh_host_rsa_key.pub"},
	}
	if !reflect.DeepEqual(reads, want) {
		t.Fatalf("host key reads=%v want=%v", reads, want)
	}
}
