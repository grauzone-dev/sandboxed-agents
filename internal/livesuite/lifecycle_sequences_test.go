package livesuite

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestLiveRunRejectsIncorrectLifecycleStatesAndReplacedVolumes(t *testing.T) {
	for _, failure := range []string{"stop", "start", "default-group", "volume-replaced"} {
		t.Run(failure, func(t *testing.T) {
			fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
			config := liveObservationConfig(t, "linux", fixture)
			failedCheck := map[string]string{
				"stop":            "lifecycle/stopped/list-stopped",
				"start":           "lifecycle/started-again/list-running",
				"default-group":   "lifecycle/created/default-group-absent",
				"volume-replaced": "lifecycle/removed/volumes-preserved",
			}[failure]
			config.Run = func(ctx context.Context, request process.Request) (int, error) {
				if request.Name != "git" && request.Name != "go" && request.Name != "sh" && request.Name != "podman" {
					if request.Args[0] == failure {
						return 0, nil
					}
					if failure == "default-group" && request.Args[0] == "list" && fixture.state != "" && containsValue(request.Env, "SANDBOXED_AGENTS_GROUP=default") {
						fmt.Fprint(request.Streams.Stdout, observationListHeader)
						fmt.Fprintf(request.Streams.Stdout, "%s running volume 2222 - none -\n", fixture.name)
						return 0, nil
					}
				}
				if failure == "volume-replaced" && request.Name == "podman" && request.Args[0] == "volume" && request.Args[1] == "inspect" && fixture.state == "volumes only" {
					var output bytes.Buffer
					original := request.Streams.Stdout
					request.Streams.Stdout = &output
					status, err := fixture.run(ctx, request)
					fmt.Fprint(original, strings.ReplaceAll(output.String(), "2026-10-08T00:00:00Z", "2026-10-09T00:00:00Z"))
					return status, err
				}
				return fixture.run(ctx, request)
			}
			if err := Run(context.Background(), config); err == nil {
				t.Fatal("incorrect lifecycle behavior passed")
			}
			summary, _ := readObservationSummary(t, config)
			if !containsCheck(summary.Checks, failedCheck, "fail") || !containsCheck(summary.Checks, "lifecycle/cleanup", "pass") || fixture.state != "" || fixture.volumes || len(fixture.probes) != 0 {
				t.Fatalf("summary=%+v state=%s volumes=%t probes=%v", summary, fixture.state, fixture.volumes, fixture.probes)
			}
		})
	}
}

func TestLiveRunRetriesCleanupWhenSuccessfulRemovalLeavesAVolume(t *testing.T) {
	fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
	config := liveObservationConfig(t, "linux", fixture)
	removals := 0
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		status, err := fixture.run(ctx, request)
		if request.Name != "podman" && request.Name != "sh" && request.Name != "git" && request.Name != "go" && request.Args[0] == "remove" && containsValue(request.Args, "--volumes") {
			removals++
			if removals == 1 {
				fixture.volumes = true
			}
		}
		return status, err
	}
	if err := Run(context.Background(), config); err == nil {
		t.Fatal("incomplete removal passed")
	}
	summary, _ := readObservationSummary(t, config)
	if removals != 2 || fixture.volumes || !containsCheck(summary.Checks, "lifecycle/group-clean", "fail") || !containsCheck(summary.Checks, "lifecycle/cleanup", "pass") {
		t.Fatalf("removals=%d volumes=%t summary=%+v", removals, fixture.volumes, summary)
	}
}
