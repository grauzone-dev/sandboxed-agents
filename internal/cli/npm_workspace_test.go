package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/npmpackage"
	"github.com/grauzone-dev/sandboxed-agents/internal/release"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpProtectsNpmLauncherPathsBeforePodman(t *testing.T) {
	for _, name := range []string{"launcher.cjs", "sandboxed-agents", "sandboxed-agents.cmd", "sandboxed-agents.ps1"} {
		t.Run(name, func(t *testing.T) {
			root := workspaceFixture(t)
			fakes := testutil.NewFakePrograms(t)
			fixture := "linux-preflight"
			if runtime.GOOS == "windows" {
				configureWindowsWorkspacePaths(t, root)
				fixture = "windows"
			}
			workspace := filepath.Join(root, "installed")
			if err := os.Mkdir(workspace, 0700); err != nil {
				t.Fatal(err)
			}
			protected := filepath.Join(workspace, name)
			if err := os.WriteFile(protected, []byte("launcher"), 0600); err != nil {
				t.Fatal(err)
			}
			paths, err := json.Marshal([]string{protected})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("SANDBOXED_AGENTS_NPM_PATHS", string(paths))
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01", workspace)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(strings.ToLower(stderr), strings.ToLower(protected)) {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("protected npm path called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestInstalledNpmLauncherProtectsItsLaunchLinkBeforePodman(t *testing.T) {
	root := workspaceFixture(t)
	fixture := "linux-preflight"
	if runtime.GOOS == "windows" {
		configureWindowsWorkspacePaths(t, root)
		fixture = "windows"
	}
	artifacts := filepath.Join(root, "artifacts")
	if err := os.Mkdir(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{release.LinuxExecutable, release.WindowsExecutable} {
		if err := os.WriteFile(filepath.Join(artifacts, name), binary, 0700); err != nil {
			t.Fatal(err)
		}
	}
	checksums, err := release.Checksums(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, release.ChecksumFilename), checksums, 0600); err != nil {
		t.Fatal(err)
	}
	tag := "v1.0.0-preview.20261007.1"
	if err := npmpackage.Write(artifacts, tag); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "installed")
	command := exec.Command("npm", "install", "--offline", "--global", "--prefix", prefix, "--cache", filepath.Join(prefix, "cache"), "--no-audit", "--no-fund", filepath.Join(artifacts, npmpackage.Filename(tag)))
	if runtime.GOOS == "windows" {
		command = exec.Command("cmd", append([]string{"/c", "npm"}, command.Args[1:]...)...)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, output)
	}
	packageDir := filepath.Join(prefix, "lib", "node_modules", "sandboxed-agents")
	link := filepath.Join(prefix, "bin", "sandboxed-agents")
	workspaces := []string{filepath.Dir(link)}
	protectedNames := []string{link}
	if runtime.GOOS == "windows" {
		packageDir = filepath.Join(prefix, "node_modules", "sandboxed-agents")
		link = filepath.Join(packageDir, "launcher.cjs")
		workspaces, protectedNames = nil, nil
	}
	paths := []string{filepath.Join(packageDir, "launcher.cjs")}
	if runtime.GOOS == "windows" {
		paths = append(paths, filepath.Join(prefix, "sandboxed-agents.cmd"), filepath.Join(prefix, "sandboxed-agents.ps1"))
	}
	for _, protected := range paths {
		workspace := filepath.Join(root, "alias-"+filepath.Base(protected))
		if err := os.Mkdir(workspace, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(protected, filepath.Join(workspace, filepath.Base(protected))); err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, workspace)
		protectedNames = append(protectedNames, protected)
	}
	fakes := testutil.NewFakePrograms(t)
	for i, workspace := range workspaces {
		command = exec.Command("node", link, "-test.run=^TestCLIProcess$", "--", "up", "agent01", workspace)
		command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE="+fixture)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "protected host path") || !strings.Contains(strings.ToLower(stderr.String()), strings.ToLower(protectedNames[i])) {
			t.Fatalf("workspace=%q run: %v stdout=%q stderr=%q", workspace, err, stdout.String(), stderr.String())
		}
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("installed npm launcher called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpProtectsNpmLaunchLinkEntryAndTarget(t *testing.T) {
	root := workspaceFixture(t)
	fakes := testutil.NewFakePrograms(t)
	fixture := "linux-preflight"
	if runtime.GOOS == "windows" {
		configureWindowsWorkspacePaths(t, root)
		fixture = "windows"
	}
	packageDir := filepath.Join(root, "package")
	binDir := filepath.Join(root, "bin")
	for _, dir := range []string{packageDir, binDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(packageDir, "launcher.cjs")
	if err := os.WriteFile(launcher, []byte("launcher"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "sandboxed-agents")
	if err := os.Symlink(launcher, link); err != nil {
		t.Fatal(err)
	}
	paths, err := json.Marshal([]string{link})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDBOXED_AGENTS_NPM_PATHS", string(paths))
	for _, workspace := range []string{binDir, packageDir} {
		stdout, stderr, status := runCLI(t, fixture, "up", "agent01", workspace)
		if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") {
			t.Fatalf("workspace=%q status=%d stdout=%q stderr=%q", workspace, status, stdout, stderr)
		}
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("protected npm link called Podman")
	}
	assertNoSSH(t, fakes)
}

func TestUpRejectsInvalidNpmProtectedPathMetadataBeforePodman(t *testing.T) {
	for _, value := range []string{"", "null", "{}", "[\"relative\"]"} {
		t.Run(value, func(t *testing.T) {
			root := workspaceFixture(t)
			fakes := testutil.NewFakePrograms(t)
			fixture := "linux-preflight"
			if runtime.GOOS == "windows" {
				configureWindowsWorkspacePaths(t, root)
				fixture = "windows"
			}
			t.Setenv("SANDBOXED_AGENTS_NPM_PATHS", value)
			stdout, stderr, status := runCLI(t, fixture, "up", "agent01", root)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "SANDBOXED_AGENTS_NPM_PATHS") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("invalid npm metadata called Podman")
			}
			assertNoSSH(t, fakes)
		})
	}
}

func TestUpProtectsNpmLaunchLinkReachedThroughBindMount(t *testing.T) {
	fakes := linuxHost(t)
	root := workspaceFixture(t)
	bin := filepath.Join(root, "bin")
	workspace := filepath.Join(root, "project")
	for _, dir := range []string{bin, workspace} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(root, "launcher.cjs")
	if err := os.WriteFile(launcher, []byte("launcher"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "sandboxed-agents")
	if err := os.Symlink(launcher, link); err != nil {
		t.Fatal(err)
	}
	paths, err := json.Marshal([]string{link})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDBOXED_AGENTS_NPM_PATHS", string(paths))
	escape := strings.NewReplacer(`\`, `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)
	mountinfo := fmt.Sprintf("1 0 0:1 / / rw - overlay overlay rw\n2 1 0:1 %s %s rw - overlay overlay rw\n32 1 0:28 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", escape.Replace(bin), escape.Replace(workspace))
	hostFile(t, os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE"), "/proc/self/mountinfo", mountinfo)
	stdout, stderr, status := runCLI(t, "linux-preflight", "up", "agent01", workspace)
	if status == 0 || stdout != "" || !strings.Contains(stderr, "protected host path") || !strings.Contains(stderr, link) {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("aliased npm launch link called Podman")
	}
	assertNoSSH(t, fakes)
}
