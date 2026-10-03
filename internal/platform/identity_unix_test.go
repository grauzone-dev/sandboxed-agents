//go:build !windows

package platform_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestProcessRunsWithRequestedIdentityAndDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("same-user case requires a non-root test process")
	}
	var out bytes.Buffer
	dir := t.TempDir()
	status, err := platform.Run(context.Background(), process.Request{Name: "/bin/sh", Args: []string{"-c", "id -u; id -g; pwd"}, User: &process.Identity{UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}, Dir: dir, Streams: process.Streams{Stdout: &out}})
	want := fmt.Sprintf("%d\n%d\n%s\n", os.Geteuid(), os.Getegid(), dir)
	if err != nil || status != 0 || out.String() != want {
		t.Fatalf("status=%d err=%v out=%q want=%q", status, err, &out, want)
	}
}
