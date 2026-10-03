package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const (
	OwnerLabel         = "io.github.sandboxed-agents.owner"
	NameLabel          = "io.github.sandboxed-agents.sandbox-name"
	WorkspaceKindLabel = "io.github.sandboxed-agents.workspace-kind"
	defaultGroup       = "default"
)

type volume struct {
	name   string
	target string
	exists bool
	owner  string
}

type Up struct {
	name         string
	container    string
	backup       string
	volumes      []volume
	assetHash    string
	run          process.Runner
	streams      process.Streams
	exists       bool
	backupExists bool
	owner        string
	running      bool
}

func NewUp(name, assetHash string, run process.Runner, streams process.Streams) *Up {
	container := "sandboxed-agents." + defaultGroup + "." + name
	return &Up{
		name: name, container: container, backup: "sandboxed-agents-backup." + defaultGroup + "." + name,
		volumes: []volume{
			{name: container + ".workspace", target: "/workspace"},
			{name: container + ".home", target: "/home/agent"},
			{name: container + ".ssh", target: "/etc/ssh"},
		},
		assetHash: assetHash, run: run, streams: streams,
	}
}

func (up *Up) Read(ctx context.Context) error {
	var err error
	up.exists, err = up.objectExists(ctx, "container", up.container)
	if err != nil {
		return err
	}
	if up.exists {
		record, err := up.inspectContainer(ctx, up.container)
		if err != nil {
			return err
		}
		up.owner = record.Config.Labels[OwnerLabel]
		up.running = record.State.Running
	}
	for index := range up.volumes {
		up.volumes[index].exists, err = up.objectExists(ctx, "volume", up.volumes[index].name)
		if err != nil {
			return err
		}
		if up.volumes[index].exists {
			var records []struct {
				Name   string
				Labels map[string]string
			}
			if err := up.inspect(ctx, "volume", up.volumes[index].name, &records); err != nil {
				return err
			}
			if len(records) != 1 || records[0].Name != up.volumes[index].name {
				return fmt.Errorf("invalid podman volume inspect response for %s", up.volumes[index].name)
			}
			up.volumes[index].owner = records[0].Labels[OwnerLabel]
		}
	}
	if !up.exists {
		return up.checkSandboxOwner()
	}
	return nil
}

func (up *Up) checkSandboxOwner() error {
	var conflicts []string
	if up.exists && up.owner != defaultGroup {
		conflicts = append(conflicts, up.container)
	}
	for _, volume := range up.volumes {
		if volume.exists && volume.owner != defaultGroup {
			conflicts = append(conflicts, volume.name)
		}
	}
	if len(conflicts) > 0 {
		return ownerConflict(conflicts)
	}
	return nil
}

func (up *Up) CheckOwner(ctx context.Context) error {
	if err := up.checkSandboxOwner(); err != nil {
		return err
	}
	var err error
	up.backupExists, err = up.objectExists(ctx, "container", up.backup)
	if err != nil {
		return err
	}
	if up.backupExists {
		backup, err := up.inspectContainer(ctx, up.backup)
		if err != nil {
			return err
		}
		if backup.Config.Labels[OwnerLabel] != defaultGroup {
			return ownerConflict([]string{up.backup})
		}
	}
	return nil
}

func ownerConflict(names []string) error {
	return fmt.Errorf("owner conflict on %s: the %s label is missing or names another controller group; remove or rename each foreign object with Podman", strings.Join(names, ", "), OwnerLabel)
}

func (up *Up) CheckInterruptedUpdate() error {
	if up.backupExists {
		return fmt.Errorf("backup container %s remains from an interrupted update; run sandboxed-agents update %s", up.backup, up.name)
	}
	return nil
}

func (up *Up) Apply(ctx context.Context) error {
	if up.exists {
		if !up.running {
			if err := up.command(ctx, "start", up.container); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(up.streams.Stdout, "Sandbox %s is running.\n", up.name)
		return err
	}
	image := images.BaseTag(up.assetHash)
	exists, err := up.objectExists(ctx, "image", image)
	if err != nil {
		return err
	}
	if !exists {
		if err := images.BuildBase(ctx, up.assetHash, up.run, up.streams); err != nil {
			return err
		}
	}
	for _, volume := range up.volumes {
		verb := "Adopted"
		if !volume.exists {
			if err := up.command(ctx, "volume", "create", "--label", OwnerLabel+"="+defaultGroup, volume.name); err != nil {
				return err
			}
			verb = "Created"
		}
		if _, err := fmt.Fprintf(up.streams.Stdout, "%s volume %s.\n", verb, volume.name); err != nil {
			return err
		}
	}

	args := []string{
		"create", "--name", up.container,
		"--label", OwnerLabel + "=" + defaultGroup,
		"--label", NameLabel + "=" + up.name,
		"--label", WorkspaceKindLabel + "=volume",
		"--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw",
		"--memory=8g", "--cpus=4", "--pids-limit=2048", "--shm-size=1g",
	}
	for _, volume := range up.volumes {
		args = append(args, "--mount", "type=volume,source="+volume.name+",target="+volume.target)
	}
	args = append(args, image)
	if err := up.command(ctx, args...); err != nil {
		return err
	}
	if err := up.command(ctx, "start", up.container); err != nil {
		return err
	}
	_, err = fmt.Fprintf(up.streams.Stdout, "Sandbox %s is running.\n", up.name)
	return err
}

func (up *Up) objectExists(ctx context.Context, kind, name string) (bool, error) {
	var diagnostic bytes.Buffer
	status, err := up.run(ctx, process.Request{Name: "podman", Args: []string{kind, "exists", name}, Streams: process.Streams{Stderr: &diagnostic}})
	if err != nil {
		return false, fmt.Errorf("start podman %s exists %s: %w", kind, name, err)
	}
	switch status {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("podman %s exists %s failed with exit status %d: %s", kind, name, status, diagnostic.String())
	}
}

func (up *Up) command(ctx context.Context, args ...string) error {
	status, err := up.run(ctx, process.Request{Name: "podman", Args: args, Streams: up.streams})
	if err != nil {
		return fmt.Errorf("start podman %s: %w", args[0], err)
	}
	if status != 0 {
		return fmt.Errorf("podman %s failed with exit status %d", args[0], status)
	}
	return nil
}

type containerRecord struct {
	Name   string
	Config *struct{ Labels map[string]string }
	State  *struct{ Running bool }
}

func (up *Up) inspectContainer(ctx context.Context, name string) (containerRecord, error) {
	var records []containerRecord
	if err := up.inspect(ctx, "container", name, &records); err != nil {
		return containerRecord{}, err
	}
	if len(records) != 1 || records[0].Name != name || records[0].Config == nil || records[0].State == nil {
		return containerRecord{}, fmt.Errorf("invalid podman container inspect response for %s", name)
	}
	return records[0], nil
}

func (up *Up) inspect(ctx context.Context, kind, name string, record any) error {
	var output, diagnostic bytes.Buffer
	status, err := up.run(ctx, process.Request{Name: "podman", Args: []string{kind, "inspect", name}, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err != nil {
		return fmt.Errorf("start podman %s inspect %s: %w", kind, name, err)
	}
	if status != 0 {
		return fmt.Errorf("podman %s inspect %s failed with exit status %d: %s", kind, name, status, diagnostic.String())
	}
	if err := json.Unmarshal(output.Bytes(), record); err != nil {
		return fmt.Errorf("decode podman %s inspect %s: %w", kind, name, err)
	}
	return nil
}
