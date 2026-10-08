package livesuite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSelectedImageCoverageCreatesToolchainSandboxesBeforeSharedRebuild(t *testing.T) {
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	config.Lifecycle = false
	config.Images = true
	attempted := false
	builds := 0
	config.Run = func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			if len(request.Args) > 1 && request.Args[1] == "exists" {
				return 1, nil
			}
			if request.Args[0] == "info" {
				fmt.Fprintln(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
			} else {
				fmt.Fprintln(request.Streams.Stdout, "[]")
			}
			return 0, nil
		}
		if request.Name != "go" && request.Name != "git" {
			switch request.Args[0] {
			case "up":
				attempted = true
				return 42, nil
			case "build":
				builds++
			}
		}
		return harnessReply(request)
	}
	err := livesuite.Run(context.Background(), config)
	if !attempted || err == nil || builds != 0 {
		t.Fatalf("up attempted=%t builds=%d error=%v", attempted, builds, err)
	}
	summary := readSummary(t, config, "linux")
	if !hasCheck(summary, "images/dotnet/up", "fail") || summary.ImagePartRan || summary.ImageCoverageComplete {
		t.Fatalf("summary=%+v", summary)
	}
}

type imageSandboxFixture struct {
	kind, name, image string
}

type imageCoverageFixture struct {
	t               *testing.T
	sandboxes       map[string]imageSandboxFixture
	retainedVolumes map[string]bool
	builds          int
	shells          int
	removals        int
	rebuilt         bool
	windows         bool
	fail            string
	changedTarget   bool
	selections      int
	cancel          context.CancelFunc
	requests        []process.Request
	temporary       string
}

func newImageCoverageFixture(t *testing.T, host string) (livesuite.Config, *imageCoverageFixture) {
	t.Helper()
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	config := lifecycleConfig(t)
	config.Lifecycle = false
	config.Images = true
	fixture := &imageCoverageFixture{t: t, sandboxes: map[string]imageSandboxFixture{}, windows: host == "windows"}
	if fixture.windows {
		config.Host = platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
	}
	config.Run = fixture.run
	return config, fixture
}

func (fixture *imageCoverageFixture) run(ctx context.Context, request process.Request) (int, error) {
	fixture.requests = append(fixture.requests, request)
	if request.Name == "git" || request.Name == "go" {
		return 0, nil
	}
	if request.Name == "podman" {
		args := request.Args
		if fixture.windows {
			for _, value := range request.Env {
				if strings.HasPrefix(value, "CONTAINER_HOST=") && value != "CONTAINER_HOST=" || strings.HasPrefix(value, "CONTAINER_CONNECTION=") && value != "CONTAINER_CONNECTION=" || strings.HasPrefix(value, "CONTAINER_SSHKEY=") && value != "CONTAINER_SSHKEY=" {
					fixture.t.Fatalf("remote environment: %v", args)
				}
			}
			if args[0] == "machine" {
				target := "chosen"
				if args[1] == "list" {
					fixture.selections++
					if fixture.changedTarget && fixture.selections > 1 {
						target = "changed"
					}
					fmt.Fprintf(request.Streams.Stdout, `[{"Name":%q,"Default":true,"Running":true,"VMType":"wsl"}]`, target)
				} else {
					fmt.Fprintf(request.Streams.Stdout, `[{"Name":%q,"State":"running","Rootful":false}]`, args[2])
				}
				return 0, nil
			}
			if len(args) < 3 || args[0] != "--connection" || args[1] != "chosen" {
				fixture.t.Fatalf("unbound Podman: %v", args)
			}
			args = args[2:]
		}
		switch args[0] {
		case "info":
			fmt.Fprint(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
		case "ps":
			var records = []map[string]any{}
			for name := range fixture.sandboxes {
				records = append(records, map[string]any{"Names": []string{"sandboxed-agents.live." + name}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			if fixture.fail == "occupied" || fixture.fail == "foreign" {
				labels := map[string]string{}
				if fixture.fail == "occupied" {
					labels["io.github.sandboxed-agents.owner"] = "live"
				}
				records = append(records, map[string]any{"Names": []string{"sandboxed-agents.live.foreign"}, "Labels": labels})
			}
			json.NewEncoder(request.Streams.Stdout).Encode(records)
		case "volume":
			if args[1] == "exists" {
				for name := range fixture.sandboxes {
					if strings.HasPrefix(args[2], "sandboxed-agents.live."+name+".") {
						return 0, nil
					}
				}
				for name := range fixture.retainedVolumes {
					if strings.HasPrefix(args[2], "sandboxed-agents.live."+name+".") {
						return 0, nil
					}
				}
				return 1, nil
			}
			records := []map[string]any{}
			for name := range fixture.sandboxes {
				records = append(records, map[string]any{"Name": "sandboxed-agents.live." + name + ".ssh", "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			for name := range fixture.retainedVolumes {
				records = append(records, map[string]any{"Name": "sandboxed-agents.live." + name + ".ssh", "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			json.NewEncoder(request.Streams.Stdout).Encode(records)
		case "image":
			if args[1] != "inspect" {
				fixture.t.Fatalf("image mutation %v", args)
			}
			reference := args[2]
			if fixture.fail == "malformed-inspect" {
				fmt.Fprint(request.Streams.Stdout, `[{}]`)
				return 0, nil
			}
			kind := "base"
			for _, candidate := range []string{"dotnet", "playwright", "azure", "native"} {
				if strings.Contains(reference, candidate) {
					kind = candidate
				}
			}
			old := strings.HasPrefix(reference, "old-")
			if old && fixture.fail == "deleted-old" {
				return 1, nil
			}
			id := "old-" + kind
			layers := []string{"old-base-layer"}
			if fixture.rebuilt && !old {
				id = "new-" + kind
				layers = []string{"new-base-layer"}
			}
			if fixture.fail == "not-rebuilt" && kind == "azure" {
				id = "old-azure"
			}
			if kind != "base" {
				layers = append(layers, "toolchain-layer")
			}
			if (fixture.fail == "layers" || fixture.fail == "layers-after" && fixture.rebuilt && !old) && kind == "azure" {
				layers[0] = "unrelated-layer"
			}
			tag := "localhost/sandboxed-agents:base-" + strings.Repeat("a", 64)
			if kind != "base" {
				tag = "localhost/sandboxed-agents:toolchains-" + kind + "-" + strings.Repeat("a", 64)
			}
			tags := []string{tag}
			if old {
				tags = nil
			}
			if fixture.fail == "group-tag" {
				tags = append(tags, "localhost/sandboxed-agents:live-tag")
			}
			if fixture.fail == "missing-tag" {
				tags = []string{"other"}
			}
			json.NewEncoder(request.Streams.Stdout).Encode([]map[string]any{{"Id": id, "RepoTags": tags, "RootFS": map[string]any{"Layers": layers}}})
		case "container":
			name := strings.TrimPrefix(args[2], "sandboxed-agents.live.")
			item, ok := fixture.sandboxes[name]
			if args[1] == "exists" {
				if ok {
					return 0, nil
				}
				return 1, nil
			}
			if !ok {
				return 1, nil
			}
			running := fixture.fail != "stopped"
			imageName := "localhost/sandboxed-agents:base-" + strings.Repeat("a", 64)
			if item.kind != "base" {
				imageName = "old-" + item.kind
			}
			if fixture.fail == "container-group" {
				imageName = "localhost/sandboxed-agents:live-tag"
			}
			image := item.image
			if fixture.fail == "container-new-image" && fixture.rebuilt {
				image = "new-" + item.kind
			}
			mount := map[string]any{"Type": "volume", "Name": "sandboxed-agents.live." + name + ".ssh", "Destination": "/etc/ssh", "RW": true}
			if fixture.fail == "ssh-bind" {
				mount["Type"] = "bind"
			}
			json.NewEncoder(request.Streams.Stdout).Encode([]map[string]any{{"Name": args[2], "Image": image, "ImageName": imageName, "State": map[string]any{"Running": running}, "Config": map[string]any{"Labels": map[string]string{"io.github.sandboxed-agents.owner": "live", "io.github.sandboxed-agents.sandbox-name": name}}, "Mounts": []any{mount}}})
		default:
			fixture.t.Fatalf("unexpected Podman %v", args)
		}
		return 0, nil
	}
	for _, value := range request.Env {
		if strings.HasPrefix(value, "TMPDIR=") {
			fixture.temporary = strings.TrimPrefix(value, "TMPDIR=")
		}
		if strings.HasPrefix(value, "CONTAINER_HOST=") && value != "CONTAINER_HOST=" || strings.HasPrefix(value, "CONTAINER_CONNECTION=") && value != "CONTAINER_CONNECTION=" || strings.HasPrefix(value, "CONTAINER_SSHKEY=") && value != "CONTAINER_SSHKEY=" {
			fixture.t.Fatalf("CLI remote environment %v", request.Args)
		}
	}
	switch request.Args[0] {
	case "version":
		return harnessReply(request)
	case "list":
		fmt.Fprintln(request.Streams.Stdout, "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES")
		for name := range fixture.sandboxes {
			state := "running"
			if fixture.rebuilt && fixture.fail != "not-outdated" {
				state = "running (outdated)"
			}
			fmt.Fprintf(request.Streams.Stdout, "%s %s volume - none none home,ssh,workspace\n", name, state)
		}
	case "up":
		if fixture.builds != 0 {
			fixture.t.Fatal("up after build")
		}
		kind := "base"
		if len(request.Args) > 2 {
			if len(request.Args) != 4 || request.Args[2] != "--with" {
				fixture.t.Fatalf("up %v", request.Args)
			}
			kind = request.Args[3]
		}
		name := request.Args[1]
		fixture.sandboxes[name] = imageSandboxFixture{kind: kind, name: name, image: "old-" + kind}
		if fixture.fail == "up" {
			return 42, nil
		}
		if fixture.cancel != nil {
			fixture.cancel()
			return 1, ctx.Err()
		}
	case "shell":
		if len(request.Args) != 2 || request.Streams.Stdin == nil {
			fixture.t.Fatalf("shell seam: %v", request.Args)
		}
		script, err := io.ReadAll(request.Streams.Stdin)
		if err != nil {
			fixture.t.Fatal(err)
		}
		fixture.shells++
		if fixture.fail == "smoke" && fixture.shells == 1 || fixture.fail == "smoke-"+fixture.sandboxes[request.Args[1]].kind {
			return 42, nil
		}
		if fixture.fail == "base-contents" && strings.Contains(request.Args[1], "images-base-") {
			return 42, nil
		}
		if strings.Contains(string(script), "ssh_host_") {
			if fixture.fail == "host-outside" {
				return 42, nil
			}
			material := "ssh-ed25519 public-" + request.Args[1]
			if fixture.fail == "shared-keys" {
				material = "ssh-ed25519 shared"
			}
			if fixture.fail == "keys-empty" {
				fmt.Fprint(request.Streams.Stdout, `{}`)
			} else {
				json.NewEncoder(request.Streams.Stdout).Encode(map[string]string{"ed25519": material})
			}
		}
	case "build":
		if len(request.Args) != 1 {
			fixture.t.Fatalf("build %v", request.Args)
		}
		if len(fixture.sandboxes) != 5 {
			fixture.t.Fatalf("build before 5 sandboxes: %d", len(fixture.sandboxes))
		}
		fixture.builds++
		fixture.rebuilt = true
		if fixture.fail == "build" {
			return 42, nil
		}
		if fixture.fail == "retained-context" {
			if err := os.Mkdir(filepath.Join(fixture.temporary, "sandboxed-agents-context-leaked"), 0700); err != nil {
				fixture.t.Fatal(err)
			}
		}
		if fixture.fail == "misleading-note" {
			fmt.Fprintln(request.Streams.Stdout, "Existing sandboxes change their image immediately. Do not update. The list is never outdated.")
		} else if fixture.fail != "build-note" {
			fmt.Fprintln(request.Streams.Stdout, "Existing sandboxes keep their current image until you update them; list marks them as outdated.")
		}
	case "remove":
		if len(request.Args) != 3 || request.Args[2] != "--volumes" {
			fixture.t.Fatalf("remove %v", request.Args)
		}
		if ctx.Err() != nil {
			fixture.t.Fatalf("cancelled cleanup %v", ctx.Err())
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 90*time.Second {
			fixture.t.Fatalf("cleanup deadline %v", deadline)
		}
		fixture.removals++
		if fixture.fail == "retained-volume" {
			if fixture.retainedVolumes == nil {
				fixture.retainedVolumes = map[string]bool{}
			}
			fixture.retainedVolumes[request.Args[1]] = true
		}
		if fixture.fail != "incomplete-removal" {
			delete(fixture.sandboxes, request.Args[1])
		}
	default:
		fixture.t.Fatalf("unexpected CLI %v", request.Args)
	}
	return 0, nil
}

func TestSelectedImageCoverageVerifiesAllToolchainsAndRetainsOldImages(t *testing.T) {
	for _, host := range []string{"linux", "windows"} {
		t.Run(host, func(t *testing.T) {
			config, fixture := newImageCoverageFixture(t, host)
			if host == "windows" {
				for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
					t.Setenv(key, "unchecked-target")
				}
			}
			if err := livesuite.Run(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			platformName := host
			if host == "windows" {
				platformName = "windows-11"
			}
			summary := readSummary(t, config, platformName)
			if summary.Result != "pass" || !summary.ImagePartRan || !summary.ImageCoverageComplete || fixture.builds != 1 || fixture.removals != 5 || len(fixture.sandboxes) != 0 || fixture.shells != 7 {
				t.Fatalf("summary=%+v builds=%d removed=%d shells=%d", summary, fixture.builds, fixture.removals, fixture.shells)
			}
			for _, kind := range []string{"dotnet", "playwright", "azure", "native"} {
				for _, phase := range []string{"up", "smoke", "rebuild"} {
					if !hasCheck(summary, "images/"+kind+"/"+phase, "pass") {
						t.Fatalf("missing %s %s", kind, phase)
					}
				}
			}
			for _, phase := range []string{"up", "contents", "host-keys", "rebuild"} {
				if !hasCheck(summary, "images/base/"+phase, "pass") {
					t.Fatalf("missing base %s", phase)
				}
			}
			if _, err := os.Stat(fixture.temporary); !os.IsNotExist(err) {
				t.Fatalf("temporary directory retained: %v", err)
			}
		})
	}
}

func TestSelectedImageCoverageRequiresBuildToDescribeRetainedSandboxImages(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	fixture.fail = "misleading-note"
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("misleading image lifecycle note passed")
	}
	summary := readSummary(t, config, "linux")
	if !summary.ImagePartRan || summary.ImageCoverageComplete || !hasCheck(summary, "images/build", "fail") || fixture.removals != 5 {
		t.Fatalf("summary=%+v removals=%d", summary, fixture.removals)
	}
}

func TestSelectedImageCoverageRejectsIncorrectLiveObservationsAndCleansCreatedSandboxes(t *testing.T) {
	cases := []struct {
		failure, check string
		buildSucceeded bool
	}{
		{"up", "images/dotnet/up", false},
		{"smoke-dotnet", "images/dotnet/smoke", false},
		{"smoke-playwright", "images/playwright/smoke", false},
		{"smoke-azure", "images/azure/smoke", false},
		{"smoke-native", "images/native/smoke", false},
		{"malformed-inspect", "images/dotnet/up", false},
		{"missing-tag", "images/dotnet/up", false},
		{"group-tag", "images/dotnet/up", false},
		{"stopped", "images/dotnet/up", false},
		{"container-group", "images/dotnet/up", false},
		{"ssh-bind", "images/dotnet/up", false},
		{"base-contents", "images/base/contents", false},
		{"host-outside", "images/base/host-keys", false},
		{"shared-keys", "images/base/host-keys", false},
		{"keys-empty", "images/base/host-keys", false},
		{"layers", "images/layers-before", false},
		{"build", "images/build", false},
		{"build-note", "images/build", true},
		{"not-rebuilt", "images/azure/rebuild", true},
		{"deleted-old", "images/dotnet/rebuild", true},
		{"container-new-image", "images/dotnet/rebuild", true},
		{"layers-after", "images/layers-after", true},
		{"not-outdated", "images/outdated", true},
		{"retained-context", "images/temporary-contexts", true},
	}
	for _, test := range cases {
		t.Run(test.failure, func(t *testing.T) {
			config, fixture := newImageCoverageFixture(t, "linux")
			fixture.fail = test.failure
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("incorrect live observation passed")
			}
			summary := readSummary(t, config, "linux")
			if summary.Result != "fail" || summary.ImagePartRan != test.buildSucceeded || summary.ImageCoverageComplete || !hasCheck(summary, test.check, "fail") || !hasCheck(summary, "images/cleanup", "pass") || len(fixture.sandboxes) != 0 {
				t.Fatalf("summary=%+v sandboxes=%v", summary, fixture.sandboxes)
			}
			if !test.buildSucceeded && test.failure != "build" && fixture.builds != 0 {
				t.Fatalf("failure before rebuild reached build: %d", fixture.builds)
			}
			if fixture.builds > 1 {
				t.Fatalf("more than one build: %d", fixture.builds)
			}
		})
	}
}

func TestSelectedImageCoverageRefusesOccupiedAndForeignControllerGroups(t *testing.T) {
	for _, failure := range []string{"occupied", "foreign"} {
		t.Run(failure, func(t *testing.T) {
			config, fixture := newImageCoverageFixture(t, "linux")
			fixture.fail = failure
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("occupied group passed")
			}
			summary := readSummary(t, config, "linux")
			if !hasCheck(summary, "images/group-empty", "fail") || fixture.builds != 0 || len(fixture.sandboxes) != 0 || fixture.removals != 0 {
				t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
			}
		})
	}
}

func TestSelectedImageCoverageCleansPartialUpAfterCancellation(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.cancel = cancel
	if err := livesuite.Run(ctx, config); err == nil {
		t.Fatal("cancelled up passed")
	}
	summary := readSummary(t, config, "linux")
	if fixture.removals != 1 || len(fixture.sandboxes) != 0 || !hasCheck(summary, "images/cleanup", "pass") || !hasCheck(summary, "images/dotnet/up", "fail") || summary.ImagePartRan || summary.ImageCoverageComplete {
		t.Fatalf("summary=%+v removed=%d", summary, fixture.removals)
	}
}

func TestSelectedImageCoverageFailsWhenRemovalLeavesGroupObjects(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	fixture.fail = "incomplete-removal"
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("retained group objects passed")
	}
	summary := readSummary(t, config, "linux")
	if summary.Result != "fail" || summary.ImageCoverageComplete || !hasCheck(summary, "images/cleanup", "fail") || fixture.removals != 5 || len(fixture.sandboxes) != 5 {
		t.Fatalf("summary=%+v removed=%d", summary, fixture.removals)
	}
}

func TestSelectedImageCoverageRefusesChangedWindowsTarget(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "windows")
	fixture.changedTarget = true
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("changed target passed")
	}
	summary := readSummary(t, config, "windows-11")
	if fixture.builds != 0 || len(fixture.sandboxes) != 0 || !hasCheck(summary, "images/dotnet/up", "fail") {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestSelectedImageCoverageAllowsControllerGroupsThatOccurInCanonicalImageNames(t *testing.T) {
	for _, group := range []string{"base", "agents", "azure", "a"} {
		t.Run(group, func(t *testing.T) {
			config, fixture := newImageCoverageFixture(t, "linux")
			t.Setenv("SANDBOXED_AGENTS_GROUP", group)
			config.Run = func(ctx context.Context, request process.Request) (int, error) {
				if request.Name != "podman" {
					return fixture.run(ctx, request)
				}
				args := append([]string(nil), request.Args...)
				for i, arg := range args {
					args[i] = strings.ReplaceAll(arg, "sandboxed-agents."+group+".", "sandboxed-agents.live.")
				}
				request.Args = args
				var output bytes.Buffer
				original := request.Streams.Stdout
				if original != nil {
					request.Streams.Stdout = &output
				}
				status, err := fixture.run(ctx, request)
				if original != nil {
					data := strings.ReplaceAll(output.String(), "sandboxed-agents.live.", "sandboxed-agents."+group+".")
					data = strings.ReplaceAll(data, `"io.github.sandboxed-agents.owner":"live"`, `"io.github.sandboxed-agents.owner":"`+group+`"`)
					fmt.Fprint(original, data)
				}
				return status, err
			}
			if err := livesuite.Run(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			if summary := readSummary(t, config, "linux"); !summary.ImageCoverageComplete {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestSelectedImageCoverageSummariesDistinguishUnselectedAndUnfinishedChecks(t *testing.T) {
	for _, selection := range []string{"unselected", "stopped"} {
		t.Run(selection, func(t *testing.T) {
			config, fixture := newImageCoverageFixture(t, "linux")
			if selection == "unselected" {
				config.Images = false
			} else {
				fixture.fail = "smoke-playwright"
			}
			err := livesuite.Run(context.Background(), config)
			if (err == nil) != (selection == "unselected") {
				t.Fatalf("error=%v", err)
			}
			summary := readSummary(t, config, "linux")
			if summary.SchemaVersion != 2 || summary.ImagePartRan || summary.ImageCoverageComplete || summary.ImagePartSelected != (selection != "unselected") {
				t.Fatalf("summary=%+v", summary)
			}
			seen := map[string]bool{}
			for _, check := range summary.Checks {
				if seen[check.Name] {
					t.Fatalf("duplicate check %s", check.Name)
				}
				seen[check.Name] = true
			}
			for _, kind := range []string{"dotnet", "playwright", "azure", "native"} {
				for _, phase := range []string{"up", "smoke", "rebuild"} {
					expected := "not-run"
					if selection == "stopped" {
						if kind == "dotnet" && phase != "rebuild" || kind == "playwright" && phase == "up" {
							expected = "pass"
						}
						if kind == "playwright" && phase == "smoke" {
							expected = "fail"
						}
					}
					if !hasCheck(summary, "images/"+kind+"/"+phase, expected) {
						t.Fatalf("missing %s %s=%s", kind, phase, expected)
					}
				}
			}
			for _, phase := range []string{"up", "contents", "host-keys", "rebuild"} {
				if !hasCheck(summary, "images/base/"+phase, "not-run") {
					t.Fatalf("missing base %s not-run", phase)
				}
			}
			if fixture.builds != 0 {
				t.Fatalf("unselected/unfinished image suite rebuilt %d times", fixture.builds)
			}
		})
	}
}

func TestSelectedImageCoverageSummaryOmitsPrivateProcessDiagnostics(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	var diagnostics bytes.Buffer
	config.Stderr = &diagnostics
	secrets := []string{"private-user", "private-host", "/home/private-user/workspace", "C:\\Users\\private-user", "ghp_secret", "PRIVATE KEY", "ssh-agent01.live", "SHA256:secret", "http://private.example:7777"}
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name != "podman" && request.Name != "git" && request.Name != "go" && request.Args[0] == "shell" {
			fmt.Fprintln(request.Streams.Stderr, strings.Join(secrets, "\n"))
			return 42, nil
		}
		return fixture.run(ctx, request)
	}
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("smoke failure passed")
	}
	data, err := os.ReadFile(filepath.Join(config.OutputDirectory, "live-suite-linux.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if !strings.Contains(diagnostics.String(), secret) || bytes.Contains(data, []byte(secret)) {
			t.Fatalf("diagnostic/summary redaction for %q: %s", secret, data)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 9 {
		t.Fatalf("summary fields=%d", len(fields))
	}
}

func TestSelectedImageCoverageUsesFreshSandboxNamesForEachRun(t *testing.T) {
	prior := map[string]bool{}
	for iteration := 0; iteration < 2; iteration++ {
		config, fixture := newImageCoverageFixture(t, "linux")
		if err := livesuite.Run(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		for _, request := range fixture.requests {
			if request.Name == "podman" || request.Name == "git" || request.Name == "go" || request.Args[0] != "up" {
				continue
			}
			name := request.Args[1]
			if prior[name] {
				t.Fatalf("sandbox name reused across runs: %s", name)
			}
			prior[name] = true
		}
	}
	if len(prior) != 10 {
		t.Fatalf("unique names=%d", len(prior))
	}
}

func TestSelectedImageCoverageScrubsRemoteSettingsFromDirectLinuxProbes(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	probes := 0
	for _, request := range fixture.requests {
		if request.Name != "podman" {
			continue
		}
		probes++
		if request.Env == nil {
			t.Fatalf("Podman probe inherits unchecked host environment: %v", request.Args)
		}
		for _, value := range request.Env {
			key, _, _ := strings.Cut(value, "=")
			if key == "CONTAINER_HOST" || key == "CONTAINER_CONNECTION" || key == "CONTAINER_SSHKEY" {
				t.Fatalf("Podman probe carries remote setting %s", key)
			}
		}
	}
	if probes == 0 {
		t.Fatal("no Podman probes")
	}
}

func TestSelectedImageCoverageFailsWhenRemovalLeavesOnlyGroupVolumes(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	fixture.fail = "retained-volume"
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("retained group volume passed")
	}
	summary := readSummary(t, config, "linux")
	if len(fixture.sandboxes) != 0 || len(fixture.retainedVolumes) != 5 || !hasCheck(summary, "images/cleanup", "fail") || summary.ImageCoverageComplete {
		t.Fatalf("summary=%+v containers=%v volumes=%v", summary, fixture.sandboxes, fixture.retainedVolumes)
	}
}
