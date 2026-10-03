package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

const (
	containerPrefix    = "sandboxed-agents."
	backupPrefix       = "sandboxed-agents-backup."
	OwnerLabel         = "io.github.sandboxed-agents.owner"
	NameLabel          = "io.github.sandboxed-agents.sandbox-name"
	WorkspaceKindLabel = "io.github.sandboxed-agents.workspace-kind"
)

var volumeDefinitions = [...]struct{ suffix, target string }{
	{"workspace", "/workspace"},
	{"home", "/home/agent"},
	{"ssh", "/etc/ssh"},
}

type volume struct {
	name   string
	target string
	exists bool
	owner  string
}

type sandboxObjects struct {
	group            string
	name             string
	container        string
	backup           string
	volumes          []volume
	run              process.Runner
	streams          process.Streams
	containerExists  bool
	backupChecked    bool
	backupExists     bool
	backupOwner      string
	containerOwner   string
	containerRunning bool
	containerLabels  map[string]string
}

func newSandboxObjects(name, group string, run process.Runner, streams process.Streams) *sandboxObjects {
	container := containerPrefix + group + "." + name
	volumes := make([]volume, len(volumeDefinitions))
	for i, definition := range volumeDefinitions {
		volumes[i] = volume{name: container + "." + definition.suffix, target: definition.target}
	}
	return &sandboxObjects{
		group: group, name: name, container: container, backup: backupPrefix + group + "." + name,
		volumes: volumes, run: run, streams: streams,
	}
}

func (state *sandboxObjects) CheckSandbox(ctx context.Context) error {
	var err error
	state.containerExists, err = state.objectExists(ctx, "container", state.container)
	if err != nil {
		return err
	}
	if state.containerExists {
		record, err := state.inspectContainer(ctx, state.container)
		if err != nil {
			return err
		}
		state.containerOwner = record.Config.Labels[OwnerLabel]
		state.containerRunning = record.State.Running
		state.containerLabels = record.Config.Labels
	}
	for index := range state.volumes {
		state.volumes[index].exists, err = state.objectExists(ctx, "volume", state.volumes[index].name)
		if err != nil {
			return err
		}
		if state.volumes[index].exists {
			record, err := state.inspectVolume(ctx, state.volumes[index].name)
			if err != nil {
				return err
			}
			state.volumes[index].owner = record.Labels[OwnerLabel]
		}
	}
	if !state.containerExists {
		if err := state.inspectBackup(ctx); err != nil {
			return err
		}
		return state.checkSandboxOwner()
	}
	return nil
}

func (state *sandboxObjects) isOwned(owner string) bool { return owner == state.group }

func (state *sandboxObjects) ownerConflicts() []string {
	var conflicts []string
	if state.containerExists && !state.isOwned(state.containerOwner) {
		conflicts = append(conflicts, state.container)
	}
	for _, volume := range state.volumes {
		if volume.exists && !state.isOwned(volume.owner) {
			conflicts = append(conflicts, volume.name)
		}
	}
	if state.backupExists && !state.isOwned(state.backupOwner) {
		conflicts = append(conflicts, state.backup)
	}
	return conflicts
}

func (state *sandboxObjects) checkSandboxOwner() error {
	if conflicts := state.ownerConflicts(); len(conflicts) > 0 {
		return ownerConflict(conflicts)
	}
	return nil
}

func (state *sandboxObjects) CheckOwner(ctx context.Context) error {
	if err := state.inspectBackup(ctx); err != nil {
		return err
	}
	return state.checkSandboxOwner()
}

func (state *sandboxObjects) inspectBackup(ctx context.Context) error {
	if state.backupChecked {
		return nil
	}
	var err error
	state.backupExists, err = state.objectExists(ctx, "container", state.backup)
	if err != nil {
		return err
	}
	if state.backupExists {
		backup, err := state.inspectContainer(ctx, state.backup)
		if err != nil {
			return err
		}
		state.backupOwner = backup.Config.Labels[OwnerLabel]
	}
	state.backupChecked = true
	return nil
}

func ownerConflict(names []string) error {
	return fmt.Errorf("owner conflict on %s: the %s label is missing or names another controller group; remove or rename each foreign object with Podman", strings.Join(names, ", "), OwnerLabel)
}

func (state *sandboxObjects) CheckInterruptedUpdate() error {
	if state.backupExists {
		return fmt.Errorf("backup container %s remains from an interrupted update; run sandboxed-agents update %s", state.backup, state.name)
	}
	return nil
}

func (state *sandboxObjects) objectExists(ctx context.Context, kind, name string) (bool, error) {
	var diagnostic bytes.Buffer
	status, err := state.run(ctx, process.Request{Name: "podman", Args: []string{kind, "exists", name}, Streams: process.Streams{Stderr: &diagnostic}})
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

func (state *sandboxObjects) runPodman(ctx context.Context, args ...string) error {
	status, err := state.run(ctx, process.Request{Name: "podman", Args: args, Streams: state.streams})
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

func (state *sandboxObjects) inspectContainer(ctx context.Context, name string) (containerRecord, error) {
	var records []containerRecord
	if err := state.inspect(ctx, "container", name, &records); err != nil {
		return containerRecord{}, err
	}
	if len(records) != 1 || records[0].Name != name || records[0].Config == nil || records[0].State == nil {
		return containerRecord{}, fmt.Errorf("invalid podman container inspect response for %s", name)
	}
	return records[0], nil
}

func (state *sandboxObjects) inspect(ctx context.Context, kind, name string, record any) error {
	return queryPodmanJSON(ctx, state.run, []string{kind, "inspect", name}, kind+" inspect "+name, record)
}

func queryPodmanJSON(ctx context.Context, run process.Runner, args []string, operation string, records any) error {
	var output, diagnostic bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	if err != nil {
		return fmt.Errorf("start podman %s: %w", operation, err)
	}
	if status != 0 {
		return fmt.Errorf("podman %s failed with exit status %d: %s", operation, status, diagnostic.String())
	}
	if err := json.Unmarshal(output.Bytes(), records); err != nil {
		return fmt.Errorf("decode podman %s: %w", operation, err)
	}
	return nil
}

type volumeRecord struct {
	Name   string
	Labels map[string]string
}

func (state *sandboxObjects) inspectVolume(ctx context.Context, name string) (volumeRecord, error) {
	var records []volumeRecord
	if err := state.inspect(ctx, "volume", name, &records); err != nil {
		return volumeRecord{}, err
	}
	if len(records) != 1 || records[0].Name != name {
		return volumeRecord{}, fmt.Errorf("invalid podman volume inspect response for %s", name)
	}
	return records[0], nil
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid sandbox name %q; names must match %s", name, namePattern.String())
	}
	return nil
}
