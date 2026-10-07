package npmpackage_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/npmpackage"
	"github.com/grauzone-dev/sandboxed-agents/internal/release"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const testTag = "v1.0.0-preview.20261003.1"

func fixturePackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	native := release.LinuxExecutable
	if runtime.GOOS == "windows" {
		native = release.WindowsExecutable
	}
	command := exec.Command("go", "build", "-o", filepath.Join(dir, native), "./testdata/process")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build process fixture: %v\n%s", err, output)
	}
	other := release.WindowsExecutable
	if native == other {
		other = release.LinuxExecutable
	}
	if err := os.WriteFile(filepath.Join(dir, other), []byte("unused binary"), 0755); err != nil {
		t.Fatal(err)
	}
	sums, err := release.Checksums(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, release.ChecksumFilename), sums, 0644); err != nil {
		t.Fatal(err)
	}
	if err := npmpackage.Write(dir, testTag); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, npmpackage.Filename(testTag))
}

func npm(t *testing.T, prefix string, args ...string) *exec.Cmd {
	t.Helper()
	binary, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{"--offline", "--no-audit", "--no-fund", "--update-notifier=false", "--ignore-scripts=false", "--prefix", prefix, "--cache", filepath.Join(prefix, "npm-cache")}
	argv = append(argv, args...)
	cli, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		cli = filepath.Join(filepath.Dir(binary), "node_modules", "npm", "bin", "npm-cli.js")
	}
	if _, err := os.Stat(cli); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", append([]string{cli}, argv...)...)
	command.Dir = prefix
	return command
}

func install(t *testing.T, archive string, global bool) (string, string) {
	t.Helper()
	prefix := t.TempDir()
	args := []string{"install", archive}
	if global {
		args = append(args, "--global")
	}
	command := npm(t, prefix, args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("npm install: %v\n%s", err, output)
	}
	root := filepath.Join(prefix, "node_modules", "sandboxed-agents")
	launch := filepath.Join(prefix, "node_modules", ".bin", "sandboxed-agents")
	if global {
		launch = filepath.Join(prefix, "bin", "sandboxed-agents")
		if runtime.GOOS == "windows" {
			launch = filepath.Join(prefix, "sandboxed-agents")
		} else {
			root = filepath.Join(prefix, "lib", "node_modules", "sandboxed-agents")
		}
	}
	return root, launch
}

func installedCommand(launch string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd.exe", append([]string{"/d", "/c", launch + ".cmd"}, args...)...)
	}
	return exec.Command(launch, args...)
}

func TestInstalledCommandForwardsArgumentsStreamsAndStatus(t *testing.T) {
	fake := testutil.NewFakePrograms(t)
	archive := fixturePackage(t)
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "global"}[global], func(t *testing.T) {
			root, launch := install(t, archive, global)
			args := []string{"argument with spaces", "", "quote\"inside", "--flag=literal", "unicode-é"}
			command := installedCommand(launch, args...)
			if runtime.GOOS == "windows" {
				args = []string{"argument with spaces", "", "--flag=literal", "unicode-é"}
				command = installedCommand(launch, args...)
			}
			command.Stdin = strings.NewReader("input\n")
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			status, ok := err.(*exec.ExitError)
			if !ok || status.ExitCode() != 23 {
				t.Fatalf("exit status: %v; stdout %q; stderr %q", err, stdout.String(), stderr.String())
			}
			var actual []string
			if err := json.Unmarshal(bytes.SplitN(stdout.Bytes(), []byte("\n"), 2)[0], &actual); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(actual, args) || !strings.HasSuffix(stdout.String(), "stdin:input\n") || stderr.String() != "fixture stderr\n" {
				t.Fatalf("forwarded args %q stdout %q stderr %q", actual, stdout.String(), stderr.String())
			}
			if runtime.GOOS == "windows" {
				args = []string{"quote\"inside", "shell & | < > ^ ( ) symbols"}
				command = exec.Command("node", append([]string{filepath.Join(root, "launcher.cjs")}, args...)...)
				output, err := command.Output()
				status, ok := err.(*exec.ExitError)
				if !ok || status.ExitCode() != 23 {
					t.Fatalf("launcher process: %v\n%s", err, output)
				}
				if err := json.Unmarshal(bytes.SplitN(output, []byte("\n"), 2)[0], &actual); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(actual, args) {
					t.Fatalf("launcher process arguments %q; want %q", actual, args)
				}
			}
		})
	}
	if len(fake.Calls("podman")) != 0 || len(fake.Calls("ssh")) != 0 {
		t.Fatal("installation or launch called Podman or SSH")
	}
}

func TestTamperedBinaryFailsInstallationAndEveryLaunch(t *testing.T) {
	archive := fixturePackage(t)
	native := release.LinuxExecutable
	if runtime.GOOS == "windows" {
		native = release.WindowsExecutable
	}
	t.Run("after-install", func(t *testing.T) {
		root, launch := install(t, archive, false)
		binary := filepath.Join(root, native)
		corrupt(t, binary)
		marker := filepath.Join(t.TempDir(), "started")
		command := installedCommand(launch, "paths")
		command.Env = append(os.Environ(), "NPM_TEST_MARKER="+marker)
		output, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "checksum mismatch") || !strings.Contains(string(output), native) {
			t.Fatalf("tampered launch: %v\n%s", err, output)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("tampered binary started: %v", err)
		}
	})
	for _, name := range []string{release.LinuxExecutable, release.WindowsExecutable} {
		t.Run("during-install-"+name, func(t *testing.T) {
			archive := fixturePackage(t)
			dir := filepath.Dir(archive)
			corrupt(t, filepath.Join(dir, name))
			if err := npmpackage.Write(dir, testTag); err != nil {
				t.Fatal(err)
			}
			prefix := t.TempDir()
			output, err := npm(t, prefix, "install", archive, "--foreground-scripts").CombinedOutput()
			if err == nil || !strings.Contains(string(output), "checksum mismatch") || !strings.Contains(string(output), name) {
				t.Fatalf("tampered installation: %v\n%s", err, output)
			}
		})
	}
}

func corrupt(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationRejectsUnsupportedPlatforms(t *testing.T) {
	archive := fixturePackage(t)
	for _, platform := range []struct{ os, arch string }{{"linux", "arm64"}, {"win32", "arm64"}, {"darwin", "x64"}} {
		t.Run(platform.os+"-"+platform.arch, func(t *testing.T) {
			prefix := t.TempDir()
			preload := filepath.Join(prefix, "unsupported.cjs")
			data := "Object.defineProperty(process, 'platform', {value: '" + platform.os + "'}); Object.defineProperty(process, 'arch', {value: '" + platform.arch + "'});"
			if err := os.WriteFile(preload, []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			command := npm(t, prefix, "install", archive, "--foreground-scripts", "--node-options=--require=\""+filepath.ToSlash(preload)+"\"")
			output, err := command.CombinedOutput()
			want := "unsupported platform " + platform.os + "/" + platform.arch
			if err == nil || !strings.Contains(string(output), want) {
				t.Fatalf("unsupported installation: %v\n%s", err, output)
			}
		})
	}
}

func TestLauncherDoesNotConsumeCommandArguments(t *testing.T) {
	archive := fixturePackage(t)
	_, launch := install(t, archive, false)
	command := installedCommand(launch, "--verify-install")
	output, err := command.CombinedOutput()
	if status, ok := err.(*exec.ExitError); !ok || status.ExitCode() != 23 || !strings.HasPrefix(string(output), "[\"--verify-install\"]\n") {
		t.Fatalf("launcher consumed an argument: %v\n%s", err, output)
	}
}

func TestLauncherProvidesProtectedPackagePathsAndReplacesInheritedPaths(t *testing.T) {
	archive := fixturePackage(t)
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "global"}[global], func(t *testing.T) {
			root, launch := install(t, archive, global)
			command := installedCommand(launch, "paths")
			command.Env = append(os.Environ(), `SANDBOXED_AGENTS_NPM_PATHS=["/inherited/untrusted"]`)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("read package paths: %v\n%s", err, output)
			}
			var paths []string
			if err := json.Unmarshal(output, &paths); err != nil {
				t.Fatalf("package paths %q: %v", output, err)
			}
			required := []string{filepath.Join(root, "launcher.cjs"), launch}
			if runtime.GOOS == "windows" {
				required = append(required, launch+".cmd", launch+".ps1")
			}
			for _, path := range required {
				if !slices.Contains(paths, path) {
					t.Fatalf("missing protected path %s from %q", path, paths)
				}
			}
			if slices.Contains(paths, "/inherited/untrusted") {
				t.Fatalf("inherited paths trusted: %q", paths)
			}
			for _, path := range paths {
				if !filepath.IsAbs(path) {
					t.Fatalf("non-absolute package path %q", path)
				}
			}
		})
	}
}

func TestLauncherProtectsAliasLaunchLinks(t *testing.T) {
	archive := fixturePackage(t)
	root, launch := install(t, archive, true)
	aliasDirectory := t.TempDir()
	alias := filepath.Join(aliasDirectory, "another-command")
	if err := os.Symlink(filepath.Join(root, "launcher.cjs"), alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	aliasBin := filepath.Join(t.TempDir(), "bin-alias")
	if err := os.Symlink(filepath.Dir(launch), aliasBin); err != nil {
		t.Fatal(err)
	}
	rawLaunch := filepath.Join(aliasBin, filepath.Base(launch))
	command := installedCommand(rawLaunch, "paths")
	command.Env = append(os.Environ(), "PATH="+aliasDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("alias launch: %v\n%s", err, output)
	}
	var paths []string
	if err := json.Unmarshal(output, &paths); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{alias, rawLaunch, launch, filepath.Join(root, "launcher.cjs")} {
		if !slices.Contains(paths, required) {
			t.Fatalf("missing alias path %s from %q", required, paths)
		}
	}
}

func TestInstallUpgradeAndRemoveLeaveHostPathsUntouched(t *testing.T) {
	fake := testutil.NewFakePrograms(t)
	archive := fixturePackage(t)
	upgradedTag := "v1.0.0-preview.20261003.2"
	if err := npmpackage.Write(filepath.Dir(archive), upgradedTag); err != nil {
		t.Fatal(err)
	}
	upgraded := filepath.Join(filepath.Dir(archive), npmpackage.Filename(upgradedTag))
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "global"}[global], func(t *testing.T) {
			host := t.TempDir()
			for _, name := range []string{"home/.ssh/config", "state/sandboxed-agents/keys", "sandboxes/workspace/keep"} {
				target := filepath.Join(host, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("host sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HOME", filepath.Join(host, "home"))
			t.Setenv("USERPROFILE", filepath.Join(host, "home"))
			t.Setenv("APPDATA", filepath.Join(host, "appdata"))
			t.Setenv("LOCALAPPDATA", filepath.Join(host, "localappdata"))
			t.Setenv("XDG_STATE_HOME", filepath.Join(host, "state"))
			before := tree(t, host)
			prefix := t.TempDir()
			temp := filepath.Join(prefix, "temp")
			if err := os.Mkdir(temp, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(name, temp)
			}
			for _, operation := range [][]string{{"install", archive}, {"install", upgraded}, {"uninstall", "sandboxed-agents"}} {
				args := slices.Clone(operation)
				if global {
					args = append(args, "--global")
				}
				if output, err := npm(t, prefix, args...).CombinedOutput(); err != nil {
					t.Fatalf("npm %s: %v\n%s", args, err, output)
				}
				if after := tree(t, host); !reflect.DeepEqual(before, after) {
					t.Fatalf("npm %s changed host paths: before %v after %v", args, before, after)
				}
			}
			root := filepath.Join(prefix, "node_modules", "sandboxed-agents")
			if global && runtime.GOOS != "windows" {
				root = filepath.Join(prefix, "lib", "node_modules", "sandboxed-agents")
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatalf("uninstall left the package: %v", err)
			}
		})
	}
	if len(fake.Calls("podman")) != 0 || len(fake.Calls("ssh")) != 0 {
		t.Fatal("package lifecycle called Podman or SSH")
	}
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			entries[relative] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
