package main_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func installerFixture(t *testing.T, binary []byte) (string, string, *testutil.FakePrograms) {
	t.Helper()
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Fatal("PowerShell 7 is required for Windows NuGet installer process tests")
		}
		t.Skip("PowerShell 7 is required for NuGet installer process tests")
	}
	fake := testutil.NewFakePrograms(t)
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		parent, err := filepath.EvalSymlinks(filepath.Dir(localAppData))
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("LOCALAPPDATA", filepath.Join(parent, filepath.Base(localAppData)))
	}
	packageDir := installerTempDir(t)
	for _, name := range []string{"install-command.ps1", "remove-command.ps1", "command-path.ps1"} {
		contents, err := os.ReadFile(filepath.Join("..", "..", "build", "nuget", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(packageDir, name), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeInstallerBinary(t, packageDir, binary)
	return pwsh, packageDir, fake
}

func installerTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		canonical, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		return canonical
	}
	return dir
}

func TestNuGetInstallerUpgradesAndRemovalKeepsUnrelatedFiles(t *testing.T) {
	pwsh, packageDir, fake := installerFixture(t, []byte("first release\n"))
	installDir := filepath.Join(t.TempDir(), "install")
	for _, binary := range [][]byte{[]byte("first release\n"), []byte("new release\n")} {
		writeInstallerBinary(t, packageDir, binary)
		if output, err := runInstaller(t, pwsh, packageDir, "install-command.ps1", "-InstallDirectory", installDir, "-NoPathUpdate"); err != nil {
			t.Fatalf("install/upgrade: %v\n%s", err, output)
		}
		contents, err := os.ReadFile(filepath.Join(installDir, "sandboxed-agents.exe"))
		if err != nil || string(contents) != string(binary) {
			t.Fatalf("installed command = %q, %v", contents, err)
		}
	}
	if err := os.WriteFile(filepath.Join(installDir, "keep.txt"), []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if output, err := runInstaller(t, pwsh, packageDir, "remove-command.ps1", "-InstallDirectory", installDir, "-NoPathUpdate"); err != nil {
			t.Fatalf("remove: %v\n%s", err, output)
		}
	}
	if _, err := os.Stat(filepath.Join(installDir, "sandboxed-agents.exe")); !os.IsNotExist(err) {
		t.Fatalf("installed command remains: %v", err)
	}
	entries, err := os.ReadDir(installDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("removal changed unrelated files: %v, %v", entries, err)
	}
	contents, err := os.ReadFile(filepath.Join(installDir, "keep.txt"))
	if err != nil || string(contents) != "unrelated" {
		t.Fatalf("unrelated file changed: %q, %v", contents, err)
	}
	if calls := fake.Calls("podman"); len(calls) != 0 {
		t.Fatalf("installation lifecycle called Podman: %v", calls)
	}
}

func writeInstallerBinary(t *testing.T, packageDir string, binary []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(packageDir, "sandboxed-agents-windows-amd64.exe"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	checksums := fmt.Sprintf("%x  sandboxed-agents-windows-amd64.exe\n", sha256.Sum256(binary))
	if err := os.WriteFile(filepath.Join(packageDir, "SHA256SUMS"), []byte(checksums), 0600); err != nil {
		t.Fatal(err)
	}
}

func runInstaller(t *testing.T, pwsh, packageDir, script string, arguments ...string) ([]byte, error) {
	t.Helper()
	args := append([]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", filepath.Join(packageDir, script)}, arguments...)
	return exec.Command(pwsh, args...).CombinedOutput()
}

func TestNuGetInstallerCopiesVerifiedCommandWithoutPathUpdate(t *testing.T) {
	binary := []byte("verified release binary\n")
	pwsh, packageDir, fake := installerFixture(t, binary)
	installDir := filepath.Join(t.TempDir(), "custom directory")
	if output, err := runInstaller(t, pwsh, packageDir, "install-command.ps1", "-InstallDirectory", installDir, "-NoPathUpdate"); err != nil {
		t.Fatalf("install verified command: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(filepath.Join(installDir, "sandboxed-agents.exe"))
	if err != nil || string(contents) != string(binary) {
		t.Fatalf("installed command = %q, %v", contents, err)
	}
	if calls := fake.Calls("podman"); len(calls) != 0 {
		t.Fatalf("installer called Podman: %v", calls)
	}
}

func TestNuGetInstallerUsesPerUserDefaultDirectoryWithoutPathUpdate(t *testing.T) {
	binary := []byte("verified release binary\n")
	pwsh, packageDir, fake := installerFixture(t, binary)
	localAppData := os.Getenv("LOCALAPPDATA")
	if output, err := runInstaller(t, pwsh, packageDir, "install-command.ps1", "-NoPathUpdate"); err != nil {
		t.Fatalf("per-user install: %v\n%s", err, output)
	}
	installDir := filepath.Join(localAppData, "Programs", "sandboxed-agents")
	contents, err := os.ReadFile(filepath.Join(installDir, "sandboxed-agents.exe"))
	if err != nil || string(contents) != string(binary) {
		t.Fatalf("per-user installed command = %q, %v", contents, err)
	}
	if output, err := runInstaller(t, pwsh, packageDir, "remove-command.ps1", "-NoPathUpdate"); err != nil {
		t.Fatalf("per-user remove: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(installDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("per-user command remains: %v, %v", entries, err)
	}
	if calls := fake.Calls("podman"); len(calls) != 0 {
		t.Fatalf("per-user installation lifecycle called Podman: %v", calls)
	}
}

func TestNuGetInstallerRequiresOneValidBinaryChecksum(t *testing.T) {
	for _, checksums := range []string{
		"",
		"not-a-checksum  sandboxed-agents-windows-amd64.exe\n",
		fmt.Sprintf("%x  sandboxed-agents-linux-amd64\n", sha256.Sum256([]byte("release"))),
		fmt.Sprintf("%x  sandboxed-agents-windows-amd64.exe\n%x  sandboxed-agents-windows-amd64.exe\n", sha256.Sum256([]byte("release")), sha256.Sum256([]byte("release"))),
	} {
		t.Run(checksums, func(t *testing.T) {
			pwsh, packageDir, _ := installerFixture(t, []byte("release"))
			installDir := filepath.Join(t.TempDir(), "install")
			if err := os.WriteFile(filepath.Join(packageDir, "SHA256SUMS"), []byte(checksums), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := runInstaller(t, pwsh, packageDir, "install-command.ps1", "-InstallDirectory", installDir, "-NoPathUpdate"); err == nil {
				t.Fatalf("invalid checksum manifest installed command: %s", output)
			}
			if _, err := os.Stat(installDir); !os.IsNotExist(err) {
				t.Fatalf("invalid checksum manifest created installation: %v", err)
			}
		})
	}
}

func TestNuGetInstallerRejectsPathSeparatorsUnlessPathUpdateIsDisabled(t *testing.T) {
	pwsh, packageDir, _ := installerFixture(t, []byte("release"))
	installDir := filepath.Join(t.TempDir(), "directory;with separator")
	for _, script := range []string{"install-command.ps1", "remove-command.ps1"} {
		output, err := runInstaller(t, pwsh, packageDir, script, "-InstallDirectory", installDir)
		if err == nil || !strings.Contains(string(output), "contains ';'") {
			t.Fatalf("PATH separator directory was not explained: %v\n%s", err, output)
		}
		if _, err := os.Stat(installDir); !os.IsNotExist(err) {
			t.Fatalf("rejected PATH separator directory was changed: %v", err)
		}
	}
	for _, script := range []string{"install-command.ps1", "remove-command.ps1"} {
		if output, err := runInstaller(t, pwsh, packageDir, script, "-InstallDirectory", installDir, "-NoPathUpdate"); err != nil {
			t.Fatalf("explicit no-PATH operation rejected directory: %v\n%s", err, output)
		}
	}
}

func TestNuGetInstallerRejectsUnverifiedBinaryBeforeChangingInstallation(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			pwsh, packageDir, fake := installerFixture(t, []byte("verified release binary\n"))
			installDir := filepath.Join(t.TempDir(), "install")
			if existing {
				if err := os.Mkdir(installDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(installDir, "sandboxed-agents.exe"), []byte("previous version"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(packageDir, "sandboxed-agents-windows-amd64.exe"), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := runInstaller(t, pwsh, packageDir, "install-command.ps1", "-InstallDirectory", installDir, "-NoPathUpdate"); err == nil {
				t.Fatalf("tampered binary installed: %s", output)
			}
			if existing {
				contents, err := os.ReadFile(filepath.Join(installDir, "sandboxed-agents.exe"))
				if err != nil || string(contents) != "previous version" {
					t.Fatalf("previous command changed: %q, %v", contents, err)
				}
			} else if _, err := os.Stat(installDir); !os.IsNotExist(err) {
				t.Fatalf("tampered binary created install directory: %v", err)
			}
			if calls := fake.Calls("podman"); len(calls) != 0 {
				t.Fatalf("installer called Podman: %v", calls)
			}
		})
	}
}

func TestNuGetWindowsInstallerPreservesRawUserPathWithoutAdministratorRights(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows user registry test")
	}
	first := filepath.Join(installerTempDir(t), "first.exe")
	upgrade := filepath.Join(installerTempDir(t), "upgrade.exe")
	for _, build := range []struct{ path, version string }{{first, "v1.0.0-preview.20261007.1"}, {upgrade, "v1.0.0-preview.20261007.2"}} {
		command := exec.Command("go", "build", "-ldflags=-X main.version="+build.version, "-o", build.path, "./cmd/sandboxed-agents")
		command.Dir = filepath.Join("..", "..")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build installer version fixture: %v\n%s", err, output)
		}
	}
	binary, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"String", "ExpandString", "Absent"} {
		t.Run(kind, func(t *testing.T) {
			pwsh, packageDir, fake := installerFixture(t, binary)
			script, err := filepath.Abs(filepath.Join("..", "..", "tests", "nuget", "run-user-path.ps1"))
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", script, "-PackageDirectory", packageDir, "-UpgradeBinary", upgrade, "-PathKind", kind, "-PowerShell", pwsh)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("native Windows installer lifecycle: %v\n%s", err, output)
			}
			if calls := fake.Calls("podman"); len(calls) != 0 {
				t.Fatalf("installation lifecycle called Podman: %v", calls)
			}
		})
	}
}
