package platform_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestRunReturnsExternalStatusAndStreams(t *testing.T) {
	f := testutil.NewFakePrograms(t)
	f.Script("ssh", testutil.Response{Stdout: "first\n", Stderr: "failure\n", ExitCode: 23}, testutil.Response{Stdout: "second\n"})
	var stdout, stderr bytes.Buffer
	request := process.Request{Name: "ssh", Args: []string{"-p", "2222", "sandbox", "exit 23"}, Streams: process.Streams{Stdout: &stdout, Stderr: &stderr}}
	code, err := platform.Run(context.Background(), request)
	if err != nil || code != 23 {
		t.Fatalf("result = %d, %v; want 23, nil", code, err)
	}
	if stdout.String() != "first\n" || stderr.String() != "failure\n" {
		t.Fatalf("streams = %q, %q", stdout.String(), stderr.String())
	}
	code, err = platform.Run(context.Background(), process.Request{Name: f.SSH, Args: []string{"sandbox", "true"}, Streams: process.Streams{Stdout: &stdout}})
	if err != nil || code != 0 || stdout.String() != "first\nsecond\n" {
		t.Fatalf("second result = %d, %v, %q", code, err, stdout.String())
	}
	want := []testutil.Call{{Args: []string{"-p", "2222", "sandbox", "exit 23"}}, {Args: []string{"sandbox", "true"}}}
	if got := f.Calls("ssh"); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
}
