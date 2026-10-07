package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func NpmCLIPath(t testing.TB) string {
	t.Helper()
	binary, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
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
	return cli
}

func NpmCommand(t testing.TB, prefix string, args ...string) *exec.Cmd {
	t.Helper()
	argv := []string{NpmCLIPath(t), "--offline", "--no-audit", "--no-fund", "--update-notifier=false", "--ignore-scripts=false", "--prefix", prefix, "--cache", filepath.Join(prefix, "npm-cache")}
	command := exec.Command("node", append(argv, args...)...)
	command.Dir = prefix
	return command
}

func NpmInstallationPaths(prefix string, global bool) (root, launch string) {
	root = filepath.Join(prefix, "node_modules", "sandboxed-agents")
	launch = filepath.Join(prefix, "node_modules", ".bin", "sandboxed-agents")
	if global {
		if runtime.GOOS == "windows" {
			launch = filepath.Join(prefix, "sandboxed-agents")
		} else {
			root = filepath.Join(prefix, "lib", "node_modules", "sandboxed-agents")
			launch = filepath.Join(prefix, "bin", "sandboxed-agents")
		}
	}
	return root, launch
}
