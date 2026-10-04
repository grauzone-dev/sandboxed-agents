package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

// ExecutablePath is the fixed path of the manager in the image. The host's control calls and the
// manager running as root start only this program, never one from the home volume or the workspace.
const ExecutablePath = "/usr/local/bin/sandboxed-agents-manager"

// agentIdentity is the user agent, which runs the worker and every installation command.
var agentIdentity = process.Identity{UID: 1000, GID: 1000}

func (m *Manager) agents(ctx context.Context, args []string, streams process.Streams, run process.Runner) error {
	var apply func() error
	switch {
	case len(args) > 0 && args[0] == "login":
		return m.login(ctx, args[1:], streams, run)
	case len(args) > 0 && args[0] == "check-enabled":
		return m.checkEnabled(args[1:], streams)
	case len(args) > 0 && args[0] == "run":
		return m.runAgent(ctx, args[1:], streams, run)
	case len(args) == 1 && args[0] == "list":
		apply = func() error { return m.listAgents(streams.Stdout) }
	case len(args) == 2 && (args[0] == "enable" || args[0] == "disable" || args[0] == "status"):
		entry, err := m.catalogEntry(args[1])
		if err != nil {
			return err
		}
		if args[0] == "status" {
			apply = func() error { return m.agentStatus(ctx, entry, streams, run) }
		} else if args[0] == "disable" {
			apply = func() error { return m.disable(ctx, entry, streams) }
		} else {
			apply = func() error { return m.enable(ctx, entry, streams, run) }
		}
	default:
		return errors.New(agentUsageMessage)
	}
	identity := m.options.User()
	if identity.UID == 0 {
		code, err := run(ctx, process.Request{Name: ExecutablePath, Args: append([]string{"agents"}, args...), User: &agentIdentity, Dir: "/", Env: agentEnvironment(m.options.Home), Streams: streams})
		if err != nil {
			return fmt.Errorf("start manager worker as agent: %w", err)
		}
		if code != 0 {
			return fmt.Errorf("manager worker failed with exit status %d", code)
		}
		return nil
	}
	if identity != agentIdentity {
		return errors.New(agentIdentityMessage)
	}
	return apply()
}

func (m *Manager) catalogEntry(name string) (agentcatalog.Entry, error) {
	entry, ok := m.options.Catalog.Find(name)
	if !ok {
		return entry, fmt.Errorf(agentUnknownFormat, name, strings.Join(m.options.Catalog.Names(), ", "))
	}
	return entry, nil
}

func (m *Manager) selectionPath() string {
	return filepath.Join(m.options.Home, ".local", "state", "sandboxed-agents", "selection.json")
}

func (m *Manager) listAgents(output io.Writer) error {
	selection, err := readSelection(m.selectionPath())
	if err != nil {
		return err
	}
	names := make([]string, 0, len(selection))
	for name := range selection {
		if _, err := m.catalogEntry(name); err != nil {
			return err
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return json.NewEncoder(output).Encode(names)
}

func agentEnvironment(home string) []string {
	return []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}
}

type selectedAgent struct {
	Version string `json:"version"`
	Pin     string `json:"pin,omitempty"`
}

func (m *Manager) enable(ctx context.Context, entry agentcatalog.Entry, streams process.Streams, run process.Runner) error {
	prefix := filepath.Join(m.options.Home, ".local")
	cache := filepath.Join(prefix, "cache", "sandboxed-agents", "npm")
	selectionPath := m.selectionPath()
	state := filepath.Dir(selectionPath)
	if err := os.MkdirAll(state, 0700); err != nil {
		return fmt.Errorf("create agent state: %w", err)
	}
	unlock, err := lockManager(ctx, filepath.Join(state, "manager.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	selection, err := readSelection(selectionPath)
	if err != nil {
		return err
	}
	_, enabled := selection[entry.Name]
	if !enabled {
		code, err := run(ctx, process.Request{Name: "/usr/bin/npm", Args: []string{"install", "--global", "--prefix", prefix, "--cache", cache, entry.Install.Package + "@latest"}, User: &agentIdentity, Dir: m.options.Home, Env: agentEnvironment(m.options.Home), Streams: streams})
		if err != nil {
			return fmt.Errorf("install %s: %w", entry.Name, err)
		}
		if code != 0 {
			return fmt.Errorf("install %s failed with exit status %d", entry.Name, code)
		}
	}
	version, err := installedVersion(m.options.Home, entry.Install.Package)
	if err != nil {
		return err
	}
	if !enabled {
		selection[entry.Name], err = json.Marshal(selectedAgent{Version: version})
		if err != nil {
			return err
		}
		if err := saveSelection(selectionPath, selection); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(streams.Stdout, "Agent %s is enabled (version %s).\n", entry.Name, version)
	return err
}

func (m *Manager) disable(ctx context.Context, entry agentcatalog.Entry, streams process.Streams) error {
	selectionPath := m.selectionPath()
	state := filepath.Dir(selectionPath)
	lockPath := filepath.Join(state, "manager.lock")
	if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) {
		selection, err := readSelection(selectionPath)
		if err != nil {
			return err
		}
		if _, enabled := selection[entry.Name]; !enabled {
			_, err := fmt.Fprintf(streams.Stdout, "Agent %s is not enabled; nothing to do.\n", entry.Name)
			return err
		}
	} else if err != nil {
		return err
	}
	unlock, err := lockManager(ctx, lockPath)
	if err != nil {
		return err
	}
	defer unlock()
	selection, err := readSelection(selectionPath)
	if err != nil {
		return err
	}
	data, enabled := selection[entry.Name]
	if !enabled {
		_, err := fmt.Fprintf(streams.Stdout, "Agent %s is not enabled; nothing to do.\n", entry.Name)
		return err
	}
	var selected selectedAgent
	if err := json.Unmarshal(data, &selected); err != nil {
		return errors.New(agentSelectionInvalid)
	}
	commandPath := filepath.Join(m.options.Home, ".local", "bin", entry.Command)
	if err := os.Remove(commandPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove managed command for %s: %w", entry.Name, err)
	}
	delete(selection, entry.Name)
	if err := saveSelection(selectionPath, selection); err != nil {
		return fmt.Errorf("save agent selection: %w", err)
	}
	if selected.Pin != "" {
		_, err = fmt.Fprintf(streams.Stdout, "Agent %s is disabled (removed pin %s).\n", entry.Name, selected.Pin)
	} else {
		_, err = fmt.Fprintf(streams.Stdout, "Agent %s is disabled.\n", entry.Name)
	}
	return err
}

func readSelection(path string) (map[string]json.RawMessage, error) {
	selection := map[string]json.RawMessage{}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return selection, nil
	}
	if err != nil {
		return nil, fmt.Errorf(agentSelectionReadFormat, err)
	}
	if err := json.Unmarshal(content, &selection); err != nil || selection == nil {
		return nil, errors.New(agentSelectionInvalid)
	}
	for _, data := range selection {
		if err := json.Unmarshal(data, new(selectedAgent)); err != nil {
			return nil, errors.New(agentSelectionInvalid)
		}
	}
	return selection, nil
}

func installedVersion(home, pkg string) (string, error) {
	data, err := os.ReadFile(filepath.Join(home, ".local", "lib", "node_modules", filepath.FromSlash(pkg), "package.json"))
	if err != nil {
		return "", fmt.Errorf("read installed version: %w", err)
	}
	var metadata struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || strings.TrimSpace(metadata.Version) == "" {
		return "", errors.New("invalid installed package version")
	}
	return metadata.Version, nil
}
func saveSelection(path string, selection map[string]json.RawMessage) error {
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "selection-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
