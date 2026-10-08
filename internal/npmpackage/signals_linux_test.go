package npmpackage_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/npmpackage"
	"github.com/grauzone-dev/sandboxed-agents/internal/release"
)

func signalPackage(t *testing.T, signal string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nkill -" + signal + " $$\n"
	if err := os.WriteFile(filepath.Join(dir, release.LinuxExecutable), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, release.WindowsExecutable), []byte("unused binary"), 0755); err != nil {
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

func runSignalledCommand(t *testing.T, signal, nodeOptions string) (*exec.ExitError, []byte) {
	t.Helper()
	_, launch := install(t, signalPackage(t, signal), false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, launch)
	command.Env = append(os.Environ(), "NODE_OPTIONS="+nodeOptions)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("signalled launcher did not exit: %v\n%s", ctx.Err(), output)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child terminated by SIG%s, launcher exit: %v\n%s", signal, err, output)
	}
	return exit, output
}

func TestInstalledCommandFailsWhenNodeHandlesChildSignal(t *testing.T) {
	preload := filepath.Join(t.TempDir(), "signal handler.cjs")
	if err := os.WriteFile(preload, []byte("process.on('SIGUSR1', () => {});\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exit, output := runSignalledCommand(t, "USR1", "--require="+strconv.Quote(preload))
	if exit.ExitCode() != 138 {
		t.Fatalf("SIGUSR1 fallback exit code=%d; want 138; termination=%v\n%s", exit.ExitCode(), exit.Sys(), output)
	}
}

func TestInstalledCommandPreservesChildSignalTermination(t *testing.T) {
	exit, output := runSignalledCommand(t, "TERM", "--inspect-port=0")
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("SIGTERM launcher termination=%v; want SIGTERM\n%s", exit.Sys(), output)
	}
}
