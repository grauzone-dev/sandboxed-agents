package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func NpmCommand(t testing.TB, prefix string, args ...string) *exec.Cmd {
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
	argv := []string{cli, "--offline", "--no-audit", "--no-fund", "--update-notifier=false", "--ignore-scripts=false", "--prefix", prefix, "--cache", filepath.Join(prefix, "npm-cache")}
	command := exec.Command("node", append(argv, args...)...)
	command.Dir = prefix
	return command
}
