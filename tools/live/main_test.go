package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestMain(m *testing.M) {
	if os.Getenv("SANDBOXED_AGENTS_LIVE_TOOL_FIXTURE") == "1" {
		if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "git" {
			switch strings.Join(os.Args[1:], " ") {
			case "rev-parse HEAD":
				fmt.Println("0123456789abcdef0123456789abcdef01234567")
			case "rev-parse --show-toplevel":
				directory, err := os.Getwd()
				if err != nil {
					os.Exit(1)
				}
				fmt.Println(directory)
			default:
				os.Exit(1)
			}
			os.Exit(0)
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runTool(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	command := exec.Command(os.Args[0], args...)
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_LIVE_TOOL_FIXTURE=1")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	status := 0
	if err := command.Run(); err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		status = exit.ExitCode()
	}
	return status, stdout.String(), stderr.String()
}

func TestToolSkipsLiveSuiteWithoutOptIn(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	output := filepath.Join(t.TempDir(), "records")
	status, stdout, stderr := runTool(t, "-images", "-output", output)
	if status != 0 || !strings.Contains(stdout, "skipped") || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("normal invocation called Podman")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("normal invocation wrote summary: %v", err)
	}
}

func TestToolRefusesUnsafeGroupsBeforePodman(t *testing.T) {
	for _, group := range []string{"default", "invalid.group"} {
		t.Run(group, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", group)
			fakes := testutil.NewFakePrograms(t)
			installGitFixture(t, fakes)
			status, _, stderr := runTool(t, "-opt-in", "-output", t.TempDir())
			if status == 0 || !strings.Contains(stderr, group) {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("rejected invocation called Podman")
			}
		})
	}
}

func TestToolRejectsUnknownOptionsAndUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{"-not-an-option"}, {"extra"}, {"-output"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			installGitFixture(t, fakes)
			status, _, stderr := runTool(t, args...)
			if status == 0 || stderr == "" {
				t.Fatalf("status=%d stderr=%q", status, stderr)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("invalid invocation called Podman")
			}
		})
	}
}

func installGitFixture(t *testing.T, fakes *testutil.FakePrograms) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(fakes.Podman), "git")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
}
