package cli_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func linuxHost(t *testing.T) *testutil.FakePrograms {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux host preflight")
	}
	fakes := testutil.NewFakePrograms(t)
	bin := filepath.Dir(fakes.Podman)
	for _, name := range []string{"newuidmap", "newgidmap", "pasta", "ssh-keygen"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("unused fixture executable"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	root := t.TempDir()
	t.Setenv("SANDBOXED_AGENTS_HOST_FIXTURE", root)
	t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_ROOT", "")
	t.Setenv("SANDBOXED_AGENTS_NO_DELEGATION", "")
	for path, data := range map[string]string{
		"/etc/subuid": "fixture:100000:65536\n", "/etc/subgid": "1000:200000:65536\n",
		"/proc/self/cgroup":    "0::/user.slice/user-1000.slice/session-3.scope\n",
		"/proc/self/mountinfo": "32 24 0:28 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers":     "cpu memory pids\n",
		"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.procs":           "",
		"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.subtree_control": "cpu memory pids\n",
	} {
		hostFile(t, root, path, data)
	}
	fakes.Script("podman", testutil.Response{Stdout: "podman version 5.0.0\n"})
	return fakes
}

func hostFile(t *testing.T, root, path, data string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReportsEveryMetLinuxPrerequisite(t *testing.T) {
	fakes := linuxHost(t)
	stdout, stderr, status := runCLI(t, "linux-preflight", "check")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	for _, name := range []string{"podman", "Podman version", "rootless", "subordinate UID", "subordinate GID", "newuidmap", "newgidmap", "pasta", "cgroups v2", "CPU", "memory", "process", "ssh", "ssh-keygen"} {
		if !strings.Contains(stdout, "OK: "+name) {
			t.Errorf("missing met prerequisite %q in %q", name, stdout)
		}
	}
	want := []testutil.Call{{Args: []string{"--version"}}}
	if got := fakes.Calls("podman"); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	if len(fakes.Calls("ssh")) != 0 {
		t.Fatal("host check attempted an SSH connection")
	}
}

func TestCheckNamesEachMissingLinuxPrerequisite(t *testing.T) {
	for _, name := range []string{"podman", "Podman version", "rootless", "subordinate UID", "subordinate GID", "newuidmap", "newgidmap", "pasta", "cgroups v2", "CPU", "memory", "process", "ssh", "ssh-keygen"} {
		t.Run(name, func(t *testing.T) {
			fakes := linuxHost(t)
			root := os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE")
			service := "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service"
			wantMissing := []string{name}
			switch name {
			case "podman", "newuidmap", "newgidmap", "pasta", "ssh", "ssh-keygen":
				if err := os.Remove(filepath.Join(filepath.Dir(fakes.Podman), name)); err != nil {
					t.Fatal(err)
				}
				if name == "podman" {
					wantMissing = append(wantMissing, "Podman version")
				}
			case "Podman version":
				fakes.Script("podman", testutil.Response{Stdout: "podman version 4.3.1\n"})
			case "rootless":
				t.Setenv("SANDBOXED_AGENTS_PREFLIGHT_ROOT", "1")
				hostFile(t, root, "/etc/subgid", "fixture:200000:65536\n")
				hostFile(t, root, "/proc/self/cgroup", "0::/delegated\n")
				for _, file := range []string{"cgroup.controllers", "cgroup.procs", "cgroup.subtree_control"} {
					hostFile(t, root, "/sys/fs/cgroup/delegated/"+file, "cpu memory pids\n")
				}
			case "subordinate UID", "subordinate GID":
				path := "/etc/subuid"
				if name == "subordinate GID" {
					path = "/etc/subgid"
				}
				hostFile(t, root, path, "other-user:100000:65536\n")
			case "cgroups v2":
				hostFile(t, root, "/proc/self/mountinfo", "32 24 0:28 / /sys/fs/cgroup rw - cgroup cgroup rw\n")
				wantMissing = append(wantMissing, "CPU", "memory", "process")
			case "CPU", "memory", "process":
				controllers := map[string]string{"CPU": "memory pids", "memory": "cpu pids", "process": "cpu memory"}
				hostFile(t, root, service+"/cgroup.controllers", controllers[name]+"\n")
			}
			stdout, stderr, status := runCLI(t, "linux-preflight", "check")
			if status == 0 || !strings.Contains(stderr, "host prerequisites") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var missing []string
			for _, line := range strings.Split(stdout, "\n") {
				if strings.HasPrefix(line, "MISSING: ") {
					fields := strings.SplitN(strings.TrimPrefix(line, "MISSING: "), ": ", 2)
					if len(fields) != 2 || fields[1] == "" {
						t.Fatalf("no setup instruction: %q", line)
					}
					missing = append(missing, fields[0])
				}
			}
			if !reflect.DeepEqual(missing, wantMissing) {
				t.Fatalf("missing=%v want=%v output=%q", missing, wantMissing, stdout)
			}
			if name == "Podman version" && !strings.Contains(stdout, "4.4.0") {
				t.Fatal("minimum version not reported")
			}
			if len(fakes.Calls("ssh")) != 0 {
				t.Fatal("host check attempted SSH")
			}
			if calls := fakes.Calls("podman"); name == "podman" && len(calls) != 0 {
				t.Fatalf("missing Podman invoked: %v", calls)
			}
		})
	}
}

func TestCheckReportsSeveralMissingPrerequisitesInOneRun(t *testing.T) {
	fakes := linuxHost(t)
	for _, name := range []string{"pasta", "newgidmap", "ssh-keygen"} {
		if err := os.Remove(filepath.Join(filepath.Dir(fakes.Podman), name)); err != nil {
			t.Fatal(err)
		}
	}
	stdout, _, status := runCLI(t, "linux-preflight", "check")
	if status == 0 || strings.Count(stdout, "MISSING: ") != 3 {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	for _, name := range []string{"pasta", "newgidmap", "ssh-keygen"} {
		if !strings.Contains(stdout, "MISSING: "+name+": ") {
			t.Fatalf("missing diagnostic %q: %q", name, stdout)
		}
	}
}

func TestCheckEnforcesConfirmedPodmanMinimum(t *testing.T) {
	for _, version := range []struct {
		value     string
		supported bool
	}{
		{"4.3.9", false}, {"4.4.0-dev", false}, {"4.4.0", true}, {"4.4.0+distribution.1", true}, {"4.4.1", true}, {"5.0.0", true}, {"6.1.3", true}, {"garbage", false}, {"4.44", false},
	} {
		t.Run(version.value, func(t *testing.T) {
			fakes := linuxHost(t)
			fakes.Script("podman", testutil.Response{Stdout: "podman version " + version.value + "\n"})
			stdout, _, status := runCLI(t, "linux-preflight", "check")
			if (status == 0) != version.supported {
				t.Fatalf("status=%d output=%q", status, stdout)
			}
		})
	}
}

func TestCheckRejectsUsageBeforeCallingPodman(t *testing.T) {
	for _, arg := range []string{"--unknown", "sandbox-name"} {
		t.Run(arg, func(t *testing.T) {
			fakes := linuxHost(t)
			stdout, stderr, status := runCLI(t, "linux-preflight", "check", arg)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("invalid usage ran external program")
			}
		})
	}
}

func TestCheckLeavesHostFilesUnchanged(t *testing.T) {
	fakes := linuxHost(t)
	root := os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE")
	snapshot := func() map[string]string {
		files := map[string]string{}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			data := []byte{}
			if !entry.IsDir() {
				data, err = os.ReadFile(path)
				if err != nil {
					return err
				}
			}
			files[path] = fmt.Sprintf("%x %v %d", sha256.Sum256(data), info.Mode(), info.ModTime().UnixNano())
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	stdout, stderr, status := runCLI(t, "linux-preflight", "check")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("host files changed: before=%v after=%v", before, after)
	}
	if got := fakes.Calls("podman"); !reflect.DeepEqual(got, []testutil.Call{{Args: []string{"--version"}}}) {
		t.Fatalf("non-version Podman call: %v", got)
	}
}

func TestCheckRequiresCgroupDelegationRatherThanControllerAvailability(t *testing.T) {
	linuxHost(t)
	t.Setenv("SANDBOXED_AGENTS_NO_DELEGATION", "1")
	stdout, _, status := runCLI(t, "linux-preflight", "check")
	if status == 0 || !strings.Contains(stdout, "OK: cgroups v2") {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
	for _, name := range []string{"CPU", "memory", "process"} {
		if !strings.Contains(stdout, "MISSING: "+name) {
			t.Fatalf("controller accepted without delegation: %q", stdout)
		}
	}
}

func TestCheckRequiresPermissionToCreateChildCgroups(t *testing.T) {
	linuxHost(t)
	t.Setenv("SANDBOXED_AGENTS_NO_DELEGATION", "directory")
	stdout, _, status := runCLI(t, "linux-preflight", "check")
	if status == 0 || !strings.Contains(stdout, "MISSING: CPU") {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
}

func TestCheckAcceptsDelegatedCgroupfsAndMountRoots(t *testing.T) {
	for _, rootPath := range []string{"/", "/delegated"} {
		t.Run(rootPath, func(t *testing.T) {
			linuxHost(t)
			root := os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE")
			hostFile(t, root, "/proc/self/cgroup", "0::/delegated/leaf\n")
			hostFile(t, root, "/proc/self/mountinfo", "32 24 0:28 "+rootPath+" /sys/fs/cgroup rw - cgroup2 cgroup rw\n")
			path := "/sys/fs/cgroup/delegated"
			if rootPath == "/delegated" {
				path = "/sys/fs/cgroup"
			}
			for _, file := range []string{"cgroup.controllers", "cgroup.procs", "cgroup.subtree_control"} {
				hostFile(t, root, path+"/"+file, "cpu memory pids\n")
			}
			stdout, stderr, status := runCLI(t, "linux-preflight", "check")
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
		})
	}
}

func TestCheckReportsVersionQueryFailures(t *testing.T) {
	fakes := linuxHost(t)
	fakes.Script("podman", testutil.Response{Stderr: "configuration error", ExitCode: 42})
	stdout, _, status := runCLI(t, "linux-preflight", "check")
	if status == 0 || !strings.Contains(stdout, "MISSING: Podman version") || !strings.Contains(stdout, "OK: ssh-keygen") {
		t.Fatalf("status=%d stdout=%q", status, stdout)
	}
}

func TestCheckRejectsInvalidSubordinateRanges(t *testing.T) {
	for _, value := range []string{"fixture:100000:0", "fixture:-1:65536", "fixture:4294967295:65536", "fixture:invalid:65536", "fixture:100000:65536:extra"} {
		t.Run(value, func(t *testing.T) {
			linuxHost(t)
			hostFile(t, os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE"), "/etc/subuid", value+"\n")
			stdout, _, status := runCLI(t, "linux-preflight", "check")
			if status == 0 || !strings.Contains(stdout, "MISSING: subordinate UID") {
				t.Fatalf("status=%d stdout=%q", status, stdout)
			}
		})
	}
}
