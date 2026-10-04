package cli_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestUpdateCanRestoreAStoppedSandboxAfterCallerCancellationDuringReadiness(t *testing.T) {
	for _, probe := range []string{"podman", "ssh-keyscan"} {
		t.Run(probe, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			responses := updateObjectResponses(t, false, "old-image", "", "")[1:]
			responses = append(responses, testutil.Response{}, testutil.Response{Stdout: `[{"Id":"new-image"}]`}, testutil.Response{}, testutil.Response{}, testutil.Response{}, testutil.Response{Stdout: "sandboxed-agents-manager v1.2.3\n"}, testutil.Response{}, testutil.Response{})
			fakes.Script("podman", responses...)
			fakes.Script("ssh-keyscan", testutil.Response{Stdout: updateSSHKey(t)})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stdout, stderr bytes.Buffer
			cleanupCalls := 0
			update := sandbox.NewUpdate("agent01", "default", "fixture-assets", func(requestCtx context.Context, request process.Request) (int, error) {
				if ctx.Err() != nil {
					cleanupCalls++
					deadline, present := requestCtx.Deadline()
					if requestCtx.Err() != nil || !present || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
						t.Fatalf("rollback context error=%v deadline=%v present=%v", requestCtx.Err(), deadline, present)
					}
				}
				status, err := platform.Run(requestCtx, request)
				if request.Name == probe && (probe == "ssh-keyscan" || request.Args[0] == "exec") {
					cancel()
				}
				return status, err
			}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			for _, prepare := range []func(context.Context) error{update.CheckContainer, update.CheckOwner, update.Prepare} {
				if err := prepare(ctx); err != nil {
					t.Fatal(err)
				}
			}
			err := update.Apply(ctx)
			if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "restored") || cleanupCalls != 2 {
				t.Fatalf("error=%v cleanup calls=%d", err, cleanupCalls)
			}
			want := [][]string{
				{"rename", "sandboxed-agents.default.agent01", "sandboxed-agents-backup.default.agent01"}, {"create"},
				{"start", "sandboxed-agents.default.agent01"}, {"rm", "--force", "--ignore", "sandboxed-agents.default.agent01"},
				{"rename", "sandboxed-agents-backup.default.agent01", "sandboxed-agents.default.agent01"},
			}
			if changes := assertUpdateChangesPreserveData(t, fakes); !reflect.DeepEqual(changes, want) {
				t.Fatalf("changes=%v want=%v", changes, want)
			}
		})
	}
}
