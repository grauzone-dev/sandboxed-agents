package npmpackage_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

func runSignalledCommand(t *testing.T, signal string) *exec.ExitError {
	t.Helper()
	_, launch := install(t, signalPackage(t, signal), false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, launch)
	command.Env = append(os.Environ(), "NODE_OPTIONS=--inspect-port=0")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("signalled launcher did not exit: %v\n%s", ctx.Err(), output)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("child terminated by SIG%s, launcher exit: %v\n%s", signal, err, output)
	}
	return exit
}

func TestInstalledCommandFailsWhenNodeHandlesChildSignal(t *testing.T) {
	exit := runSignalledCommand(t, "USR1")
	if exit.ExitCode() != 138 {
		t.Fatalf("SIGUSR1 fallback exit code=%d; want 138", exit.ExitCode())
	}
}

func TestInstalledCommandPreservesChildSignalTermination(t *testing.T) {
	exit := runSignalledCommand(t, "TERM")
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("SIGTERM launcher termination=%v; want SIGTERM", exit.Sys())
	}
}
