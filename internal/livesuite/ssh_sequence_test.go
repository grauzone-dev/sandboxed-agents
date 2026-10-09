package livesuite

import (
	"context"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestLiveRunReachesSSHAfterLifecycleCleanup(t *testing.T) {
	fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
	config := liveObservationConfig(t, "linux", fixture)
	config.SSH = true
	sshReached := false
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "ssh" {
			sshReached = true
			if fixture.state != "" || fixture.volumes || len(fixture.probes) != 0 {
				t.Fatal("SSH part started before lifecycle cleanup")
			}
			return 42, nil
		}
		return fixture.run(ctx, request)
	}
	if err := Run(context.Background(), config); err == nil || !sshReached {
		t.Fatalf("SSH reached=%t error=%v", sshReached, err)
	}
	summary, _ := readObservationSummary(t, config)
	if !containsCheck(summary.Checks, "lifecycle", "pass") || !containsCheck(summary.Checks, "ssh/client", "fail") || !containsCheck(summary.Checks, "ssh", "fail") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestLiveRunDoesNotReachSSHWhenLifecycleFails(t *testing.T) {
	fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
	config := liveObservationConfig(t, "linux", fixture)
	config.SSH = true
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "ssh" {
			t.Fatal("failed lifecycle reached SSH")
		}
		if len(request.Args) > 0 && request.Args[0] == "up" {
			return 42, nil
		}
		return fixture.run(ctx, request)
	}
	if err := Run(context.Background(), config); err == nil {
		t.Fatal("failed lifecycle passed")
	}
	summary, _ := readObservationSummary(t, config)
	if !containsCheck(summary.Checks, "ssh", "not-run") {
		t.Fatalf("summary=%+v", summary)
	}
}
