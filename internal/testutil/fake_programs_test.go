package testutil_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestFakeProgramsReturnScriptedOutputAndRecordExactArguments(t *testing.T) {
	f := testutil.NewFakePrograms(t)
	f.Script("podman", testutil.Response{Stdout: "sandbox-ready\n", Stderr: "warning\n", ExitCode: 7})
	cmd := exec.CommandContext(context.Background(), "podman", "inspect", "name with spaces", "--format={{.State}}", "")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v, want 7", err)
	}
	if stdout.String() != "sandbox-ready\n" || stderr.String() != "warning\n" {
		t.Fatalf("streams = %q, %q", stdout.String(), stderr.String())
	}
	want := []testutil.Call{{Args: []string{"inspect", "name with spaces", "--format={{.State}}", ""}}}
	if got := f.Calls("podman"); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
	if got := f.Calls("ssh"); len(got) != 0 {
		t.Fatalf("ssh calls = %#v", got)
	}
}

func TestFakeProgramsRepeatAProbeUntilTheNextOperation(t *testing.T) {
	f := testutil.NewFakePrograms(t)
	f.Script("podman", testutil.Response{RepeatForArgs: []string{"exec", "version"}, Stdout: "not ready\n"}, testutil.Response{Stdout: "removed\n"})
	for _, args := range [][]string{{"exec", "version"}, {"exec", "version"}, {"rm", "sandbox"}} {
		output, err := exec.Command("podman", args...).Output()
		want := "not ready\n"
		if args[0] == "rm" {
			want = "removed\n"
		}
		if err != nil || string(output) != want {
			t.Fatalf("args=%v output=%q error=%v", args, output, err)
		}
	}
}

func TestFakeProgramsRepeatedProbesLeaveScriptedResponsesUntouched(t *testing.T) {
	f := testutil.NewFakePrograms(t)
	f.Script("podman", testutil.Response{RepeatForArgs: []string{"exec", "version"}, Stdout: "not ready\n"}, testutil.Response{Stdout: "removed\n"})
	path := filepath.Join(filepath.Dir(filepath.Dir(f.Podman)), "podman.responses.json")
	stamp := time.Unix(1, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		output, err := exec.Command("podman", "exec", "version").Output()
		if err != nil || string(output) != "not ready\n" {
			t.Fatalf("output=%q error=%v", output, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(stamp) {
			t.Fatal("repeated probe rewrote the response script, exposing it to cancellation")
		}
	}
	output, err := exec.Command("podman", "rm", "sandbox").Output()
	if err != nil || string(output) != "removed\n" {
		t.Fatalf("next operation output=%q error=%v", output, err)
	}
}
