package cli_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestMain(m *testing.M) {
	if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "" {
		if err := os.Unsetenv("SANDBOXED_AGENTS_GROUP"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func TestVersionPrintsVersionAndAssetHash(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	stdout, stderr, status := runCLI(t, "production", "version")
	if status != 0 || stdout != "sandboxed-agents v1.2.3\nassets fixture-assets\n" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
		t.Fatal("version ran an external program")
	}
}

func runCLI(t *testing.T, fixture string, args ...string) (string, string, int) {
	t.Helper()
	return runCLIAt(t, "", fixture, args...)
}

func runCLIAt(t *testing.T, directory, fixture string, args ...string) (string, string, int) {
	t.Helper()
	return runCLIWithInput(t, directory, fixture, nil, args...)
}

func runCLIWithInput(t *testing.T, directory, fixture string, input io.Reader, args ...string) (string, string, int) {
	t.Helper()
	command := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	command.Dir = directory
	command.Env = append(os.Environ(), "SANDBOXED_AGENTS_CLI_FIXTURE="+fixture)
	command.Stdin = input
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	status := 0
	if err := command.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			status = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return stdout.String(), stderr.String(), status
}

func TestCLIProcess(t *testing.T) {
	if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "" {
		return
	}
	for index, arg := range os.Args {
		if arg == "--" {
			args := os.Args[index+1:]
			if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "sandbox-host" {
				os.Exit(cli.RunWithHost(args, os.Stdout, os.Stderr, "v1.2.3", "fixture-assets", preflight.Host{Platform: "linux", Run: platform.Run}))
			}
			if strings.HasPrefix(os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE"), "windows") {
				host := platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
				switch os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") {
				case "windows-10":
					host.WindowsBuild = 19045
				case "windows-arm64":
					host.Architecture = "arm64"
				case "windows-server":
					host.WindowsWorkstation = false
				case "windows-unknown":
					host = platform.Host{OS: "windows"}
				}
				hash := "fixture-assets"
				if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "windows-build" {
					hash = assets.Hash()
				}
				os.Exit(cli.RunWithWindowsHost(args, os.Stdout, os.Stderr, "v1.2.3", hash, host))
			}
			if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "nested" {
				tree := cli.Tree{Name: "sandboxed-agents", Commands: []cli.Command{
					{Name: "agents", Commands: []cli.Command{
						{Name: "login", Action: func(invocation *cli.Invocation) error {
							fmt.Fprintln(invocation.Stdout, strings.Join(invocation.Args, " "))
							return nil
						}},
					}},
				}}
				os.Exit(tree.Execute(args, os.Stdout, os.Stderr))
			}
			if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "prepare" {
				tree := checkTree()
				tree.Commands[0].Prepare = func(invocation *cli.Invocation) error {
					command := exec.Command("podman", append([]string{"build"}, invocation.Args...)...)
					command.Stdout, command.Stderr = invocation.Stdout, invocation.Stderr
					return command.Run()
				}
				os.Exit(tree.Execute(args, os.Stdout, os.Stderr))
			}
			if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "checks" {
				os.Exit(checkTree().Execute(args, os.Stdout, os.Stderr))
			}
			if os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE") == "unsupported-preflight" {
				os.Exit(cli.RunWithHost(args, os.Stdout, os.Stderr, "v1.2.3", "fixture-assets", preflight.Host{Platform: "darwin"}))
			}
			if fixture := os.Getenv("SANDBOXED_AGENTS_CLI_FIXTURE"); fixture == "linux-preflight" || fixture == "linux-build" {
				host := preflight.LocalHost()
				host.UID, host.Username = 1000, "fixture"
				if os.Getenv("SANDBOXED_AGENTS_PREFLIGHT_AS_ROOT") != "" {
					host.UID = 0
				}
				if os.Getenv("SANDBOXED_AGENTS_UNKNOWN_ACCOUNT") != "" {
					host.Username = ""
				}
				root := os.Getenv("SANDBOXED_AGENTS_HOST_FIXTURE")
				host.ReadFile = func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(root, path)) }
				host.Writable = func(path string) bool {
					info, err := os.Stat(filepath.Join(root, path))
					if err != nil {
						return false
					}
					mode := os.Getenv("SANDBOXED_AGENTS_NO_DELEGATION")
					return mode == "" || (mode == "directory" && !info.IsDir())
				}
				hash := "fixture-assets"
				if fixture == "linux-build" {
					hash = assets.Hash()
				}
				os.Exit(cli.RunWithHost(args, os.Stdout, os.Stderr, "v1.2.3", hash, host))
			}
			os.Exit(cli.Run(args, os.Stdout, os.Stderr, "v1.2.3", "fixture-assets"))
		}
	}
	t.Fatal("missing CLI arguments")
}

func TestInvalidUsageReportsAnError(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown"}, {"--unknown"}, {"version", "--unknown"}, {"version", "extra"}, {"version", "extra", "--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, "production", args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("invalid usage ran an external program")
			}
		})
	}
}

func TestCommandPathPrecedesSandboxName(t *testing.T) {
	stdout, stderr, status := runCLI(t, "nested", "agents", "login", "version", "codex")
	if status != 0 || stdout != "version codex\n" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func checkTree() cli.Tree {
	check := func(name string) cli.Handler {
		return func(invocation *cli.Invocation) error {
			for _, failure := range strings.Split(os.Getenv("SANDBOXED_AGENTS_FAIL_CHECKS"), ",") {
				if failure == name {
					return errors.New(name + " failed")
				}
			}
			if name == "usage" {
				return nil
			}
			command := exec.Command("podman", "check", name)
			command.Stdout, command.Stderr = invocation.Stdout, invocation.Stderr
			return command.Run()
		}
	}
	return cli.Tree{Name: "sandboxed-agents", Commands: []cli.Command{
		{Name: "stand-in", Checks: cli.Checks{
			Usage: check("usage"), Preflight: check("preflight"),
			Sandbox: check("sandbox"), Owner: check("owner"),
			InterruptedUpdate: check("interrupted-update"), Running: check("running"),
			Preconditions: check("preconditions"), Terminal: check("terminal"),
			SessionGuard: check("session-guard"),
		}, Action: func(invocation *cli.Invocation) error {
			command := exec.Command("ssh", append([]string{"action"}, invocation.Args...)...)
			command.Stdout, command.Stderr = invocation.Stdout, invocation.Stderr
			return command.Run()
		}},
	}}
}

func TestChecksReportEarliestFailure(t *testing.T) {
	steps := []string{"usage", "preflight", "sandbox", "owner", "interrupted-update", "running", "preconditions", "terminal", "session-guard"}
	for index := 0; index < len(steps)-1; index++ {
		t.Run(steps[index]+" before "+steps[index+1], func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			fakes.Script("podman", make([]testutil.Response, index)...)
			t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", steps[index]+","+steps[index+1])
			stdout, stderr, status := runCLI(t, "checks", "stand-in", "sandbox01")
			if status == 0 || stdout != "" || !strings.Contains(stderr, steps[index]+" failed") || strings.Contains(stderr, steps[index+1]+" failed") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			var expected []testutil.Call
			for prior := 1; prior < index; prior++ {
				expected = append(expected, testutil.Call{Args: []string{"check", steps[prior]}})
			}
			if got := fakes.Calls("podman"); !reflect.DeepEqual(got, expected) {
				t.Fatalf("calls=%v want=%v", got, expected)
			}
			if len(fakes.Calls("ssh")) != 0 {
				t.Fatal("a rejected command ran its action")
			}
		})
	}
}

func TestSuccessfulChecksReachTheActionInOrder(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman",
		testutil.Response{Stdout: "preflight\n"},
		testutil.Response{Stdout: "sandbox\n"},
		testutil.Response{Stdout: "owner\n"},
		testutil.Response{Stdout: "interrupted-update\n"},
		testutil.Response{Stdout: "running\n"},
		testutil.Response{Stdout: "preconditions\n"},
		testutil.Response{Stdout: "terminal\n"},
		testutil.Response{Stdout: "session-guard\n"},
	)
	fakes.Script("ssh", testutil.Response{Stdout: "completed\n"})
	t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", "")
	stdout, stderr, status := runCLI(t, "checks", "stand-in", "sandbox01")
	if status != 0 || stdout != "preflight\nsandbox\nowner\ninterrupted-update\nrunning\npreconditions\nterminal\nsession-guard\ncompleted\n" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	want := []testutil.Call{
		{Args: []string{"check", "preflight"}},
		{Args: []string{"check", "sandbox"}},
		{Args: []string{"check", "owner"}},
		{Args: []string{"check", "interrupted-update"}},
		{Args: []string{"check", "running"}},
		{Args: []string{"check", "preconditions"}},
		{Args: []string{"check", "terminal"}},
		{Args: []string{"check", "session-guard"}},
	}
	if got := fakes.Calls("podman"); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
	if got := fakes.Calls("ssh"); !reflect.DeepEqual(got, []testutil.Call{{Args: []string{"action", "sandbox01"}}}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestActionFailureUsesANonzeroStatus(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", make([]testutil.Response, 8)...)
	fakes.Script("ssh", testutil.Response{Stderr: "refused\n", ExitCode: 7})
	t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", "")
	stdout, stderr, status := runCLI(t, "checks", "stand-in", "sandbox01")
	if status != 1 || stdout != "" || !strings.Contains(stderr, "refused") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
}

func TestPreparationFinishesBeforeTheFinalGuardAndAction(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := append(make([]testutil.Response, 7), testutil.Response{Stdout: "built\n"}, testutil.Response{Stdout: "guarded\n"})
	fakes.Script("podman", responses...)
	fakes.Script("ssh", testutil.Response{Stdout: "changed\n"})
	t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", "")
	stdout, stderr, status := runCLI(t, "prepare", "stand-in", "sandbox01")
	if status != 0 || stdout != "built\nguarded\nchanged\n" || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 9 || !reflect.DeepEqual(calls[7:], []testutil.Call{{Args: []string{"build", "sandbox01"}}, {Args: []string{"check", "session-guard"}}}) {
		t.Fatalf("calls=%v", calls)
	}
	if got := fakes.Calls("ssh"); !reflect.DeepEqual(got, []testutil.Call{{Args: []string{"action", "sandbox01"}}}) {
		t.Fatalf("calls=%v", got)
	}
}

func TestFailedPreparationSkipsTheFinalGuardAndAction(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := append(make([]testutil.Response, 7), testutil.Response{Stderr: "build failed\n", ExitCode: 7})
	fakes.Script("podman", responses...)
	t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", "")
	stdout, stderr, status := runCLI(t, "prepare", "stand-in", "sandbox01")
	if status == 0 || stdout != "" || !strings.Contains(stderr, "build failed") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 8 || !reflect.DeepEqual(calls[7].Args, []string{"build", "sandbox01"}) {
		t.Fatalf("calls=%v", calls)
	}
	if len(fakes.Calls("ssh")) != 0 {
		t.Fatal("failed preparation ran its action")
	}
}

func TestFinalGuardCanRefuseAnActionAfterPreparation(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	responses := append(make([]testutil.Response, 7), testutil.Response{Stdout: "built\n"})
	fakes.Script("podman", responses...)
	t.Setenv("SANDBOXED_AGENTS_FAIL_CHECKS", "session-guard")
	stdout, stderr, status := runCLI(t, "prepare", "stand-in", "sandbox01")
	if status == 0 || stdout != "built\n" || !strings.Contains(stderr, "session-guard failed") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 8 || !reflect.DeepEqual(calls[7].Args, []string{"build", "sandbox01"}) {
		t.Fatalf("calls=%v", calls)
	}
	if len(fakes.Calls("ssh")) != 0 {
		t.Fatal("a rejected command ran its action")
	}
}

func TestUnknownOptionsAreNamedAsOptions(t *testing.T) {
	for _, test := range []struct {
		fixture string
		args    []string
	}{
		{fixture: "production", args: []string{"--unknown"}},
		{fixture: "nested", args: []string{"agents", "--unknown"}},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			stdout, stderr, status := runCLI(t, test.fixture, test.args...)
			if status == 0 || stdout != "" || !strings.Contains(stderr, "unknown option") || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("ssh")) != 0 {
				t.Fatal("invalid usage ran an external program")
			}
		})
	}
}

func TestCheckUsesNativeOperatingSystem(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	if runtime.GOOS == "linux" {
		fakes.Script("podman", testutil.Response{Stdout: "podman version 5.0.0\n"})
		stdout, _, _ := runCLI(t, "production", "check")
		if !strings.Contains(stdout, "OK: Podman version") {
			t.Fatalf("native Linux check output=%q", stdout)
		}
		if calls := fakes.Calls("podman"); !reflect.DeepEqual(calls, []testutil.Call{{Args: []string{"--version"}}}) {
			t.Fatalf("native Linux Podman calls=%v", calls)
		}
	} else if runtime.GOOS == "windows" {
		fakes.Script("podman", healthyWindowsPodman()...)
		stdout, _, _ := runCLI(t, "production", "check")
		if !strings.Contains(stdout, "ok: Podman client") {
			t.Fatalf("native Windows check output=%q", stdout)
		}
		calls := fakes.Calls("podman")
		if len(calls) != 7 || !reflect.DeepEqual(calls[1].Args, []string{"machine", "list", "--format", "json"}) {
			t.Fatalf("native Windows Podman calls=%v", calls)
		}
	} else {
		stdout, _, status := runCLI(t, "production", "check")
		if status == 0 || !strings.Contains(stdout, "Prerequisite checks are not available for this operating system yet") {
			t.Fatalf("native unsupported check status=%d output=%q", status, stdout)
		}
		if len(fakes.Calls("podman")) != 0 {
			t.Fatal("unsupported host check ran Podman")
		}
	}
	if len(fakes.Calls("ssh")) != 0 || len(fakes.Calls("ssh-keygen")) != 0 {
		t.Fatal("native check ran an unexpected host program")
	}
}

func TestCheckExplainsUnsupportedOperatingSystem(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	stdout, stderr, status := runCLI(t, "unsupported-preflight", "check")
	if status == 0 || stderr == "" || !strings.Contains(stdout, "Prerequisite checks are not available for this operating system yet") || strings.Contains(stdout, "Linux hosts only") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if len(fakes.Calls("podman")) != 0 || len(fakes.Calls("getent")) != 0 || len(fakes.Calls("ssh")) != 0 || len(fakes.Calls("ssh-keygen")) != 0 {
		t.Fatal("unsupported host check ran an external program")
	}
}
