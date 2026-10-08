package livesuite

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sandbox"
)

func runLifecycle(ctx context.Context, config Config, run process.Runner, executable, group string, check func(string, func() error) error) (result error) {
	var podman process.Runner
	var connection string
	if err := check("lifecycle/target", func() error {
		var err error
		podman, connection, err = newLivePodman(ctx, config, run)
		return err
	}); err != nil {
		return err
	}
	if err := check("lifecycle/group-empty", func() error { return requireEmptyGroup(ctx, podman, group) }); err != nil {
		return err
	}
	name := "lifecycle-" + strings.ToLower(rand.Text())
	invoke := func(ctx context.Context, selectedGroup string, args []string, output io.Writer) error {
		if connection != "" {
			selection, cancel := context.WithTimeout(ctx, 30*time.Second)
			current, err := preflight.SelectWindowsConnection(selection, podman)
			cancel()
			if err != nil {
				return err
			}
			if current != connection {
				return errors.New(changedPodmanTargetMessage)
			}
		}
		status, err := run(ctx, process.Request{Name: executable, Args: args, Env: withGroup(os.Environ(), selectedGroup), Streams: process.Streams{Stdout: output, Stderr: config.Stderr}})
		if err != nil {
			return err
		}
		if status != 0 {
			return fmt.Errorf("%s: exit status %d", args[0], status)
		}
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
		defer cancel()
		result = errors.Join(result, check("lifecycle/cleanup", func() error {
			if requireEmptyGroup(cleanup, podman, group) == nil {
				return nil
			}
			if err := invoke(cleanup, group, []string{"remove", name, "--volumes"}, config.Stdout); err != nil {
				return err
			}
			return requireEmptyGroup(cleanup, podman, group)
		}))
	}()
	up := func() error {
		return invoke(ctx, group, []string{"up", name, "--with", "none", "--memory", "256m", "--cpus", "1", "--pids-limit", "128", "--shm-size", "16m"}, config.Stdout)
	}
	if err := check("lifecycle/up", up); err != nil {
		return err
	}
	state := func(phase, expected string) error {
		if err := check("lifecycle/"+phase+"/list-"+strings.ReplaceAll(expected, " ", "-"), func() error {
			var output bytes.Buffer
			if err := invoke(ctx, group, []string{"list"}, &output); err != nil {
				return err
			}
			return verifyLifecycleList(output.String(), name, expected)
		}); err != nil {
			return err
		}
		return check("lifecycle/"+phase+"/default-group-absent", func() error {
			var output bytes.Buffer
			if err := invoke(ctx, "default", []string{"list"}, &output); err != nil {
				return err
			}
			return verifyLifecycleList(output.String(), name, "absent")
		})
	}
	if err := state("created", "running"); err != nil {
		return err
	}
	observe := newSandboxObserver(ctx, config, podman, group, name, connection)
	if err := observe(check, "lifecycle/created"); err != nil {
		return err
	}
	var original []lifecycleVolume
	if err := check("lifecycle/created/volumes", func() error {
		var err error
		original, err = readLifecycleVolumes(ctx, podman, group, name)
		return err
	}); err != nil {
		return err
	}
	if err := check("lifecycle/stop", func() error { return invoke(ctx, group, []string{"stop", name}, config.Stdout) }); err != nil {
		return err
	}
	if err := state("stopped", "stopped"); err != nil {
		return err
	}
	if err := check("lifecycle/start", func() error { return invoke(ctx, group, []string{"start", name}, config.Stdout) }); err != nil {
		return err
	}
	if err := state("started-again", "running"); err != nil {
		return err
	}
	if err := observe(check, "lifecycle/started-again"); err != nil {
		return err
	}
	if err := check("lifecycle/remove-keep-volumes", func() error { return invoke(ctx, group, []string{"remove", name}, config.Stdout) }); err != nil {
		return err
	}
	if err := state("removed", "volumes only"); err != nil {
		return err
	}
	preserved := func() error {
		current, err := readLifecycleVolumes(ctx, podman, group, name)
		if err != nil {
			return err
		}
		if !slices.Equal(current, original) {
			return errors.New(changedLifecycleVolumesMessage)
		}
		return nil
	}
	if err := check("lifecycle/removed/volumes-preserved", preserved); err != nil {
		return err
	}
	if err := check("lifecycle/adopting-up", up); err != nil {
		return err
	}
	if err := state("adopted", "running"); err != nil {
		return err
	}
	if err := check("lifecycle/adopted/volumes-preserved", preserved); err != nil {
		return err
	}
	if err := observe(check, "lifecycle/adopted"); err != nil {
		return err
	}
	if err := check("lifecycle/remove-volumes", func() error { return invoke(ctx, group, []string{"remove", name, "--volumes"}, config.Stdout) }); err != nil {
		return err
	}
	if err := state("cleaned", "absent"); err != nil {
		return err
	}
	return check("lifecycle/group-clean", func() error { return requireEmptyGroup(ctx, podman, group) })
}

func verifyLifecycleList(output, name, expected string) error {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "NAME STATE WORKSPACE SSH PORT TOOLCHAINS AGENTS VOLUMES" {
		return errors.New(invalidLifecycleListMessage)
	}
	found := false
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			if found || expected == "absent" || len(fields) < 2 || !strings.HasPrefix(strings.Join(fields[1:], " ")+" ", expected+" ") {
				return errors.New(unexpectedLifecycleStateMessage)
			}
			found = true
		}
	}
	if found != (expected != "absent") {
		return errors.New(unexpectedLifecycleStateMessage)
	}
	return nil
}

type lifecycleVolume struct {
	Name, CreatedAt, Mountpoint string
}

func readLifecycleVolumes(ctx context.Context, run process.Runner, group, name string) ([]lifecycleVolume, error) {
	base := "sandboxed-agents." + group + "." + name
	names := []string{base + ".home", base + ".ssh", base + ".workspace"}
	var records []struct {
		lifecycleVolume
		Labels map[string]string
	}
	if err := liveJSON(ctx, run, append([]string{"volume", "inspect"}, names...), &records); err != nil {
		return nil, err
	}
	if len(records) != 3 {
		return nil, errors.New(invalidVolumeInventoryMessage)
	}
	volumes := make([]lifecycleVolume, 0, 3)
	for _, record := range records {
		if !slices.Contains(names, record.Name) || record.CreatedAt == "" || record.Mountpoint == "" || record.Labels[sandbox.OwnerLabel] != group {
			return nil, errors.New(invalidVolumeInventoryMessage)
		}
		volumes = append(volumes, record.lifecycleVolume)
	}
	slices.SortFunc(volumes, func(a, b lifecycleVolume) int { return strings.Compare(a.Name, b.Name) })
	for i := range volumes {
		if volumes[i].Name != names[i] {
			return nil, errors.New(invalidVolumeInventoryMessage)
		}
	}
	return volumes, nil
}

func newLivePodman(ctx context.Context, config Config, run process.Runner) (process.Runner, string, error) {
	if config.Host.OS == "linux" {
		if os.Getenv("CONTAINER_HOST") != "" || os.Getenv("CONTAINER_CONNECTION") != "" {
			return nil, "", errors.New(localPodmanRequiredMessage)
		}
		var info struct {
			Host struct{ ServiceIsRemote *bool }
		}
		if err := liveJSON(ctx, run, []string{"info", "--format", "json"}, &info); err != nil {
			return nil, "", err
		}
		if info.Host.ServiceIsRemote == nil || *info.Host.ServiceIsRemote {
			return nil, "", errors.New(localPodmanRequiredMessage)
		}
		return run, "", nil
	}
	scrub := func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" {
			environment := request.Env
			if environment == nil {
				environment = os.Environ()
			}
			request.Env = scrubPodmanRemoteEnvironment(environment)
		}
		return run(ctx, request)
	}
	selection, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, err := preflight.SelectWindowsConnection(selection, scrub)
	if err != nil {
		return nil, "", err
	}
	bound := func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" && (len(request.Args) == 0 || request.Args[0] != "machine") {
			request.Args = append([]string{"--connection", connection}, request.Args...)
		}
		return scrub(ctx, request)
	}
	return bound, connection, nil
}

func liveJSON(ctx context.Context, run process.Runner, args []string, value any) error {
	var output bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &output}})
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("%s: exit status %d", args[0], status)
	}
	return json.Unmarshal(output.Bytes(), value)
}

func requireEmptyGroup(ctx context.Context, run process.Runner, group string) error {
	var containers []struct {
		Names  []string
		Labels map[string]string
	}
	if err := liveJSON(ctx, run, []string{"ps", "--all", "--format", "json"}, &containers); err != nil {
		return err
	}
	if containers == nil {
		return errors.New(invalidContainerInventoryMessage)
	}
	for _, container := range containers {
		if len(container.Names) == 0 || slices.Contains(container.Names, "") {
			return errors.New(invalidContainerInventoryMessage)
		}
		if container.Labels[sandbox.OwnerLabel] == group || container.Labels[liveProbeGroupLabel] == group {
			return errors.New(groupNotEmptyMessage)
		}
		for _, name := range container.Names {
			if strings.HasPrefix(name, "sandboxed-agents."+group+".") || strings.HasPrefix(name, "sandboxed-agents-backup."+group+".") || strings.HasPrefix(name, liveProbePrefix+group+".") {
				return errors.New(groupNotEmptyMessage)
			}
		}
	}
	var volumes []struct {
		Name   string
		Labels map[string]string
	}
	if err := liveJSON(ctx, run, []string{"volume", "ls", "--format", "json"}, &volumes); err != nil {
		return err
	}
	if volumes == nil {
		return errors.New(invalidVolumeInventoryMessage)
	}
	for _, volume := range volumes {
		if volume.Name == "" {
			return errors.New(invalidVolumeInventoryMessage)
		}
		if volume.Labels[sandbox.OwnerLabel] == group || strings.HasPrefix(volume.Name, "sandboxed-agents."+group+".") {
			return errors.New(groupNotEmptyMessage)
		}
	}
	return nil
}
