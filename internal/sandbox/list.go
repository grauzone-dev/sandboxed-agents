package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type listContainer struct {
	Names  []string
	Labels map[string]string
}

type listObjects struct {
	state      *sandboxObjects
	containers []string
	backups    []string
	volumes    []volume
}

func List(ctx context.Context, group string, run process.Runner, output io.Writer) error {
	var containers []listContainer
	if err := listInventory(ctx, run, []string{"ps", "--all", "--format", "json"}, &containers); err != nil {
		return err
	}
	if containers == nil {
		return fmt.Errorf("invalid podman ps response")
	}
	var volumes []volumeRecord
	if err := listInventory(ctx, run, []string{"volume", "ls", "--format", "json"}, &volumes); err != nil {
		return err
	}
	if volumes == nil {
		return fmt.Errorf("invalid podman volume ls response")
	}
	objects := make(map[string]*listObjects)
	get := func(name string) *listObjects {
		if objects[name] == nil {
			objects[name] = &listObjects{state: newSandboxObjects(name, group, run, process.Streams{})}
		}
		return objects[name]
	}
	seen := make(map[string]bool)
	for _, container := range containers {
		if len(container.Names) != 1 || container.Names[0] == "" || seen[container.Names[0]] {
			return fmt.Errorf("invalid podman ps response")
		}
		podmanName := container.Names[0]
		seen[podmanName] = true
		backup := strings.HasPrefix(podmanName, "sandboxed-agents-backup.")
		prefix := "sandboxed-agents."
		if backup {
			prefix = "sandboxed-agents-backup."
		}
		name, relevant := listSandboxName(podmanName, prefix, group, container.Labels)
		if !relevant {
			continue
		}
		if err := ValidateName(name); err != nil {
			return fmt.Errorf("invalid podman ps response: %w", err)
		}
		row := get(name)
		if backup {
			row.backups = append(row.backups, podmanName)
		} else {
			row.containers = append(row.containers, podmanName)
		}
	}
	clear(seen)
	for _, record := range volumes {
		if record.Name == "" || seen[record.Name] {
			return fmt.Errorf("invalid podman volume ls response")
		}
		seen[record.Name] = true
		base, suffix, ok := splitSandboxVolume(record.Name)
		if !ok {
			continue
		}
		name, relevant := listSandboxName(base, "sandboxed-agents.", group, record.Labels)
		if !relevant {
			continue
		}
		if err := ValidateName(name); err != nil {
			return fmt.Errorf("invalid podman volume ls response: %w", err)
		}
		get(name).volumes = append(get(name).volumes, volume{name: record.Name, target: suffix})
	}
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	slices.Sort(names)
	var buffer bytes.Buffer
	writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSTATE\tWORKSPACE\tPORT\tTOOLCHAINS\tAGENTS\tVOLUMES")
	for _, name := range names {
		row := objects[name]
		state, workspace, existingVolumes, err := row.inspect(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t-\t-\t-\t%s\n", name, state, workspace, existingVolumes)
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	_, err := io.Copy(output, &buffer)
	return err
}

func listSandboxName(podmanName, prefix, group string, labels map[string]string) (string, bool) {
	if name, ok := strings.CutPrefix(podmanName, prefix+group+"."); ok {
		return name, true
	}
	if labels[OwnerLabel] != group {
		return "", false
	}
	if name := labels[NameLabel]; name != "" {
		return name, true
	}
	if rest, ok := strings.CutPrefix(podmanName, prefix); ok {
		_, name, found := strings.Cut(rest, ".")
		return name, found
	}
	return podmanName, true
}

func splitSandboxVolume(name string) (string, string, bool) {
	for _, suffix := range []string{"workspace", "home", "ssh"} {
		if base, ok := strings.CutSuffix(name, "."+suffix); ok && strings.HasPrefix(base, "sandboxed-agents.") {
			return base, suffix, true
		}
	}
	return "", "", false
}

func (row *listObjects) inspect(ctx context.Context) (string, string, string, error) {
	state, workspace := "volumes only", "-"
	conflict := false
	slices.Sort(row.containers)
	for _, name := range row.containers {
		record, err := row.state.inspectContainer(ctx, name)
		if err != nil {
			return "", "", "", err
		}
		conflict = conflict || !row.state.isOwned(record.Config.Labels[OwnerLabel])
		if state == "volumes only" || name == row.state.container {
			state = "stopped"
			if record.State.Running {
				state = "running"
			}
			workspace = record.Config.Labels[WorkspaceKindLabel]
			if workspace == "" {
				workspace = "-"
			}
		}
	}
	slices.SortFunc(row.volumes, func(a, b volume) int { return strings.Compare(a.name, b.name) })
	var volumeNames []string
	for _, object := range row.volumes {
		record, err := row.state.inspectVolume(ctx, object.name)
		if err != nil {
			return "", "", "", err
		}
		conflict = conflict || !row.state.isOwned(record.Labels[OwnerLabel])
		volumeNames = append(volumeNames, object.name)
		if len(row.containers) == 0 && object.target == "workspace" {
			workspace = "volume"
		}
	}
	slices.Sort(row.backups)
	for _, name := range row.backups {
		record, err := row.state.inspectContainer(ctx, name)
		if err != nil {
			return "", "", "", err
		}
		conflict = conflict || !row.state.isOwned(record.Config.Labels[OwnerLabel])
		if len(row.containers) == 0 && workspace == "-" {
			if kind := record.Config.Labels[WorkspaceKindLabel]; kind != "" {
				workspace = kind
			}
		}
		state = "update interrupted"
	}
	if conflict {
		state = "owner conflict"
	}
	volumes := "-"
	if len(volumeNames) > 0 {
		volumes = strings.Join(volumeNames, ",")
	}
	return state, workspace, volumes, nil
}

func listInventory(ctx context.Context, run process.Runner, args []string, records any) error {
	var output, diagnostic bytes.Buffer
	status, err := run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &output, Stderr: &diagnostic}})
	operation := strings.Join(args[:len(args)-2], " ")
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
