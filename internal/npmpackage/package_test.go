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
	"strconv"
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
	native := nativeExecutable()
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

func install(t *testing.T, archive string, global bool) (string, string) {
	t.Helper()
	prefix := t.TempDir()
	args := []string{"install", archive}
	if global {
		args = append(args, "--global")
	}
	command := testutil.NpmCommand(t, prefix, args...)
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
			_, launch := install(t, archive, global)
			args := []string{"argument with spaces", "", "quote\"inside", "shell & | < > ^ ( ) symbols", "--flag=literal", "unicode-é"}
			command := installedCommand(launch, args...)
			if runtime.GOOS == "windows" {
				command = cmdShimCommand(t, launch, args)
			}
			assertForwarding(t, command, args)
		})
	}
	if len(fake.Calls("podman")) != 0 || len(fake.Calls("ssh")) != 0 {
		t.Fatal("installation or launch called Podman or SSH")
	}
}

func cmdShimCommand(t *testing.T, launch string, args []string) *exec.Cmd {
	t.Helper()
	dir := t.TempDir()
	source := "@echo off\r\n\"%NPM_TEST_SHIM%\""
	env := append(os.Environ(), "NPM_TEST_SHIM="+launch+".cmd")
	for index, arg := range args {
		key := "NPM_TEST_ARGUMENT_" + strconv.Itoa(index)
		env = append(env, key+"="+strings.ReplaceAll(arg, "\"", "\"\""))
		source += " \"%" + key + "%\""
	}
	source += "\r\n"
	if err := os.WriteFile(filepath.Join(dir, "forward.cmd"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("cmd.exe", "/d", "/v:off", "/c", "forward.cmd")
	command.Env = env
	command.Dir = dir
	return command
}

func TestInstalledPowerShellShimForwardsArgumentsStreamsAndStatus(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		if runtime.GOOS == "windows" {
			t.Fatal(err)
		}
		t.Skip("PowerShell is unavailable")
	}
	archive := fixturePackage(t)
	for _, global := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "global"}[global], func(t *testing.T) {
			root, launch := install(t, archive, global)
			if runtime.GOOS != "windows" {
				npm := testutil.NpmCommand(t, t.TempDir())
				module := filepath.Join(filepath.Dir(filepath.Dir(npm.Args[1])), "node_modules", "cmd-shim")
				generate := exec.Command("node", "-e", `require(process.argv[1])(process.argv[2],process.argv[3]).catch(error=>{console.error(error);process.exit(1)})`, module, filepath.Join(root, "launcher.cjs"), launch)
				if output, err := generate.CombinedOutput(); err != nil {
					t.Fatalf("generate npm PowerShell shim: %v\n%s", err, output)
				}
			}
			args := []string{"argument with spaces", "", "quote\"inside", "shell & | < > ^ ( ) symbols", "--flag=literal", "unicode-é"}
			literals := make([]string, 0, len(args))
			for _, arg := range args {
				literals = append(literals, "'"+strings.ReplaceAll(arg, "'", "''")+"'")
			}
			arguments := "$arguments=@(" + strings.Join(literals, ",") + "); "
			source := arguments + "& '" + strings.ReplaceAll(launch+".ps1", "'", "''") + "' @arguments; exit $LASTEXITCODE"
			command := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", source)
			referenceSource := arguments + "& '" + strings.ReplaceAll(filepath.Join(root, nativeExecutable()), "'", "''") + "' @arguments; exit $LASTEXITCODE"
			reference := exec.Command("pwsh", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", referenceSource)
			stdout, stderr := captureForwarding(t, command, args)
			wantStdout, wantStderr := captureForwarding(t, reference, args)
			if !bytes.Equal(stdout, wantStdout) || !bytes.Equal(stderr, wantStderr) {
				t.Fatalf("shim streams stdout %q stderr %q; native stdout %q stderr %q", stdout, stderr, wantStdout, wantStderr)
			}
		})
	}
}

func assertForwarding(t *testing.T, command *exec.Cmd, args []string) {
	t.Helper()
	stdout, stderr := captureForwarding(t, command, args)
	parts := bytes.SplitN(stdout, []byte("\n"), 2)
	if len(parts) != 2 || string(parts[1]) != "stdin:input\n" || string(stderr) != "fixture stderr\n" {
		t.Fatalf("forwarded stdout %q stderr %q", stdout, stderr)
	}
}

func captureForwarding(t *testing.T, command *exec.Cmd, args []string) ([]byte, []byte) {
	t.Helper()
	command.Stdin = strings.NewReader("input\n")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	status, ok := err.(*exec.ExitError)
	if !ok || status.ExitCode() != 23 {
		t.Fatalf("exit status: %v; stdout %q; stderr %q", err, stdout.String(), stderr.String())
	}
	parts := bytes.SplitN(stdout.Bytes(), []byte("\n"), 2)
	if len(parts) != 2 {
		t.Fatalf("stdout %q", stdout.String())
	}
	var actual []string
	if err := json.Unmarshal(parts[0], &actual); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actual, args) {
		t.Fatalf("forwarded args %q; want %q", actual, args)
	}
	return stdout.Bytes(), stderr.Bytes()
}

func TestTamperedBinaryFailsInstallationAndEveryLaunch(t *testing.T) {
	archive := fixturePackage(t)
	native := nativeExecutable()
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
			output, err := testutil.NpmCommand(t, prefix, "install", archive, "--foreground-scripts").CombinedOutput()
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
			command := testutil.NpmCommand(t, prefix, "install", archive, "--foreground-scripts", "--node-options=--require=\""+filepath.ToSlash(preload)+"\"")
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
				if output, err := testutil.NpmCommand(t, prefix, args...).CombinedOutput(); err != nil {
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

func nativeExecutable() string {
	if runtime.GOOS == "windows" {
		return release.WindowsExecutable
	}
	return release.LinuxExecutable
}
