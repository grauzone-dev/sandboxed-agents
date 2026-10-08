package livesuite_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestLiveLifecycleRecordsAnUpFailureWithoutBuildingSharedImages(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("CONTAINER_CONNECTION", "")
	config := livesuite.Config{OptIn: true, Lifecycle: true, Commit: commit, Repository: t.TempDir(), OutputDirectory: t.TempDir(), Host: platform.Host{OS: "linux", Architecture: "amd64"}, Stdout: io.Discard, Stderr: io.Discard}
	up := false
	config.Run = func(_ context.Context, request process.Request) (int, error) {
		switch request.Name {
		case "git", "go":
			return 0, nil
		case "podman":
			if request.Args[0] == "info" {
				fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
			} else {
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		switch request.Args[0] {
		case "version":
			fmt.Fprintf(request.Streams.Stdout, "sandboxed-agents %s\nassets %s\n", commit, strings.Repeat("a", 64))
		case "list":
			fmt.Fprintln(request.Streams.Stdout, "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES")
		case "up":
			up = true
			return 42, nil
		case "build":
			t.Fatal("lifecycle rebuilt shared images")
		}
		return 0, nil
	}
	err := livesuite.Run(context.Background(), config)
	if err == nil || !up {
		t.Fatalf("up attempted=%t error=%v", up, err)
	}
	summary := readSummary(t, config, "linux")
	found := false
	for _, check := range summary.Checks {
		if check.Name == "lifecycle/up" && check.Result == "fail" {
			found = true
		}
	}
	if summary.Result != "fail" || !found {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestLiveLifecycleRefusesAnOccupiedControllerGroupBeforeUp(t *testing.T) {
	for _, object := range []string{"container", "volume", "foreign-prefix"} {
		t.Run(object, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
			config := lifecycleConfig(t)
			config.Run = func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					switch request.Args[0] {
					case "info":
						fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
					case "ps":
						if object == "container" {
							fmt.Fprintln(request.Streams.Stdout, `[{"Names":["sandboxed-agents.live.existing"],"Labels":{"io.github.sandboxed-agents.owner":"live"}}]`)
						} else if object == "foreign-prefix" {
							fmt.Fprintln(request.Streams.Stdout, `[{"Names":["sandboxed-agents.live.foreign"],"Labels":{}}]`)
						} else {
							fmt.Fprintln(request.Streams.Stdout, "[]")
						}
					case "volume":
						fmt.Fprintln(request.Streams.Stdout, `[{"Name":"sandboxed-agents.live.existing.workspace","Labels":{"io.github.sandboxed-agents.owner":"live"}}]`)
					}
					return 0, nil
				}
				if request.Name != "git" && request.Name != "go" && request.Args[0] == "up" {
					t.Fatal("occupied group reached up")
				}
				return harnessReply(request)
			}
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("occupied group passed")
			}
			summary := readSummary(t, config, "linux")
			if !hasCheck(summary, "lifecycle/group-empty", "fail") {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestLiveLifecycleFailsWhenUpDoesNotProduceARunningSandbox(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	config.Run = func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			if request.Args[0] == "info" {
				fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
			} else {
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		return harnessReply(request)
	}
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("missing sandbox passed the lifecycle run")
	}
	if summary := readSummary(t, config, "linux"); !hasCheck(summary, "lifecycle/created/list-running", "fail") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestLiveLifecycleDoesNotTreatMalformedInventoryAsAnEmptyGroup(t *testing.T) {
	for _, inventory := range []string{"null", "{}", "[{}]", `[{"Names":[""]}]`} {
		t.Run(inventory, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
			config := lifecycleConfig(t)
			config.Run = func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "podman" {
					if request.Args[0] == "info" {
						fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
					} else {
						fmt.Fprintln(request.Streams.Stdout, inventory)
					}
					return 0, nil
				}
				if request.Name != "git" && request.Name != "go" && request.Args[0] == "up" {
					t.Fatal("malformed inventory reached up")
				}
				return harnessReply(request)
			}
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("malformed inventory passed")
			}
		})
	}
}

func TestLiveLifecycleCleansAPartialUpAfterCancellation(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleaned := false
	name := ""
	config.Run = func(current context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			if request.Args[0] == "info" {
				fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
			} else if request.Args[0] == "volume" && name != "" && !cleaned {
				fmt.Fprintf(request.Streams.Stdout, `[{"Name":"sandboxed-agents.live.%s.workspace","Labels":{"io.github.sandboxed-agents.owner":"live"}}]`, name)
			} else {
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		if request.Name != "git" && request.Name != "go" {
			switch request.Args[0] {
			case "up":
				name = request.Args[1]
				cancel()
				return 1, current.Err()
			case "remove":
				deadline, bounded := current.Deadline()
				if current.Err() != nil || !bounded || time.Until(deadline) > 90*time.Second || strings.Join(request.Args, " ") != "remove "+name+" --volumes" {
					t.Fatalf("cleanup context=%v args=%v deadline=%v", current.Err(), request.Args, deadline)
				}
				cleaned = true
			}
		}
		return harnessReply(request)
	}
	if err := livesuite.Run(ctx, config); err == nil || !cleaned {
		t.Fatalf("error=%v cleanup=%t", err, cleaned)
	}
	if summary := readSummary(t, config, "linux"); summary.Result != "fail" || !hasCheck(summary, "lifecycle/cleanup", "pass") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestLiveLifecycleBindsWindowsProbesAndScrubsRemoteSettings(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	config.Host = platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
	for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
		t.Setenv(key, "unchecked-target")
	}
	boundQueries := 0
	config.Run = func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			for _, value := range request.Env {
				if strings.HasPrefix(value, "CONTAINER_HOST=") || strings.HasPrefix(value, "CONTAINER_CONNECTION=") || strings.HasPrefix(value, "CONTAINER_SSHKEY=") {
					t.Fatalf("unscrubbed query: %v", request.Args)
				}
			}
			if request.Args[0] == "machine" {
				if request.Args[1] == "list" {
					fmt.Fprintln(request.Streams.Stdout, `[{"Name":"chosen","Default":true,"Running":true,"VMType":"wsl"}]`)
				} else {
					fmt.Fprintln(request.Streams.Stdout, `[{"Name":"chosen","State":"running","Rootful":false}]`)
				}
			} else {
				if request.Args[0] != "--connection" || request.Args[1] != "chosen" {
					t.Fatalf("unbound query: %v", request.Args)
				}
				boundQueries++
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		if request.Name != "git" && request.Name != "go" && request.Args[0] == "up" {
			return 42, nil
		}
		return harnessReply(request)
	}
	if err := livesuite.Run(context.Background(), config); err == nil || boundQueries == 0 {
		t.Fatalf("error=%v bound queries=%d", err, boundQueries)
	}
}

func TestLiveLifecycleRefusesAChangedWindowsTargetBeforeUp(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	config.Host = platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
	selections := 0
	config.Run = func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			if request.Args[0] == "machine" {
				if request.Args[1] == "list" {
					selections++
					name := "chosen"
					if selections > 1 {
						name = "changed"
					}
					fmt.Fprintf(request.Streams.Stdout, `[{"Name":%q,"Default":true,"Running":true,"VMType":"wsl"}]`, name)
				} else {
					fmt.Fprintf(request.Streams.Stdout, `[{"Name":%q,"State":"running","Rootful":false}]`, request.Args[2])
				}
			} else {
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		if request.Name != "git" && request.Name != "go" && request.Args[0] == "up" {
			t.Fatal("changed Windows target reached up")
		}
		return harnessReply(request)
	}
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("changed Windows target passed")
	}
}

func lifecycleConfig(t *testing.T) livesuite.Config {
	t.Helper()
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("CONTAINER_CONNECTION", "")
	return livesuite.Config{OptIn: true, Lifecycle: true, Commit: commit, Repository: t.TempDir(), OutputDirectory: t.TempDir(), Host: platform.Host{OS: "linux", Architecture: "amd64"}, Stdout: io.Discard, Stderr: io.Discard}
}

func harnessReply(request process.Request) (int, error) {
	if request.Name == "git" || request.Name == "go" {
		return 0, nil
	}
	switch request.Args[0] {
	case "version":
		fmt.Fprintf(request.Streams.Stdout, "sandboxed-agents %s\nassets %s\n", commit, strings.Repeat("a", 64))
	case "list":
		fmt.Fprintln(request.Streams.Stdout, "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES")
	}
	return 0, nil
}

func hasCheck(summary livesuite.Summary, name, result string) bool {
	for _, check := range summary.Checks {
		if check.Name == name && check.Result == result {
			return true
		}
	}
	return false
}
