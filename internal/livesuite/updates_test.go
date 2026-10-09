package livesuite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type updateFixture struct {
	*sshFixture
	port, id, image, privateImage, backup, data string
	updateCalls, imageRemovals                  int
}

func newUpdateFixture(t *testing.T, hostOS string) *updateFixture {
	base := newSSHFixture(t, hostOS)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	f := &updateFixture{sshFixture: base, port: port}
	f.config.SSH = false
	f.config.Updates = true
	f.config.Run = f.run
	return f
}

func (f *updateFixture) record() map[string]any {
	container := "sandboxed-agents.live." + f.name
	labels := map[string]string{
		"io.github.sandboxed-agents.owner": "live", "io.github.sandboxed-agents.sandbox-name": f.name,
		"io.github.sandboxed-agents.toolchains": "", "io.github.sandboxed-agents.workspace-kind": "volume",
		"io.github.sandboxed-agents.ssh-port": f.port, "io.github.sandboxed-agents.memory": "268435456",
		"io.github.sandboxed-agents.cpus": "1", "io.github.sandboxed-agents.pids-limit": "128", "io.github.sandboxed-agents.shm-size": "16777216",
	}
	mounts := []any{}
	for suffix, target := range map[string]string{"home": "/home/agent", "ssh": "/etc/ssh", "workspace": "/workspace"} {
		mounts = append(mounts, map[string]any{"Type": "volume", "Name": container + "." + suffix, "Source": "/private/" + suffix, "Destination": target, "RW": true})
	}
	return map[string]any{"Id": f.id, "Image": f.image, "Name": container, "Config": map[string]any{"Labels": labels}, "State": map[string]any{"Running": f.state == "running", "Status": map[bool]string{true: "running", false: "exited"}[f.state == "running"]}, "Mounts": mounts, "HostConfig": map[string]any{"Memory": 268435456, "NanoCpus": 1000000000, "CpuPeriod": 0, "CpuQuota": 0, "PidsLimit": 128, "ShmSize": 16777216, "PortBindings": map[string]any{"22/tcp": []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": f.port}}}}}
}

func (f *updateFixture) run(ctx context.Context, request process.Request) (int, error) {
	args := request.Args
	if request.Name == "podman" && args[0] == "--connection" {
		args = args[2:]
	}
	write := func(value any) (int, error) { return 0, json.NewEncoder(request.Streams.Stdout).Encode(value) }
	if request.Name == "ssh-keyscan" {
		fmt.Fprintf(request.Streams.Stdout, "[127.0.0.1]:%s ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea\n", f.port)
		return 0, nil
	}
	if request.Name == "podman" {
		handled := true
		defer func() {
			if handled {
				f.requests = append(f.requests, request)
			}
		}()
		switch args[0] {
		case "ps":
			items := []any{}
			if f.state != "" {
				items = append(items, map[string]any{"Names": []string{"sandboxed-agents.live." + f.name}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			if f.backup != "" {
				items = append(items, map[string]any{"Names": []string{f.backup}, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
			}
			return write(items)
		case "image":
			if args[1] == "inspect" {
				if args[2] == f.privateImage && f.privateImage != "" {
					return write([]any{map[string]any{"Id": f.privateImage, "RepoTags": []string{}, "Labels": map[string]string{"io.github.sandboxed-agents.live-fixture": f.name}}})
				}
				return write([]any{map[string]any{"Id": "sha256:current"}})
			}
			if args[1] == "rm" {
				if args[2] != f.privateImage {
					f.t.Fatalf("deleted non-private image %v", args)
				}
				f.imageRemovals++
				return 0, nil
			}
		case "create":
			f.id = "private-original"
			f.image = args[len(args)-1]
			return 0, nil
		case "commit":
			if len(args) != 6 || args[1] != "--quiet" || args[2] != "--include-volumes=false" || args[3] != "--change" || !strings.HasPrefix(args[4], "LABEL io.github.sandboxed-agents.live-fixture=") {
				f.t.Fatalf("unsafe commit %v", args)
			}
			f.privateImage = "sha256:" + strings.Repeat("b", 64)
			fmt.Fprintln(request.Streams.Stdout, f.privateImage)
			return 0, nil
		case "rename":
			if strings.HasPrefix(args[1], "sandboxed-agents-backup.") {
				f.backup = ""
			} else {
				f.backup = args[2]
			}
			f.state = "stopped"
			return 0, nil
		case "rm":
			f.backup = ""
			return 0, nil
		case "exec":
			if args[3] != "node" {
				fmt.Fprintln(request.Streams.Stdout, "sandboxed-agents-manager fixture")
				return 0, nil
			}
			if strings.Contains(args[5], "writeFileSync") {
				f.data = args[6]
				return 0, nil
			}
			return write(map[string]string{"/home/agent": f.data, "/etc/ssh": f.data, "/workspace": f.data})
		case "container":
			switch args[1] {
			case "exists":
				if strings.HasPrefix(args[2], "sandboxed-agents-backup.") && f.backup == "" {
					return 1, nil
				}
				return 0, nil
			case "inspect":
				if len(args) == 3 {
					record := f.record()
					record["NetworkSettings"] = map[string]any{"Ports": map[string]any{"22/tcp": []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": f.port}}}}
					return write([]any{record})
				}
			}
		case "volume":
			if args[1] == "inspect" {
				volumes := []any{}
				for _, name := range args[2:] {
					volumes = append(volumes, map[string]any{"Name": name, "CreatedAt": "2026-10-09T00:00:00Z", "Mountpoint": "/private/" + name, "Labels": map[string]string{"io.github.sandboxed-agents.owner": "live"}})
				}
				return write(volumes)
			}
		}
		handled = false
	}
	if request.Name != "podman" && len(args) > 0 && args[0] == "remove" && f.backup != "" {
		return 42, nil
	}
	if request.Name != "podman" && len(args) > 0 && args[0] == "update" {
		if f.backup != "" {
			f.backup = ""
			return 0, nil
		}
		f.requests = append(f.requests, request)
		f.updateCalls++
		if strings.Contains(f.name, "rollback") {
			listener, err := net.Dial("tcp4", "127.0.0.1:"+f.port)
			if err != nil {
				f.t.Fatalf("failure port not reserved: %v", err)
			}
			listener.Close()
			fmt.Fprintln(request.Streams.Stderr, `update failed at step "start the new container"; Sandbox was restored: its original container is back`)
			return 1, nil
		}
		f.id = "replacement"
		f.image = "sha256:current"
		return 0, nil
	}
	var output bytes.Buffer
	original := request.Streams.Stdout
	request.Streams.Stdout = &output
	status, err := f.sshFixture.run(ctx, request)
	if len(args) > 0 && args[0] == "up" {
		f.id = "created"
		f.image = "sha256:current"
	}
	if original != nil {
		fmt.Fprint(original, strings.ReplaceAll(output.String(), "2222", f.port))
	}
	return status, err
}

func TestLiveUpdatesReplaceImagePreserveConfigurationDataAndInstalledSSH(t *testing.T) {
	for _, hostOS := range []string{"linux", "windows"} {
		t.Run(hostOS, func(t *testing.T) {
			f := newUpdateFixture(t, hostOS)
			if err := livesuite.Run(context.Background(), f.config); err != nil {
				t.Fatal(err)
			}
			platformName := map[string]string{"linux": "linux", "windows": "windows-11"}[hostOS]
			summary := readSummary(t, f.config, platformName)
			for _, name := range []string{"updates/success", "updates/success/outdated", "updates/success/update", "updates/success/preserved", "updates/success/updated/setup-unchanged", "updates/success/updated/connect", "updates/success/cleanup"} {
				if !hasCheck(summary, name, "pass") {
					t.Fatalf("missing %s: %+v", name, summary)
				}
			}
			if f.updateCalls < 1 || f.imageRemovals < 1 || f.state != "" || f.volumes {
				t.Fatalf("updates=%d images removed=%d state=%s", f.updateCalls, f.imageRemovals, f.state)
			}
			connections := 0
			for _, request := range f.requests {
				if slices.Contains(request.Args, "build") {
					t.Fatalf("shared image build issued: %v", request.Args)
				}
				if request.Name == "ssh" && slices.Contains(request.Args, "id") {
					connections++
				}
			}
			if connections < 2 {
				t.Fatalf("SSH connections=%d", connections)
			}
			data, err := os.ReadFile(filepath.Join(f.config.OutputDirectory, "live-suite-"+platformName+".json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{f.name, f.sshRoot, "/private/", "sha256:", f.data, f.port, "selected-machine"} {
				if secret != "" && strings.Contains(string(data), secret) {
					t.Fatalf("summary leaked %q", secret)
				}
			}
		})
	}
}

func TestLiveUpdatesForceFailureAfterRenameAndRestoreOriginalStoppedSandbox(t *testing.T) {
	for _, hostOS := range []string{"linux", "windows"} {
		t.Run(hostOS, func(t *testing.T) {
			f := newUpdateFixture(t, hostOS)
			if err := livesuite.Run(context.Background(), f.config); err != nil {
				t.Fatal(err)
			}
			summary := readSummary(t, f.config, map[string]string{"linux": "linux", "windows": "windows-11"}[hostOS])
			for _, name := range []string{"updates/rollback", "updates/rollback/outdated", "updates/rollback/forced-failure", "updates/rollback/restored", "updates/rollback/data-preserved", "updates/rollback/cleanup"} {
				if !hasCheck(summary, name, "pass") {
					t.Fatalf("missing %s: %+v", name, summary)
				}
			}
			if f.updateCalls != 2 || f.imageRemovals != 2 || f.backup != "" || f.state != "" || f.volumes {
				t.Fatalf("updateCalls=%d removals=%d backup=%s state=%s", f.updateCalls, f.imageRemovals, f.backup, f.state)
			}
		})
	}
}

func TestLiveUpdatesRefusePrivateFixtureImagesWithSharedTags(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		args := request.Args
		if request.Name == "podman" && len(args) > 2 && args[0] == "image" && args[1] == "inspect" && args[2] == f.privateImage && f.privateImage != "" {
			return 0, json.NewEncoder(request.Streams.Stdout).Encode([]any{map[string]any{"Id": f.privateImage, "RepoTags": []string{"localhost/sandboxed-agents:base-shared"}, "Config": map[string]any{"Labels": map[string]string{"io.github.sandboxed-agents.live-fixture": f.name}}}})
		}
		return f.run(ctx, request)
	}
	if err := livesuite.Run(context.Background(), f.config); err == nil {
		t.Fatal("tagged fixture image passed")
	}
	for _, request := range f.requests {
		if request.Name == "podman" && slices.Contains(request.Args, "rename") {
			t.Fatal("tagged image reached replacement")
		}
	}
}

func TestLiveUpdatesRestoreTheirSourceWhenFixtureCreationFails(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" && request.Args[0] == "create" {
			return 42, nil
		}
		return f.run(ctx, request)
	}
	if err := livesuite.Run(context.Background(), f.config); err == nil {
		t.Fatal("failed fixture construction passed")
	}
	if f.backup != "" || f.state != "" || f.volumes || f.imageRemovals != 1 {
		t.Fatalf("fixture leaked backup=%q state=%q volumes=%t imagesRemoved=%d", f.backup, f.state, f.volumes, f.imageRemovals)
	}
	if !hasCheck(readSummary(t, f.config, "linux"), "updates/success/cleanup", "pass") {
		t.Fatal("fixture construction failure prevented CLI cleanup")
	}
}

func TestLiveUpdatesLeaveAllUpdateChecksNotRunWhenUnselected(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	f.config.Updates = false
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	summary := readSummary(t, f.config, "linux")
	for _, name := range []string{"updates", "updates/success", "updates/success/outdated", "updates/success/updated/connect", "updates/rollback", "updates/rollback/forced-failure", "updates/rollback/restored"} {
		if !hasCheck(summary, name, "not-run") {
			t.Fatalf("unselected %s missing: %+v", name, summary)
		}
	}
	if f.name != "" {
		t.Fatal("unselected updates created sandbox")
	}
}

func TestLiveUpdatesPrepareFixturesUsingRemoteSupportedCommands(t *testing.T) {
	f := newUpdateFixture(t, "windows")
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" && slices.Contains(request.Args, "clone") {
			return 1, fmt.Errorf("cloning a container is not supported on the remote client")
		}
		return f.run(ctx, request)
	}
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
}

func TestLiveUpdatesBindAndRecheckWindowsTargetBeforeEveryAction(t *testing.T) {
	f := newUpdateFixture(t, "windows")
	for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
		t.Setenv(key, "ambient-private-target")
	}
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	actions := 0
	for i, request := range f.requests {
		if request.Name == "podman" {
			for _, env := range request.Env {
				for _, key := range []string{"CONTAINER_HOST=", "CONTAINER_CONNECTION=", "CONTAINER_SSHKEY="} {
					if strings.HasPrefix(strings.ToUpper(env), key) {
						t.Fatalf("remote variable reached podman: %s", env)
					}
				}
			}
			if request.Args[0] == "machine" {
				continue
			}
			if len(request.Args) < 3 || request.Args[0] != "--connection" || request.Args[1] != "selected-machine" {
				t.Fatalf("unbound query %v", request.Args)
			}
		} else if !slices.Contains([]string{"up", "stop", "start", "update", "remove"}, request.Args[0]) {
			continue
		}
		if i == 0 || f.requests[i-1].Name != "podman" || !slices.Equal(f.requests[i-1].Args, []string{"machine", "inspect", "selected-machine"}) {
			t.Fatalf("target not checked before %s %v; previous=%s %v", request.Name, request.Args, f.requests[max(0, i-1)].Name, f.requests[max(0, i-1)].Args)
		}
		actions++
	}
	if actions < 20 {
		t.Fatalf("only %d target actions checked", actions)
	}
}

func TestLiveUpdatesWaitForHealthyFixtureBeforeUpdating(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	probes := 0
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" && request.Args[0] == "exec" && request.Args[len(request.Args)-1] == "version" {
			probes++
			if probes == 1 {
				return 42, nil
			}
		}
		return f.run(ctx, request)
	}
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	if probes < 3 || !hasCheck(readSummary(t, f.config, "linux"), "updates/success/fixture-ready", "pass") {
		t.Fatalf("healthy fixture never probed; probes=%d", probes)
	}
}

func TestLiveUpdatesRejectIncompleteSuccessAndCleanup(t *testing.T) {
	for _, failure := range []string{"same-image", "same-container", "memory-label", "memory-limit", "cpu-limit", "pids-limit", "shm-limit", "mount", "workspace", "port", "toolchains", "volume-replaced", "data-lost", "backup", "host-key", "reconnect", "remove-fails", "image-removal-fails"} {
		t.Run(failure, func(t *testing.T) {
			f := newUpdateFixture(t, "linux")
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				args := request.Args
				if failure == "remove-fails" && args[0] == "remove" {
					return 42, nil
				}
				if failure == "image-removal-fails" && request.Name == "podman" && args[0] == "image" && args[1] == "rm" {
					return 42, nil
				}
				var output bytes.Buffer
				original := request.Streams.Stdout
				request.Streams.Stdout = &output
				status, err := f.run(ctx, request)
				if request.Name != "podman" && args[0] == "update" && failure == "host-key" {
					err = os.WriteFile(filepath.Join(f.keyDirectory(), "known_hosts"), []byte("changed-pin"), 0600)
				}
				if f.updateCalls == 1 {
					if failure == "reconnect" && request.Name == "ssh" && slices.Contains(args, "id") {
						status = 255
					}
					if request.Name == "podman" && args[0] == "container" && args[1] == "exists" && failure == "backup" {
						f.backup = "sandboxed-agents-backup.live." + f.name
						status = 0
					}
					if request.Name == "podman" && args[0] == "container" && args[1] == "inspect" {
						records := []map[string]any{}
						if err := json.Unmarshal(output.Bytes(), &records); err != nil {
							t.Fatal(err)
						}
						record := records[0]
						labels := record["Config"].(map[string]any)["Labels"].(map[string]any)
						host := record["HostConfig"].(map[string]any)
						switch failure {
						case "same-image":
							record["Image"] = f.privateImage
						case "same-container":
							record["Id"] = "private-original"
						case "memory-label":
							labels["io.github.sandboxed-agents.memory"] = "536870912"
						case "memory-limit":
							host["Memory"] = 536870912
						case "cpu-limit":
							host["NanoCpus"] = 2000000000
						case "pids-limit":
							host["PidsLimit"] = 256
						case "shm-limit":
							host["ShmSize"] = 33554432
						case "mount":
							record["Mounts"].([]any)[0].(map[string]any)["Name"] = "foreign-volume"
						case "workspace":
							labels["io.github.sandboxed-agents.workspace-kind"] = "bind"
						case "port":
							host["PortBindings"].(map[string]any)["22/tcp"].([]any)[0].(map[string]any)["HostPort"] = "2"
						case "toolchains":
							labels["io.github.sandboxed-agents.toolchains"] = "native"
						}
						output.Reset()
						json.NewEncoder(&output).Encode(records)
					}
					if request.Name == "podman" && args[0] == "volume" && args[1] == "inspect" && failure == "volume-replaced" {
						data := strings.ReplaceAll(output.String(), "2026-10-09T00:00:00Z", "2026-10-10T00:00:00Z")
						output.Reset()
						output.WriteString(data)
					}
					if request.Name == "podman" && args[0] == "exec" && strings.Contains(args[len(args)-1], "readFileSync") && failure == "data-lost" {
						output.Reset()
						output.WriteString(`{"/home/agent":"lost","/etc/ssh":"lost","/workspace":"lost"}`)
					}
				}
				if original != nil {
					original.Write(output.Bytes())
				}
				return status, err
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("incomplete update verification passed")
			}
			summary := readSummary(t, f.config, "linux")
			if !hasCheck(summary, "updates/success", "fail") || !hasCheck(summary, "updates", "fail") {
				t.Fatalf("missing failed summary: %+v", summary)
			}
		})
	}
}

func TestLiveUpdatesRejectEarlyFailureAndIncompleteRollback(t *testing.T) {
	for _, failure := range []string{"early-rename", "early-create", "already-current", "no-restoration-diagnostic", "rollback-incomplete", "wrong-id", "wrong-name", "wrong-image", "wrong-state", "backup", "volume-missing", "data-lost"} {
		t.Run(failure, func(t *testing.T) {
			f := newUpdateFixture(t, "linux")
			f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
				args := request.Args
				rollback := f.name != "" && strings.Contains(f.name, "rollback")
				if rollback && request.Name != "podman" && args[0] == "update" {
					switch failure {
					case "early-rename":
						fmt.Fprintln(request.Streams.Stderr, `failed at step "rename the container to the backup name"; was restored:`)
						return 1, nil
					case "early-create":
						fmt.Fprintln(request.Streams.Stderr, `failed at step "create the new container"; was restored:`)
						return 1, nil
					case "already-current":
						return 0, nil
					case "no-restoration-diagnostic":
						fmt.Fprintln(request.Streams.Stderr, `failed at step "start the new container"`)
						return 1, nil
					case "rollback-incomplete":
						fmt.Fprintln(request.Streams.Stderr, `failed at step "start the new container"; was not restored`)
						return 1, nil
					}
				}
				var output bytes.Buffer
				original := request.Streams.Stdout
				request.Streams.Stdout = &output
				status, err := f.run(ctx, request)
				if rollback && f.updateCalls == 2 {
					if request.Name == "podman" && args[0] == "container" && args[1] == "inspect" {
						records := []map[string]any{}
						json.Unmarshal(output.Bytes(), &records)
						record := records[0]
						switch failure {
						case "wrong-id":
							record["Id"] = "replacement"
						case "wrong-name":
							record["Name"] = "sandboxed-agents-backup.live." + f.name
						case "wrong-image":
							record["Image"] = "sha256:current"
						case "wrong-state":
							record["State"] = map[string]any{"Running": true, "Status": "running"}
						}
						output.Reset()
						json.NewEncoder(&output).Encode(records)
					}
					if request.Name == "podman" && args[0] == "container" && args[1] == "exists" && failure == "backup" {
						f.backup = "sandboxed-agents-backup.live." + f.name
						status = 0
					}
					if request.Name == "podman" && args[0] == "volume" && args[1] == "inspect" && failure == "volume-missing" {
						status = 1
					}
					if request.Name == "podman" && args[0] == "exec" && strings.Contains(args[len(args)-1], "readFileSync") && failure == "data-lost" {
						output.Reset()
						output.WriteString(`{"/home/agent":"lost","/etc/ssh":"lost","/workspace":"lost"}`)
					}
				}
				if original != nil {
					original.Write(output.Bytes())
				}
				return status, err
			}
			if err := livesuite.Run(context.Background(), f.config); err == nil {
				t.Fatal("unobserved or incomplete rollback passed")
			}
			summary := readSummary(t, f.config, "linux")
			if !hasCheck(summary, "updates/success", "pass") || !hasCheck(summary, "updates/rollback", "fail") || !hasCheck(summary, "updates", "fail") {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}

func TestLiveUpdatesCleanTheirSandboxVolumesAndPrivateImageAfterCancellation(t *testing.T) {
	for _, phase := range []string{"success", "rollback", "fixture-create"} {
		t.Run(phase, func(t *testing.T) {
			f := newUpdateFixture(t, "linux")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sandboxCleaned, imageCleaned := false, false
			f.config.Run = func(callCtx context.Context, request process.Request) (int, error) {
				args := request.Args
				if (request.Name != "podman" && args[0] == "update" && strings.Contains(f.name, phase)) || (phase == "fixture-create" && request.Name == "podman" && args[0] == "create") {
					cancel()
					return 0, ctx.Err()
				}
				if args[0] == "remove" || (request.Name == "podman" && args[0] == "image" && args[1] == "rm") {
					deadline, ok := callCtx.Deadline()
					if callCtx.Err() != nil || !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 90*time.Second {
						t.Fatal("cleanup context cancelled or unbounded")
					}
					if args[0] == "remove" {
						sandboxCleaned = true
					} else {
						imageCleaned = true
					}
				}
				return f.run(callCtx, request)
			}
			if err := livesuite.Run(ctx, f.config); err == nil {
				t.Fatal("cancelled updates passed")
			}
			if !sandboxCleaned || !imageCleaned || f.state != "" || f.volumes || f.backup != "" {
				t.Fatalf("sandboxCleaned=%t imageCleaned=%t state=%s volumes=%t backup=%s", sandboxCleaned, imageCleaned, f.state, f.volumes, f.backup)
			}
		})
	}
}

func TestLiveUpdatesRunAfterSSHHasCleanedItsSandboxAndHostSetup(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	f.config.SSH = true
	if err := livesuite.Run(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	summary := readSummary(t, f.config, "linux")
	for _, name := range []string{"ssh", "ssh/cleanup", "updates", "updates/success", "updates/rollback"} {
		if !hasCheck(summary, name, "pass") {
			t.Fatalf("combined suite missing %s", name)
		}
	}
	ups := 0
	removals := 0
	for _, request := range f.requests {
		if request.Args[0] == "remove" {
			removals++
		}
		if request.Args[0] == "up" {
			if ups > 0 && removals != ups {
				t.Fatal("next part created sandbox before previous cleanup")
			}
			ups++
		}
	}
	if ups != 3 || removals != 3 {
		t.Fatalf("ups=%d removals=%d", ups, removals)
	}
}

func TestLiveUpdatesRefuseChangedWindowsTargetBeforeFixtureMutation(t *testing.T) {
	f := newUpdateFixture(t, "windows")
	changed := false
	mutatedAfterChange := false
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		args := request.Args
		if changed && request.Name == "podman" && slices.Equal(args, []string{"machine", "list", "--format", "json"}) {
			return 0, json.NewEncoder(request.Streams.Stdout).Encode([]any{map[string]any{"Name": "different-machine", "Default": true, "Running": true, "VMType": "wsl"}})
		}
		if changed && request.Name == "podman" && args[0] == "machine" && args[1] == "inspect" {
			return 0, json.NewEncoder(request.Streams.Stdout).Encode([]any{map[string]any{"Name": "different-machine", "State": "running", "Rootful": false}})
		}
		if changed && (slices.Contains(args, "rename") || slices.Contains(args, "create") || slices.Contains(args, "rm") || args[0] == "remove" || args[0] == "update") {
			mutatedAfterChange = true
		}
		status, err := f.run(ctx, request)
		if request.Name == "podman" && slices.Contains(args, "commit") {
			changed = true
		}
		return status, err
	}
	if err := livesuite.Run(context.Background(), f.config); err == nil {
		t.Fatal("changed selected target passed")
	}
	if mutatedAfterChange {
		t.Fatal("suite mutated after target selection changed")
	}
	summary := readSummary(t, f.config, "windows-11")
	if !hasCheck(summary, "updates/success/outdated", "fail") || !hasCheck(summary, "updates/success/cleanup", "fail") || !hasCheck(summary, "updates/success/fixture-cleanup", "fail") {
		t.Fatalf("changed target summary=%+v", summary)
	}
}

func TestLiveUpdatesPreserveTheWholeInstalledSSHSetup(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		status, err := f.run(ctx, request)
		if request.Name != "podman" && request.Args[0] == "update" && strings.Contains(f.name, "success") {
			data, readErr := os.ReadFile(f.userConfig)
			if readErr != nil {
				t.Fatal(readErr)
			}
			err = os.WriteFile(f.userConfig, append(data, []byte("# unexpected host edit\n")...), 0600)
		}
		return status, err
	}
	if err := livesuite.Run(context.Background(), f.config); err == nil {
		t.Fatal("changed user SSH configuration passed")
	}
	if !hasCheck(readSummary(t, f.config, "linux"), "updates/success/updated/setup-unchanged", "fail") {
		t.Fatal("host configuration change was not observed")
	}
}

func TestLiveUpdatesRecoverLeftoverOwnedBackupBeforeCLICleanup(t *testing.T) {
	f := newUpdateFixture(t, "linux")
	leftover := false
	f.config.Run = func(ctx context.Context, request process.Request) (int, error) {
		status, err := f.run(ctx, request)
		if request.Name != "podman" && request.Args[0] == "update" && !leftover {
			f.backup = "sandboxed-agents-backup.live." + f.name
			leftover = true
		}
		return status, err
	}
	if err := livesuite.Run(context.Background(), f.config); err == nil {
		t.Fatal("leftover backup passed successful update assertion")
	}
	if f.backup != "" || f.state != "" || f.volumes || f.imageRemovals != 1 {
		t.Fatalf("leaked state backup=%s state=%s volumes=%t removedImages=%d", f.backup, f.state, f.volumes, f.imageRemovals)
	}
	if !hasCheck(readSummary(t, f.config, "linux"), "updates/success/cleanup", "pass") {
		t.Fatal("backup prevented CLI cleanup")
	}
}
