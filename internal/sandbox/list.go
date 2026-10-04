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
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/images"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

type listObjects struct {
	state      *sandboxObjects
	containers []string
	backups    []string
	volumes    []volume
}

type sandboxState string

const (
	sandboxRunning           sandboxState = "running"
	sandboxStopped           sandboxState = "stopped"
	sandboxVolumesOnly       sandboxState = "volumes only"
	sandboxUpdateInterrupted sandboxState = "update interrupted"
	sandboxOwnerConflict     sandboxState = "owner conflict"
)

type listRow struct {
	name       string
	container  string
	state      sandboxState
	workspace  string
	sshPort    string
	volumes    []string
	toolchains string
	agents     string
}

func List(ctx context.Context, group string, run process.Runner, output io.Writer, catalog agentcatalog.Catalog) error {
	objects, err := collectListObjects(ctx, group, run)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(objects))
	for name := range objects {
		names = append(names, name)
	}
	slices.Sort(names)
	rows := make([]listRow, 0, len(names))
	for _, name := range names {
		row, err := objects[name].inspect(ctx)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	for index := range rows {
		if rows[index].state == sandboxRunning {
			rows[index].agents = objects[rows[index].name].agentsColumn(ctx, rows[index].container, catalog)
		}
	}
	return renderList(output, rows)
}

func collectListObjects(ctx context.Context, group string, run process.Runner) (map[string]*listObjects, error) {
	containers, err := queryContainers(ctx, run)
	if err != nil {
		return nil, err
	}
	var volumes []volumeRecord
	if err := queryPodmanJSON(ctx, run, []string{"volume", "ls", "--format", "json"}, "volume ls", &volumes); err != nil {
		return nil, err
	}
	if volumes == nil {
		return nil, fmt.Errorf("invalid podman volume ls response")
	}
	objects := make(map[string]*listObjects)
	get := func(name string) *listObjects {
		if objects[name] == nil {
			objects[name] = &listObjects{state: newSandboxObjects(name, group, run, process.Streams{})}
		}
		return objects[name]
	}
	for _, container := range containers {
		podmanName := container.Names[0]
		backup := strings.HasPrefix(podmanName, backupPrefix)
		prefix := containerPrefix
		if backup {
			prefix = backupPrefix
		}
		name, relevant := listSandboxName(podmanName, prefix, group, container.Labels)
		if !relevant {
			continue
		}
		if err := ValidateName(name); err != nil {
			return nil, fmt.Errorf("invalid podman ps response: %w", err)
		}
		row := get(name)
		if backup {
			row.backups = append(row.backups, podmanName)
		} else {
			row.containers = append(row.containers, podmanName)
		}
	}
	seen := make(map[string]bool)
	for _, record := range volumes {
		if record.Name == "" || seen[record.Name] {
			return nil, fmt.Errorf("invalid podman volume ls response")
		}
		seen[record.Name] = true
		base, target, ok := splitSandboxVolume(record.Name)
		if !ok {
			continue
		}
		name, relevant := strings.CutPrefix(base, containerPrefix+group+".")
		if !relevant {
			continue
		}
		if err := ValidateName(name); err != nil {
			return nil, fmt.Errorf("invalid podman volume ls response: %w", err)
		}
		row := get(name)
		row.volumes = append(row.volumes, volume{name: record.Name, target: target})
	}
	return objects, nil
}

func renderList(output io.Writer, rows []listRow) error {
	var buffer bytes.Buffer
	writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSTATE\tWORKSPACE\tSSH PORT\tTOOLCHAINS\tAGENTS\tVOLUMES")
	for _, row := range rows {
		workspace, existingVolumes := row.workspace, "-"
		port := row.sshPort
		if port == "" {
			port = "-"
		}
		if workspace == "" {
			workspace = "-"
		}
		if len(row.volumes) > 0 {
			existingVolumes = strings.Join(row.volumes, ",")
		}
		selection := row.toolchains
		if selection == "" {
			selection = "-"
		}
		agents := row.agents
		if agents == "" {
			agents = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.name, row.state, workspace, port, selection, agents, existingVolumes)
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
	for _, definition := range volumeDefinitions {
		if base, ok := strings.CutSuffix(name, "."+definition.suffix); ok && strings.HasPrefix(base, containerPrefix) {
			return base, definition.target, true
		}
	}
	return "", "", false
}

func (row *listObjects) inspect(ctx context.Context) (listRow, error) {
	result := listRow{name: row.state.name, state: sandboxVolumesOnly}
	conflict := false
	slices.Sort(row.containers)
	for index, name := range row.containers {
		record, err := row.state.inspectContainer(ctx, name)
		if err != nil {
			return listRow{}, err
		}
		conflict = conflict || !row.state.isOwned(record.Config.Labels[OwnerLabel])
		if index == 0 || name == row.state.container {
			result.container = name
			result.state = sandboxStopped
			if record.State.Running {
				result.state = sandboxRunning
			}
			result.workspace = record.Config.Labels[WorkspaceKindLabel]
			result.toolchains = record.Config.Labels[images.ToolchainsLabel]
			result.sshPort = record.Config.Labels[SSHPortLabel]
		}
	}
	slices.SortFunc(row.volumes, func(a, b volume) int { return strings.Compare(a.name, b.name) })
	for _, object := range row.volumes {
		record, err := row.state.inspectVolume(ctx, object.name)
		if err != nil {
			return listRow{}, err
		}
		conflict = conflict || !row.state.isOwned(record.Labels[OwnerLabel])
		result.volumes = append(result.volumes, object.name)
		if len(row.containers) == 0 && object.target == "/workspace" {
			result.workspace = "volume"
		}
	}
	slices.Sort(row.backups)
	for _, name := range row.backups {
		record, err := row.state.inspectContainer(ctx, name)
		if err != nil {
			return listRow{}, err
		}
		conflict = conflict || !row.state.isOwned(record.Config.Labels[OwnerLabel])
		if len(row.containers) == 0 {
			if result.workspace == "" {
				result.workspace = record.Config.Labels[WorkspaceKindLabel]
			}
			result.toolchains = record.Config.Labels[images.ToolchainsLabel]
			result.sshPort = record.Config.Labels[SSHPortLabel]
		}
		result.state = sandboxUpdateInterrupted
	}
	if conflict {
		result.state = sandboxOwnerConflict
	}
	return result, nil
}

func (row *listObjects) agentsColumn(ctx context.Context, container string, catalog agentcatalog.Catalog) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	args := managerArgs(container, "agents", "list")
	status, err := row.state.run(ctx, process.Request{Name: "podman", Args: args, Streams: process.Streams{Stdout: &output}})
	if err != nil || status != 0 || ctx.Err() != nil {
		return ""
	}
	var agents []string
	if err := json.Unmarshal(output.Bytes(), &agents); err != nil || agents == nil {
		return ""
	}
	for _, agent := range agents {
		if _, ok := catalog.Find(agent); !ok {
			return ""
		}
	}
	slices.Sort(agents)
	agents = slices.Compact(agents)
	if len(agents) == 0 {
		return "none"
	}
	return strings.Join(agents, ",")
}
