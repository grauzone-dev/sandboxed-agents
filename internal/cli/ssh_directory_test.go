package cli_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpAndStartLeaveTheUsersSSHDirectoryUntouched(t *testing.T) {
	for _, host := range resourceLimitHosts {
		for _, command := range []string{"up", "start"} {
			for _, existing := range []bool{false, true} {
				t.Run(host.name+"/"+command+"/existing-"+strconv.FormatBool(existing), func(t *testing.T) {
					fakes, fixture := resourceLimitHost(t, host.windows)
					home := t.TempDir()
					t.Setenv("HOME", home)
					t.Setenv("USERPROFILE", home)
					sshDirectory := filepath.Join(home, ".ssh")
					if existing {
						for name, contents := range map[string]string{
							"config":           "Host personal\n  HostName personal.example\n",
							"id_ed25519":       "private host credential\x00\xff\n",
							"id_ed25519.pub":   "existing public key\n",
							"known_hosts":      "existing pinned host key\n",
							"nested/untouched": "user-managed SSH data\n",
						} {
							path := filepath.Join(sshDirectory, name)
							if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					before := sshDirectoryContents(t, sshDirectory)
					port := unusedSSHPort(t)
					args := []string{command, "agent01"}
					if command == "up" {
						responses := append(upObjectResponses(nil, false, nil, nil), make([]testutil.Response, 6)...)
						scriptResourceLimitObjects(t, fakes, host.windows, responses, nil)
						args = append(args, "--port", strconv.Itoa(port))
					} else {
						owned := "default"
						responses := upObjectResponses(&owned, false, nil, nil)
						scriptResourceLimitObjects(t, fakes, false, responses, map[string]string{"ssh-port": strconv.Itoa(port)})
						responses = append(responses[1:], testutil.Response{})
						if host.windows {
							responses = append(healthyWindowsPodman()[1:3], responses...)
						}
						fakes.Script("podman", responses...)
					}
					stdout, stderr, status := runCLI(t, fixture, args...)
					if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running.") {
						t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
					}
					if after := sshDirectoryContents(t, sshDirectory); !reflect.DeepEqual(after, before) {
						t.Fatalf("SSH directory changed: before=%v after=%v", before, after)
					}
					assertNoSSH(t, fakes)
					if calls := fakes.Calls("ssh-keygen"); len(calls) != 0 {
						t.Fatalf("host SSH key generation attempted: %v", calls)
					}
				})
			}
		}
	}
}

func sshDirectoryContents(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		t.Fatal(err)
	}
	contents := make(map[string][]byte)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			contents[name] = nil
			return nil
		}
		contents[name], err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
