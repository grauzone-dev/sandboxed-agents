package sandbox_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func lifecycleLockState(t *testing.T) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("LOCALAPPDATA", state)
}

func lifecycleLockPath(t *testing.T) string {
	t.Helper()
	state, err := controllergroup.StateDirectory(runtime.GOOS, "default")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(state, "locks", "71a65e50658bc54a4205d968042fa9b457e81c94f5c8930209ae3b059088fb8d")
}

func TestLifecycleLockLeavesAnEmptyRegularFileAtItsStableHashedPath(t *testing.T) {
	lifecycleLockState(t)
	release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	path := lifecycleLockPath(t)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Mode().IsRegular() || before.Size() != 0 {
		t.Fatalf("lock file is not an empty regular file: %v", before)
	}
	release()
	for range 2 {
		release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
		if err != nil {
			t.Fatal(err)
		}
		release()
		after, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) || after.Size() != 0 {
			t.Fatal("release replaced, removed, or wrote the persistent lock file")
		}
	}
}

func TestLifecycleLockRefusesNonregularFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			lifecycleLockState(t)
			path := lifecycleLockPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("creating symlinks unavailable: %v", err)
					}
					t.Fatal(err)
				}
				t.Cleanup(func() {
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "untouched" {
						t.Errorf("symlink target changed: %q, %v", data, err)
					}
				})
			case "fifo":
				if runtime.GOOS != "linux" {
					t.Skip("named pipes are tested on Linux")
				}
				if err := exec.Command("mkfifo", path).Run(); err != nil {
					t.Fatal(err)
				}
			}
			if release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01"); err == nil {
				release()
				t.Fatalf("accepted %s as a lifecycle lock", kind)
			} else if release != nil {
				t.Fatal("failed acquisition returned a release function")
			}
		})
	}
}

func TestLifecycleLockProcess(t *testing.T) {
	mode := os.Getenv("SANDBOXED_AGENTS_TEST_LIFECYCLE_LOCK")
	if mode == "" {
		return
	}
	release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if mode == "hold" {
		fmt.Fprintln(os.Stdout, "locked")
		io.Copy(io.Discard, os.Stdin)
	}
	release()
	os.Exit(0)
}

func lifecycleLockProcess(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifecycleLockProcess$")
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_TEST_LIFECYCLE_LOCK="+mode)
	return command
}

func TestLifecycleLockRefusesAnotherProcess(t *testing.T) {
	lifecycleLockState(t)
	release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	output, commandErr := lifecycleLockProcess(t, "try").CombinedOutput()
	if commandErr == nil {
		t.Fatal("another process acquired the locked sandbox")
	}
	if !strings.Contains(string(output), "agent01") || !strings.Contains(string(output), "retry") {
		t.Fatalf("contention did not identify the sandbox and retry: %s", output)
	}
	release()
	if output, err := lifecycleLockProcess(t, "try").CombinedOutput(); err != nil {
		t.Fatalf("another process could not acquire released lock: %v: %s", err, output)
	}
}

func TestLifecycleLocksAllowDifferentSandboxNamesAndControllerGroups(t *testing.T) {
	lifecycleLockState(t)
	for _, key := range []struct{ group, name string }{
		{"default", "agent01"},
		{"default", "Agent01"},
		{"default", "other"},
		{"team-a", "agent01"},
		{"con", "nul"},
	} {
		release, err := sandbox.LockLifecycle(runtime.GOOS, key.group, key.name)
		if err != nil {
			t.Fatalf("independent sandbox %s/%s blocked: %v", key.group, key.name, err)
		}
		t.Cleanup(release)
	}
}

func TestLifecycleLockIsReleasedWhenHoldingProcessDies(t *testing.T) {
	lifecycleLockState(t)
	command := lifecycleLockProcess(t, "hold")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close() })
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command.Process.Kill()
		command.Wait()
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(output).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatalf("process did not acquire its lock: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not report acquiring its lock")
	}
	if release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01"); err == nil {
		release()
		t.Fatal("holding process did not exclude a lifecycle command")
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	command.Wait()
	release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		t.Fatalf("dead process left sandbox locked: %v", err)
	}
	release()
}

func TestLifecycleLockRefusesConcurrentCommandAndCanBeReleased(t *testing.T) {
	lifecycleLockState(t)
	release, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	started := time.Now()
	secondRelease, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err == nil {
		secondRelease()
		t.Fatal("concurrent lifecycle command acquired the same sandbox")
	}
	if time.Since(started) > time.Second {
		t.Fatal("contending lifecycle command did not fail immediately")
	}
	if secondRelease != nil {
		t.Fatal("failed acquisition returned a release function")
	}
	release()
	nextRelease, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01")
	if err != nil {
		t.Fatalf("released sandbox remains locked: %v", err)
	}
	release()
	if unwantedRelease, err := sandbox.LockLifecycle(runtime.GOOS, "default", "agent01"); err == nil {
		unwantedRelease()
		t.Fatal("repeated release unlocked a later lifecycle command")
	}
	nextRelease()
}
