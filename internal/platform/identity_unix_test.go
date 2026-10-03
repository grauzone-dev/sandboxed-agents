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

func TestRequestedIdentityIsNeverSilentlyIgnored(t *testing.T) {
	requested := process.Identity{UID: 1000, GID: 1000}
	if os.Geteuid() != 0 {
		requested = process.Identity{UID: uint32(os.Geteuid()) + 1, GID: uint32(os.Getegid()) + 1}
	}
	var out bytes.Buffer
	status, err := platform.Run(context.Background(), process.Request{Name: "/bin/sh", Args: []string{"-c", "id -u; id -g; id -G"}, User: &requested, Dir: "/", Streams: process.Streams{Stdout: &out}})
	if os.Geteuid() == 0 {
		if err != nil || status != 0 || out.String() != "1000\n1000\n1000\n" {
			t.Fatalf("privilege drop status=%d err=%v output=%q", status, err, &out)
		}
	} else if err == nil || status == 0 || out.Len() != 0 {
		t.Fatalf("unprivileged switch must fail before execution: status=%d err=%v output=%q", status, err, &out)
	}
}
