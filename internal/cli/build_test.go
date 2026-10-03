package cli_test

import (
	"bytes"
	"debug/elf"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func buildCalls(t *testing.T, fakes *testutil.FakePrograms, expectedCalls int) (string, string) {
	t.Helper()
	stdout, stderr, status := runCLI(t, "linux-build", "version")
	if status != 0 || stderr != "" {
		t.Fatalf("version status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	hash := strings.TrimSpace(strings.Split(stdout, "assets ")[1])
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Fatalf("invalid asset hash %q", hash)
	}
	calls := fakes.Calls("podman")
	if len(calls) != expectedCalls || !reflect.DeepEqual(calls[0].Args, []string{"--version"}) {
		t.Fatalf("calls=%v", calls)
	}
	args := calls[1].Args
	if expectedCalls == 3 {
		assertImageDiscovery(t, calls[2].Args, hash)
	}
	if len(args) == 0 {
		t.Fatal("empty build call")
	}
	directory := args[len(args)-1]
	tag := "localhost/sandboxed-agents:base-" + hash
	want := []string{
		"build", "--pull=always", "--no-cache", "--tag", tag,
		"--label", "io.github.sandboxed-agents.managed=true",
		"--label", "io.github.sandboxed-agents.asset-hash=" + hash,
		"--label", "io.github.sandboxed-agents.toolchains=",
		"--file", filepath.Join(directory, "Containerfile"), directory,
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("build args=%v want=%v", args, want)
	}
	if !strings.HasPrefix(filepath.Base(directory), "sandboxed-agents-context-") {
		t.Fatalf("build context=%q", directory)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("build context remains: %q err=%v", directory, err)
	}
	if len(fakes.Calls("ssh")) != 0 {
		t.Fatal("build attempted an SSH connection")
	}
	return tag, directory
}

func TestBuildRebuildsBaseAndRemovesContextOnSuccessAndFailure(t *testing.T) {
	for _, status := range []int{0, 42} {
		t.Run(map[int]string{0: "success", 42: "failure"}[status], func(t *testing.T) {
			fakes := linuxHost(t)
			captured := filepath.Join(t.TempDir(), "captured")
			responses := []testutil.Response{{Stdout: "podman version 5.0.0\n"}, {
				Stdout: "build log\n", Stderr: "build diagnostic\n", ExitCode: status, CaptureBuildContext: captured,
			}}
			if status == 0 {
				responses = append(responses, testutil.Response{Stdout: "[]"})
			}
			fakes.Script("podman", responses...)
			stdout, stderr, exit := runCLIAt(t, t.TempDir(), "linux-build", "build")
			wantCalls := 2
			if status == 0 {
				wantCalls = 3
			}
			tag, _ := buildCalls(t, fakes, wantCalls)
			if !strings.Contains(stdout, "OK: podman") || !strings.Contains(stdout, "build log\n") || !strings.Contains(stderr, "build diagnostic\n") {
				t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
			}
			if status == 0 {
				if exit != 0 || !strings.Contains(stdout, "Built image "+tag) || !strings.Contains(stdout, "Existing sandboxes keep their current image until you update") || !strings.Contains(stdout, "list marks them as outdated") {
					t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
			} else if exit == 0 || !strings.Contains(stderr, "podman build failed with exit status 42") || strings.Contains(stdout, "Built image") {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			checkBaseContext(t, captured)
		})
	}
}

func checkBaseContext(t *testing.T, directory string) {
	t.Helper()
	context := map[string]string{}
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if entry.Name() == "manager" {
			manager, err := elf.NewFile(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("manager is not ELF: %v", err)
			}
			defer manager.Close()
			if manager.Machine != elf.EM_X86_64 {
				t.Fatalf("manager machine=%v", manager.Machine)
			}
			for _, program := range manager.Progs {
				if program.Type == elf.PT_INTERP {
					t.Fatal("manager requires a dynamic loader")
				}
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
				t.Fatal("materialized manager is not executable")
			}
			return nil
		}
		if bytes.Contains(data, []byte("\r\n")) {
			t.Fatalf("context file %s contains CRLF", path)
		}
		context[entry.Name()] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(context) != 5 {
		t.Fatalf("unexpected context files: %v", context)
	}
	for file, required := range map[string][]string{
		"Containerfile":      {"FROM docker.io/library/debian:bookworm-slim", "COPY manager /usr/local/bin/sandboxed-agents-manager", "COPY sshd_config /usr/local/etc/sandboxed-agents/sshd_config", "sh /tmp/install-base.sh", "sh /tmp/record-versions.sh", "USER root", `ENTRYPOINT ["/usr/local/bin/sandboxed-agents-entrypoint"]`},
		"install-base.sh":    {"--no-install-recommends", "openssh-client", "openssh-server", "git", "gh", "tmux", "curl", "jq", "ripgrep", "less", "unzip", "ca-certificates", "coreutils", "util-linux", "node_24.x", "signed-by=/etc/apt/keyrings/nodesource.gpg", "apt-get install -y --no-install-recommends nodejs", "groupadd --gid 1000 agent", "useradd --uid 1000 --gid 1000", "--shell /bin/bash agent", "rm -f /etc/ssh/ssh_host_*"},
		"record-versions.sh": {"/usr/local/share/sandboxed-agents/versions.tsv", "dpkg-query -W", "${binary:Package}\\t${Version}\\n", "node --version", "npm --version", "sandboxed-agents-manager version"},
		"entrypoint.sh":      {"mkdir -p /run/sshd", "/usr/local/bin/sandboxed-agents-manager ssh start", "exec runuser -u agent -- sleep infinity"},
	} {
		for _, text := range required {
			if !strings.Contains(context[file], text) {
				t.Errorf("%s missing %q", file, text)
			}
		}
	}
	for file, data := range context {
		for _, forbidden := range []string{"npm install", "dotnet-sdk", "playwright install", "azure-cli", "build-essential", "ssh-keygen", "sudo"} {
			if strings.Contains(data, forbidden) {
				t.Errorf("%s includes %q", file, forbidden)
			}
		}
	}
}

func TestBuildUsesTheSameTagAcrossControllerGroupsAndRebuildsAnExistingTag(t *testing.T) {
	fakes := linuxHost(t)
	fakes.Script("podman",
		testutil.Response{Stdout: "podman version 5.0.0\n"}, testutil.Response{}, testutil.Response{Stdout: "[]"},
		testutil.Response{Stdout: "podman version 5.0.0\n"}, testutil.Response{}, testutil.Response{Stdout: "[]"},
	)
	var tag string
	for index, group := range []string{"default", "second-group"} {
		t.Setenv("SANDBOXED_AGENTS_GROUP", group)
		stdout, stderr, status := runCLI(t, "linux-build", "build")
		if status != 0 || stderr != "" {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
		}
		calls := fakes.Calls("podman")
		if len(calls) != 3*(index+1) {
			t.Fatalf("unexpected calls=%v", calls)
		}
		build := calls[3*index+1].Args
		if len(build) < 5 || !reflect.DeepEqual(build[:4], []string{"build", "--pull=always", "--no-cache", "--tag"}) {
			t.Fatalf("unexpected build=%v", build)
		}
		if index == 0 {
			tag = build[4]
		} else if build[4] != tag {
			t.Fatalf("tags differ: %q and %q", tag, build[4])
		}
		if _, err := os.Stat(build[len(build)-1]); !os.IsNotExist(err) {
			t.Fatalf("context was not removed: %v", err)
		}
	}
}

func TestBuildRejectsUsageBeforePreflight(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"extra"}, {"--with"}, {"--with", "none", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			if err := os.Remove(fakes.Podman); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, status := runCLI(t, "linux-build", append([]string{"build"}, args...)...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage: sandboxed-agents build") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("invalid usage ran an external command")
			}
		})
	}
}

func TestBuildStopsWhenPreflightFails(t *testing.T) {
	fakes := linuxHost(t)
	if err := os.Remove(filepath.Join(filepath.Dir(fakes.Podman), "pasta")); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, status := runCLI(t, "linux-build", "build")
	if status == 0 || !strings.Contains(stdout, "MISSING: pasta:") || !strings.Contains(stderr, "host prerequisites are missing") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !reflect.DeepEqual(fakes.Calls("podman"), []testutil.Call{{Args: []string{"--version"}}}) {
		t.Fatalf("calls=%v", fakes.Calls("podman"))
	}
}

func TestBuildStopsWhenItsContextCannotBeCreated(t *testing.T) {
	fakes := linuxHost(t)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing", "temporary"))
	stdout, stderr, status := runCLI(t, "linux-build", "build")
	if status == 0 || !strings.Contains(stderr, "create build context directory:") || strings.Contains(stdout, "Built image") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if !reflect.DeepEqual(fakes.Calls("podman"), []testutil.Call{{Args: []string{"--version"}}}) {
		t.Fatalf("calls=%v", fakes.Calls("podman"))
	}
}
