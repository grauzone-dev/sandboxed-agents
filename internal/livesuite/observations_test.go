package livesuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func TestSandboxObservationsRefuseHostBinds(t *testing.T) {
	run := func(_ context.Context, request process.Request) (int, error) {
		fmt.Fprint(request.Streams.Stdout, `[{"Name":"sandboxed-agents.live.sample","Image":"image-id","State":{"Running":true,"Pid":4321},"Mounts":[{"Type":"bind","Source":"/private","Destination":"/workspace"}]}]`)
		return 0, nil
	}
	observe := newSandboxObserver(context.Background(), Config{Host: platform.Host{OS: "linux"}}, run, "live", "sample", "")
	if err := observe(func(_ string, action func() error) error { return action() }, "created"); err == nil {
		t.Fatal("host bind passed the isolation observation")
	}
}

func TestGatewayProbesNeverAppearAsSandboxes(t *testing.T) {
	fixture := newObservationFixture()
	if err := observeGateway(context.Background(), Config{}, fixture.run, "live", "sample", "sha256:fixture", "sandboxed-agents.live.sample", "10.0.2.2"); err != nil {
		t.Fatal(err)
	}
	type probeRecord struct {
		Names  []string
		Labels map[string]string
	}
	probes := []probeRecord{}
	for _, request := range fixture.requests {
		if request.Args[0] != "run" {
			continue
		}
		record := probeRecord{Labels: map[string]string{}}
		for index, arg := range request.Args {
			if arg == "--name" {
				record.Names = []string{request.Args[index+1]}
			}
			if arg == "--label" {
				key, value, _ := strings.Cut(request.Args[index+1], "=")
				record.Labels[key] = value
			}
		}
		probes = append(probes, record)
	}
	if len(probes) != 2 {
		t.Fatalf("probe creations=%d", len(probes))
	}
	run := func(_ context.Context, request process.Request) (int, error) {
		write := func(value any) (int, error) { return 0, json.NewEncoder(request.Streams.Stdout).Encode(value) }
		switch request.Args[0] {
		case "ps":
			return write(probes)
		case "volume":
			return write([]any{})
		case "container":
			for _, record := range probes {
				if record.Names[0] == request.Args[2] {
					return write([]any{map[string]any{"Name": record.Names[0], "Image": "sha256:fixture", "Config": map[string]any{"Labels": record.Labels}, "State": map[string]any{"Running": false}}})
				}
			}
		case "image":
			return 1, nil
		}
		return 99, fmt.Errorf("unexpected list query %v", request.Args)
	}
	var output bytes.Buffer
	if err := sandbox.List(context.Background(), "live", strings.Repeat("a", 64), run, &output, agentcatalog.Embedded()); err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(strings.TrimSpace(output.String()), "\n")) != 1 {
		t.Fatalf("leaked gateway probes appeared as sandboxes:\n%s", output.String())
	}
}

func TestGatewayProbesUseAnIndependentOwnershipNamespace(t *testing.T) {
	fixture := newObservationFixture()
	if err := observeGateway(context.Background(), Config{}, fixture.run, "live", "sample", "sha256:fixture", "sandboxed-agents.live.sample", "10.0.2.2"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, request := range fixture.requests {
		if request.Args[0] != "run" {
			continue
		}
		labels := map[string]string{}
		for index, arg := range request.Args {
			if arg == "--name" {
				names = append(names, request.Args[index+1])
			}
			if arg == "--label" {
				key, value, _ := strings.Cut(request.Args[index+1], "=")
				labels[key] = value
			}
		}
		wantLabels := map[string]string{"io.github.sandboxed-agents.live-suite-group": "live", "io.github.sandboxed-agents.live-suite-sandbox-name": "sample"}
		if !reflect.DeepEqual(labels, wantLabels) {
			t.Fatalf("probe ownership labels=%v", labels)
		}
	}
	if !reflect.DeepEqual(names, []string{"sandboxed-agents-live-probe.live.sample-gateway-host", "sandboxed-agents-live-probe.live.sample-gateway-control"}) {
		t.Fatalf("probe names=%v", names)
	}
}

func TestSandboxObservationsRequireKernelFacts(t *testing.T) {
	run := func(_ context.Context, request process.Request) (int, error) {
		if len(request.Args) > 1 && request.Args[0] == "container" && request.Args[1] == "inspect" {
			fmt.Fprint(request.Streams.Stdout, validObservationContainer)
		} else {
			fmt.Fprint(request.Streams.Stdout, "not-kernel-facts")
		}
		return 0, nil
	}
	var failed []string
	observe := newSandboxObserver(context.Background(), Config{Host: platform.Host{OS: "linux"}}, run, "live", "sample", "")
	err := observe(func(name string, action func() error) error {
		err := action()
		if err != nil {
			failed = append(failed, name)
		}
		return err
	}, "created")
	if err == nil || len(failed) == 0 || !strings.HasPrefix(failed[0], "created/") {
		t.Fatalf("missing kernel facts passed: error=%v failed=%v", err, failed)
	}
}

const validObservationContainer = `[{"Name":"sandboxed-agents.live.sample","Image":"sha256:fixture","Config":{"Labels":{"io.github.sandboxed-agents.owner":"live","io.github.sandboxed-agents.sandbox-name":"sample"}},"State":{"Running":true,"Pid":4321},"Mounts":[{"Type":"volume","Name":"sandboxed-agents.live.sample.workspace","Source":"/volumes/workspace","Destination":"/workspace","RW":true},{"Type":"volume","Name":"sandboxed-agents.live.sample.home","Source":"/volumes/home","Destination":"/home/agent","RW":true},{"Type":"volume","Name":"sandboxed-agents.live.sample.ssh","Source":"/volumes/ssh","Destination":"/etc/ssh","RW":true}]}]`

func TestSandboxObservationsRequireActualContainerOwnershipLabels(t *testing.T) {
	for _, item := range []struct{ name, label, value string }{
		{"wrong-owner", "io.github.sandboxed-agents.owner", "foreign"},
		{"missing-owner", "io.github.sandboxed-agents.owner", ""},
		{"wrong-sandbox-name", "io.github.sandboxed-agents.sandbox-name", "other"},
		{"missing-sandbox-name", "io.github.sandboxed-agents.sandbox-name", ""},
	} {
		t.Run(item.name, func(t *testing.T) {
			fixture := newObservationFixture()
			var records []map[string]any
			if err := json.Unmarshal([]byte(fixture.container), &records); err != nil {
				t.Fatal(err)
			}
			labels := records[0]["Config"].(map[string]any)["Labels"].(map[string]any)
			if item.value == "" {
				delete(labels, item.label)
			} else {
				labels[item.label] = item.value
			}
			data, err := json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			fixture.container = string(data)
			var checks []Check
			if err := fixtureObserver(fixture)(recordingCheck(&checks), "created"); err == nil {
				t.Fatal("incorrect container ownership labels passed")
			}
			if !containsCheck(checks, "created/mounts-three-named-volumes-no-binds-or-sockets", "fail") {
				t.Fatal(checks)
			}
		})
	}
}

const validHostObservation = `host-uid 1000
host-gid 1000
uid-map 0 100000 1000
uid-map 1000 1000 1
uid-map 1001 101000 64535
gid-map 0 200000 1000
gid-map 1000 1000 1
gid-map 1001 201000 64535
start-host-uid 100000
start-host-gid 200000
sub-uid 100000 65536
sub-gid 200000 65536
`

type observationFixture struct {
	container   string
	host        string
	kernel      map[string]any
	agent       map[string]any
	gateway     string
	failClient  int
	failCleanup bool
	probeOwner  string
	clientCalls int
	requests    []process.Request
	sandboxName string
}

func observationIdentity(id uint64) map[string]any {
	identity := map[string]any{"uid": []uint64{id, id, id, id}, "gid": []uint64{id, id, id, id}, "no_new_privileges": 1}
	if id == 1000 {
		identity["name"] = "agent"
		identity["user_namespace"] = "user:[4026532991]"
	}
	return identity
}

func newObservationFixture() *observationFixture {
	return &observationFixture{container: validObservationContainer, host: validHostObservation, kernel: map[string]any{
		"start": observationIdentity(0), "manager": observationIdentity(0), "memory": "268435456", "cpu": "100000 100000", "pids": "128", "shm": 16777216, "gateway": "10.0.2.2", "user_namespace": "user:[4026532991]",
	}, agent: observationIdentity(1000), sandboxName: "sample"}
}

func (fixture *observationFixture) run(_ context.Context, request process.Request) (int, error) {
	fixture.requests = append(fixture.requests, request)
	write := func(value any) (int, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return 1, err
		}
		_, err = request.Streams.Stdout.Write(data)
		return 0, err
	}
	if request.Name == "sh" {
		fmt.Fprint(request.Streams.Stdout, fixture.host)
		return 0, nil
	}
	args := request.Args
	if len(args) > 2 && args[0] == "container" && args[1] == "exists" {
		return 0, nil
	}
	if len(args) > 2 && args[0] == "container" && args[1] == "inspect" && strings.Contains(args[2], "-gateway-") {
		owner := fixture.probeOwner
		if owner == "" {
			owner = "live"
		}
		return write([]any{map[string]any{"Name": args[2], "Config": map[string]any{"Labels": map[string]string{"io.github.sandboxed-agents.live-suite-group": owner, "io.github.sandboxed-agents.live-suite-sandbox-name": fixture.sandboxName}}}})
	}
	if len(args) > 1 && args[0] == "container" && args[1] == "inspect" {
		fmt.Fprint(request.Streams.Stdout, fixture.container)
		return 0, nil
	}
	if len(args) > 2 && args[0] == "machine" && args[1] == "ssh" {
		fmt.Fprint(request.Streams.Stdout, fixture.host)
		return 0, nil
	}
	if len(args) > 0 && args[0] == "run" {
		fmt.Fprint(request.Streams.Stdout, "fixture-container-id")
		return 0, nil
	}
	if len(args) > 0 && args[0] == "rm" {
		if fixture.failCleanup {
			return 42, nil
		}
		return 0, nil
	}
	if len(args) > 0 && args[0] == "stop" {
		return 0, nil
	}
	if len(args) > 0 && args[0] == "exec" {
		for _, arg := range args {
			switch arg {
			case kernelObservationScript:
				return write(fixture.kernel)
			case agentObservationScript:
				return write(fixture.agent)
			case gatewayClientScript:
				fixture.clientCalls++
				if fixture.clientCalls == fixture.failClient {
					fmt.Fprint(request.Streams.Stdout, "fail")
					return 1, nil
				}
				fmt.Fprint(request.Streams.Stdout, "pass")
				return 0, nil
			}
		}
		if strings.HasSuffix(args[2], "-gateway-host") {
			fmt.Fprint(request.Streams.Stdout, "37119")
			return 0, nil
		}
	}
	return 99, fmt.Errorf("unexpected process %s %v", request.Name, request.Args)
}

type liveObservationFixture struct {
	observations *observationFixture
	name, state  string
	volumes      bool
	probes       map[string]bool
	phase        int
	mutate       func(*observationFixture, int)
	requests     []process.Request
}

const observationCommit = "0123456789abcdef0123456789abcdef01234567"
const observationListHeader = "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES\n"

func (f *liveObservationFixture) run(ctx context.Context, request process.Request) (int, error) {
	f.requests = append(f.requests, request)
	if request.Name == "git" || request.Name == "go" {
		return 0, nil
	}
	if request.Name != "podman" && request.Name != "sh" {
		switch request.Args[0] {
		case "version":
			fmt.Fprintf(request.Streams.Stdout, "sandboxed-agents %s\nassets %s\n", observationCommit, strings.Repeat("a", 64))
		case "list":
			fmt.Fprint(request.Streams.Stdout, observationListHeader)
			if f.state != "" && !containsValue(request.Env, "SANDBOXED_AGENTS_GROUP=default") {
				fmt.Fprintf(request.Streams.Stdout, "%s %s volume no - none none 3\n", f.name, f.state)
			}
		case "up":
			if f.name == "" {
				f.name = request.Args[1]
				f.observations.sandboxName = f.name
				f.observations.container = strings.ReplaceAll(f.observations.container, "sample", f.name)
			}
			f.state, f.volumes = "running", true
		case "stop":
			f.state = "stopped"
		case "start":
			f.state = "running"
		case "remove":
			f.state = "volumes only"
			if containsValue(request.Args, "--volumes") {
				f.state, f.volumes = "", false
			}
		case "build":
			return 99, fmt.Errorf("unexpected shared-image-build")
		default:
			return 99, fmt.Errorf("unexpected executable invocation %v", request.Args)
		}
		return 0, nil
	}
	args := request.Args
	if len(args) > 1 && args[0] == "--connection" {
		request.Args = args[2:]
		args = request.Args
	}
	write := func(value any) (int, error) { return 0, json.NewEncoder(request.Streams.Stdout).Encode(value) }
	if request.Name == "podman" {
		switch args[0] {
		case "info":
			fmt.Fprint(request.Streams.Stdout, `{"host":{"serviceIsRemote":false}}`)
			return 0, nil
		case "ps":
			items := []any{}
			for name := range f.probes {
				items = append(items, map[string]any{"Names": []string{name}, "Labels": map[string]string{"io.github.sandboxed-agents.live-suite-group": "live"}})
			}
			if f.state == "running" || f.state == "stopped" {
				items = append(items, map[string]any{"Names": []string{"sandboxed-agents.live." + f.name}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			return write(items)
		case "volume":
			if args[1] == "ls" {
				items := []any{}
				if f.volumes {
					items = append(items, map[string]any{"Name": "sandboxed-agents.live." + f.name + ".home", "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
				}
				return write(items)
			}
			items := []any{}
			for _, name := range args[2:] {
				suffix := name[strings.LastIndex(name, ".")+1:]
				items = append(items, map[string]any{"Name": name, "CreatedAt": "2026-10-08T00:00:00Z", "Mountpoint": "/private-fixture/volumes/" + suffix, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			return write(items)
		case "machine":
			if args[1] == "list" {
				fmt.Fprint(request.Streams.Stdout, `[{"Name":"selected-machine","Default":true,"Running":true,"VMType":"wsl"}]`)
				return 0, nil
			}
			if args[1] == "inspect" {
				fmt.Fprint(request.Streams.Stdout, `[{"Name":"selected-machine","State":"running","Rootful":false}]`)
				return 0, nil
			}
		case "run":
			for i, arg := range args {
				if arg == "--name" {
					f.probes[args[i+1]] = true
				}
			}
		case "rm":
			delete(f.probes, args[1])
		}
		if containsValue(args, kernelObservationScript) {
			f.phase++
			if f.mutate != nil {
				f.mutate(f.observations, f.phase)
			}
		}
	}
	return f.observations.run(ctx, request)
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func liveObservationConfig(t *testing.T, hostOS string, fixture *liveObservationFixture) Config {
	t.Helper()
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	t.Setenv("CONTAINER_HOST", "")
	t.Setenv("CONTAINER_CONNECTION", "")
	return Config{OptIn: true, Lifecycle: true, Commit: observationCommit, Repository: t.TempDir(), OutputDirectory: t.TempDir(), Host: platform.Host{OS: hostOS, Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}, Run: fixture.run, Stdout: io.Discard, Stderr: io.Discard}
}

func readObservationSummary(t *testing.T, config Config) (Summary, []byte) {
	t.Helper()
	platformName := config.Host.OS
	if platformName == "windows" {
		platformName = "windows-11"
	}
	data, err := os.ReadFile(filepath.Join(config.OutputDirectory, "live-suite-"+platformName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	return summary, data
}

func TestLiveRunRecordsObservedIsolationAcrossTheLifecycle(t *testing.T) {
	for _, hostOS := range []string{"linux", "windows"} {
		t.Run(hostOS, func(t *testing.T) {
			fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
			config := liveObservationConfig(t, hostOS, fixture)
			if hostOS == "windows" {
				t.Setenv("CONTAINER_HOST", "private-remote-target")
				t.Setenv("CONTAINER_CONNECTION", "private-connection")
				t.Setenv("CONTAINER_SSHKEY", "private-key-path")
			}
			if err := Run(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			summary, data := readObservationSummary(t, config)
			if summary.Result != "pass" || fixture.phase != 3 || fixture.state != "" || fixture.volumes || len(fixture.probes) != 0 {
				t.Fatalf("summary=%+v phase=%d state=%s volumes=%t probes=%v", summary, fixture.phase, fixture.state, fixture.volumes, fixture.probes)
			}
			for _, phase := range []string{"created", "started-again", "adopted"} {
				for _, name := range []string{"root-maps-to-subordinate-ids", "agent-uid-1000-gid-1000-maps-to-host-user", "start-uid-0-gid-0", "manager-exec-uid-0-gid-0", "shell-uid-1000-gid-1000", "no-new-privileges", "memory-limit-256m", "cpu-limit-1", "process-limit-128", "shm-limit-16m", "mounts-three-named-volumes-no-binds-or-sockets", "gateway-host-access-blocked-with-positive-control"} {
					if !containsCheck(summary.Checks, "lifecycle/"+phase+"/"+name, "pass") {
						t.Fatalf("missing observation %s/%s", phase, name)
					}
				}
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil || len(fields) != 9 {
				t.Fatalf("summary schema=%s error=%v", data, err)
			}
			for _, secret := range []string{fixture.name, "/volumes/", "/private-fixture/", "selected-machine", "private-remote-target", "private-key-path", "10.0.2.2", "100000", "200000"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("summary leaked %s: %s", secret, data)
				}
			}
			for _, request := range fixture.requests {
				if containsValue(request.Args, "--force") {
					t.Fatal("forced lifecycle command")
				}
				if hostOS == "windows" && request.Name == "podman" {
					for _, value := range request.Env {
						if strings.HasPrefix(value, "CONTAINER_HOST=") || strings.HasPrefix(value, "CONTAINER_CONNECTION=") || strings.HasPrefix(value, "CONTAINER_SSHKEY=") {
							t.Fatalf("remote environment reached probe: %v", request.Args)
						}
					}
					if request.Args[0] != "machine" && (len(request.Args) < 3 || request.Args[0] != "--connection" || request.Args[1] != "selected-machine") {
						t.Fatalf("unbound Windows observation: %v", request.Args)
					}
				}
			}
		})
	}
}

func containsCheck(checks []Check, name, result string) bool {
	for _, check := range checks {
		if check.Name == name && check.Result == result {
			return true
		}
	}
	return false
}

func TestLiveRunFailsIsolationAndCleansItsSandbox(t *testing.T) {
	for _, failure := range []string{"memory", "manager", "gateway-before", "gateway-denial", "gateway-after", "adopted-mount-source"} {
		t.Run(failure, func(t *testing.T) {
			fixture := &liveObservationFixture{observations: newObservationFixture(), probes: map[string]bool{}}
			failedCheck := "lifecycle/created/"
			switch failure {
			case "memory":
				fixture.observations.kernel["memory"] = "max"
				failedCheck += "memory-limit-256m"
			case "manager":
				fixture.observations.kernel["manager"] = observationIdentity(1000)
				failedCheck += "manager-exec-uid-0-gid-0"
			case "gateway-before":
				fixture.observations.failClient = 1
				failedCheck += "gateway-host-access-blocked-with-positive-control"
			case "gateway-denial":
				fixture.observations.failClient = 2
				failedCheck += "gateway-host-access-blocked-with-positive-control"
			case "gateway-after":
				fixture.observations.failClient = 3
				failedCheck += "gateway-host-access-blocked-with-positive-control"
			case "adopted-mount-source":
				failedCheck = "lifecycle/adopted/observations-match-created"
				fixture.mutate = func(f *observationFixture, phase int) {
					if phase == 2 {
						f.container = strings.ReplaceAll(f.container, "/volumes/workspace", "/private-replacement/workspace")
					}
				}
			}
			config := liveObservationConfig(t, "linux", fixture)
			if err := Run(context.Background(), config); err == nil {
				t.Fatal("unsafe observation passed")
			}
			summary, _ := readObservationSummary(t, config)
			if summary.Result != "fail" || !containsCheck(summary.Checks, failedCheck, "fail") || !containsCheck(summary.Checks, "lifecycle/cleanup", "pass") || fixture.state != "" || fixture.volumes || len(fixture.probes) != 0 {
				t.Fatalf("failed check=%s summary=%+v state=%s volumes=%t probes=%v", failedCheck, summary, fixture.state, fixture.volumes, fixture.probes)
			}
		})
	}
}

func fixtureObserver(fixture *observationFixture) func(func(string, func() error) error, string) error {
	return newSandboxObserver(context.Background(), Config{Host: platform.Host{OS: "linux"}}, fixture.run, "live", "sample", "")
}

func recordingCheck(checks *[]Check) func(string, func() error) error {
	return func(name string, action func() error) error {
		err := action()
		result := "pass"
		if err != nil {
			result = "fail"
		}
		*checks = append(*checks, Check{Name: name, Result: result})
		return err
	}
}

func TestSandboxObservationsRepeatIndependentChecksAndCleanupGatewayProbes(t *testing.T) {
	fixture := newObservationFixture()
	observe := fixtureObserver(fixture)
	var checks []Check
	for _, phase := range []string{"created", "started-again", "adopted"} {
		if err := observe(recordingCheck(&checks), phase); err != nil {
			t.Fatal(err)
		}
	}
	if len(checks) != 41 {
		t.Fatalf("checks=%v", checks)
	}
	for _, check := range checks {
		if check.Result != "pass" {
			t.Fatal(check)
		}
	}
	var removals, networks []string
	for _, request := range fixture.requests {
		if request.Args[0] == "rm" {
			removals = append(removals, request.Args[1])
		}
		if request.Args[0] == "run" {
			for _, arg := range request.Args {
				if strings.HasPrefix(arg, "--network=") {
					networks = append(networks, arg)
				}
			}
		}
	}
	wantRemovals := []string{"sandboxed-agents-live-probe.live.sample-gateway-control", "sandboxed-agents-live-probe.live.sample-gateway-host", "sandboxed-agents-live-probe.live.sample-gateway-control", "sandboxed-agents-live-probe.live.sample-gateway-host", "sandboxed-agents-live-probe.live.sample-gateway-control", "sandboxed-agents-live-probe.live.sample-gateway-host"}
	if !reflect.DeepEqual(removals, wantRemovals) {
		t.Fatalf("probe cleanup=%v", removals)
	}
	if !reflect.DeepEqual(networks, []string{"--network=host", "--network=pasta:--map-gw", "--network=host", "--network=pasta:--map-gw", "--network=host", "--network=pasta:--map-gw"}) {
		t.Fatalf("probe networks=%v", networks)
	}
	if fixture.clientCalls != 9 {
		t.Fatalf("network observations=%d", fixture.clientCalls)
	}
}

func TestSandboxObservationsFailEachIndependentIsolationViolation(t *testing.T) {
	cases := []struct {
		name, check string
		mutate      func(*observationFixture)
	}{
		{"root-host-root", "root-maps-to-subordinate-ids", func(f *observationFixture) {
			f.host = strings.ReplaceAll(f.host, "uid-map 0 100000 1000", "uid-map 0 0 1000")
			f.host = strings.ReplaceAll(f.host, "start-host-uid 100000", "start-host-uid 0")
		}},
		{"root-outside-subordinate-range", "root-maps-to-subordinate-ids", func(f *observationFixture) {
			f.host = strings.ReplaceAll(f.host, "sub-uid 100000 65536", "sub-uid 300000 65536")
		}},
		{"agent-not-host-user", "agent-uid-1000-gid-1000-maps-to-host-user", func(f *observationFixture) {
			f.host = strings.ReplaceAll(f.host, "uid-map 1000 1000 1", "uid-map 1000 300000 1")
		}},
		{"start-as-agent", "start-uid-0-gid-0", func(f *observationFixture) { f.kernel["start"] = observationIdentity(1000) }},
		{"start-host-identity", "start-uid-0-gid-0", func(f *observationFixture) {
			f.host = strings.ReplaceAll(f.host, "start-host-gid 200000", "start-host-gid 1000")
		}},
		{"manager-as-agent", "manager-exec-uid-0-gid-0", func(f *observationFixture) { f.kernel["manager"] = observationIdentity(1000) }},
		{"shell-as-root", "shell-uid-1000-gid-1000", func(f *observationFixture) { f.agent = observationIdentity(0) }},
		{"wrong-agent-account", "shell-uid-1000-gid-1000", func(f *observationFixture) { f.agent["name"] = "other" }},
		{"wrong-agent-namespace", "shell-uid-1000-gid-1000", func(f *observationFixture) { f.agent["user_namespace"] = "user:[4026533991]" }},
		{"saved-shell-root", "shell-uid-1000-gid-1000", func(f *observationFixture) { f.agent["uid"] = []uint64{1000, 1000, 0, 1000} }},
		{"start-privileges", "no-new-privileges", func(f *observationFixture) { f.kernel["start"].(map[string]any)["no_new_privileges"] = 0 }},
		{"manager-privileges", "no-new-privileges", func(f *observationFixture) { f.kernel["manager"].(map[string]any)["no_new_privileges"] = 0 }},
		{"shell-privileges", "no-new-privileges", func(f *observationFixture) { f.agent["no_new_privileges"] = 0 }},
		{"unlimited-memory", "memory-limit-256m", func(f *observationFixture) { f.kernel["memory"] = "max" }},
		{"wrong-memory", "memory-limit-256m", func(f *observationFixture) { f.kernel["memory"] = "536870912" }},
		{"unlimited-cpu", "cpu-limit-1", func(f *observationFixture) { f.kernel["cpu"] = "max 100000" }},
		{"two-cpus", "cpu-limit-1", func(f *observationFixture) { f.kernel["cpu"] = "200000 100000" }},
		{"zero-cpu-period", "cpu-limit-1", func(f *observationFixture) { f.kernel["cpu"] = "0 0" }},
		{"unlimited-processes", "process-limit-128", func(f *observationFixture) { f.kernel["pids"] = "max" }},
		{"wrong-process-limit", "process-limit-128", func(f *observationFixture) { f.kernel["pids"] = "256" }},
		{"wrong-shm", "shm-limit-16m", func(f *observationFixture) { f.kernel["shm"] = 67108864 }},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			fixture := newObservationFixture()
			item.mutate(fixture)
			var checks []Check
			err := fixtureObserver(fixture)(recordingCheck(&checks), "created")
			if err == nil {
				t.Fatal("isolation violation passed")
			}
			found := false
			for _, check := range checks {
				if check.Name == "created/"+item.check && check.Result == "fail" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing failed %s: %v", item.check, checks)
			}
		})
	}
}

func TestSandboxObservationsGatewayRequiresBothPositiveControlsAndDenial(t *testing.T) {
	for _, call := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(call), func(t *testing.T) {
			fixture := newObservationFixture()
			fixture.failClient = call
			var checks []Check
			err := fixtureObserver(fixture)(recordingCheck(&checks), "created")
			if err == nil {
				t.Fatal("network failure passed")
			}
			last := checks[len(checks)-1]
			if last.Name != "created/gateway-host-access-blocked-with-positive-control" || last.Result != "fail" {
				t.Fatal(checks)
			}
			var removed []string
			for _, request := range fixture.requests {
				if request.Args[0] == "rm" {
					removed = append(removed, request.Args[1])
				}
			}
			if len(removed) != 2 {
				t.Fatalf("network failure leaked probes: %v", removed)
			}
		})
	}
}

func TestSandboxObservationsRejectChangedVolumeSourcesAfterReuse(t *testing.T) {
	fixture := newObservationFixture()
	observe := fixtureObserver(fixture)
	var checks []Check
	if err := observe(recordingCheck(&checks), "created"); err != nil {
		t.Fatal(err)
	}
	fixture.container = strings.ReplaceAll(fixture.container, "/volumes/workspace", "/other-volume/workspace")
	if err := observe(recordingCheck(&checks), "adopted"); err == nil {
		t.Fatal("changed adopted volume passed")
	}
	if last := checks[len(checks)-1]; last.Name != "adopted/observations-match-created" || last.Result != "fail" {
		t.Fatal(checks)
	}
}

func TestSandboxObservationsRejectChangedUserNamespaceAfterStart(t *testing.T) {
	fixture := newObservationFixture()
	observe := fixtureObserver(fixture)
	var checks []Check
	if err := observe(recordingCheck(&checks), "created"); err != nil {
		t.Fatal(err)
	}
	fixture.host = strings.ReplaceAll(fixture.host, "uid-map 0 100000 1000", "uid-map 0 100001 1000")
	fixture.host = strings.ReplaceAll(fixture.host, "uid-map 1001 101000 64535", "uid-map 1001 101001 64534")
	fixture.host = strings.ReplaceAll(fixture.host, "start-host-uid 100000", "start-host-uid 100001")
	if err := observe(recordingCheck(&checks), "started-again"); err == nil {
		t.Fatal("changed namespace passed")
	}
	if last := checks[len(checks)-1]; last.Name != "started-again/observations-match-created" || last.Result != "fail" {
		t.Fatal(checks)
	}
}

func TestSandboxObservationsWindowsReadOnlyTheSelectedMachineHost(t *testing.T) {
	fixture := newObservationFixture()
	observe := newSandboxObserver(context.Background(), Config{Host: platform.Host{OS: "windows"}}, fixture.run, "live", "sample", "selected-machine")
	if err := observe(func(_ string, action func() error) error { return action() }, "created"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, request := range fixture.requests {
		if request.Name == "sh" {
			t.Fatal("Windows probe read local /proc")
		}
		if request.Args[0] == "machine" {
			found = true
			if !reflect.DeepEqual(request.Args[:5], []string{"machine", "ssh", "selected-machine", "sh", "-c"}) {
				t.Fatal(request.Args)
			}
		}
	}
	if !found {
		t.Fatal("Windows probe omitted machine host facts")
	}
}

func TestSandboxObservationsNeverForceProbeCleanup(t *testing.T) {
	fixture := newObservationFixture()
	if err := fixtureObserver(fixture)(func(_ string, action func() error) error { return action() }, "created"); err != nil {
		t.Fatal(err)
	}
	for _, request := range fixture.requests {
		for _, arg := range request.Args {
			if arg == "--force" {
				t.Fatalf("forced cleanup: %v", request.Args)
			}
		}
	}
}

func TestSandboxObservationsLeaveForeignGatewayProbesUntouched(t *testing.T) {
	fixture := newObservationFixture()
	fixture.probeOwner = "foreign"
	if err := fixtureObserver(fixture)(func(_ string, action func() error) error { return action() }, "created"); err == nil {
		t.Fatal("foreign probe cleanup passed")
	}
	for _, request := range fixture.requests {
		if request.Args[0] == "stop" || request.Args[0] == "rm" {
			t.Fatalf("foreign probe changed: %v", request.Args)
		}
	}
}

func TestSandboxObservationsGatewayCleanupRunsAfterCancellation(t *testing.T) {
	fixture := newObservationFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := func(ctx context.Context, request process.Request) (int, error) {
		if containsValue(request.Args, gatewayClientScript) && request.Args[len(request.Args)-1] == "denied" {
			cancel()
			return 1, ctx.Err()
		}
		if request.Args[0] == "container" || request.Args[0] == "stop" || request.Args[0] == "rm" {
			if err := ctx.Err(); err != nil {
				t.Fatalf("canceled cleanup context for %v: %v", request.Args, err)
			}
		}
		return fixture.run(ctx, request)
	}
	observe := newSandboxObserver(ctx, Config{Host: platform.Host{OS: "linux"}}, run, "live", "sample", "")
	if err := observe(func(_ string, action func() error) error { return action() }, "created"); err == nil {
		t.Fatal("canceled gateway observation passed")
	}
	removed := 0
	for _, request := range fixture.requests {
		if request.Args[0] == "rm" {
			removed++
		}
	}
	if removed != 2 {
		t.Fatalf("cancellation leaked probes: removed=%d", removed)
	}
}

func TestSandboxObservationsFailWhenGatewayCleanupFails(t *testing.T) {
	fixture := newObservationFixture()
	fixture.failCleanup = true
	var checks []Check
	if err := fixtureObserver(fixture)(recordingCheck(&checks), "created"); err == nil {
		t.Fatal("leaked probes passed")
	}
	if last := checks[len(checks)-1]; last.Name != "created/gateway-host-access-blocked-with-positive-control" || last.Result != "fail" {
		t.Fatal(checks)
	}
}

func TestSandboxObservationsRefuseMalformedUserNamespaceFacts(t *testing.T) {
	for _, facts := range []string{
		strings.ReplaceAll(validHostObservation, "uid-map 1000 1000 1", "uid-map 500 1000 1"),
		strings.ReplaceAll(validHostObservation, "uid-map 1000 1000 1", "uid-map 1000 100000 1"),
		strings.ReplaceAll(validHostObservation, "uid-map 1000 1000 1", "uid-map 1000 1000 0"),
		strings.ReplaceAll(validHostObservation, "uid-map 1000 1000 1", "uid-map 4294967295 1000 2"),
		strings.ReplaceAll(validHostObservation, "uid-map 1000 1000 1", "uid-map 1000 4294967295 2"),
		strings.ReplaceAll(validHostObservation, "sub-uid 100000 65536", "sub-uid 100000 0"),
		strings.ReplaceAll(validHostObservation, "host-uid 1000", "host-uid 1000\nhost-uid 1001"),
		strings.ReplaceAll(validHostObservation, "start-host-uid 100000\n", ""),
	} {
		t.Run(fmt.Sprintf("case-%d", len(facts)), func(t *testing.T) {
			fixture := newObservationFixture()
			fixture.host = facts
			var checks []Check
			if err := fixtureObserver(fixture)(recordingCheck(&checks), "created"); err == nil {
				t.Fatal("malformed mappings passed")
			}
			if !containsCheck(checks, "created/kernel-observations", "fail") {
				t.Fatal(checks)
			}
		})
	}
}
