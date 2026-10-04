package testutil_test

import (
	"bytes"
	"context"
	"os/exec"
	"reflect"
	"testing"

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
