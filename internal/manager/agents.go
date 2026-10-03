package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if len(args) != 2 || args[0] != "enable" {
		return errors.New("usage: sandboxed-agents-manager agents enable AGENT")
	}
	entry, ok := m.options.Catalog.Find(args[1])
	if !ok {
		return fmt.Errorf("unknown agent %q; valid agents: %s", args[1], strings.Join(m.options.Catalog.Names(), ", "))
	}
	identity := m.options.User()
	if identity.UID == 0 {
		code, err := run(ctx, process.Request{Name: ExecutablePath, Args: []string{"agents", "enable", entry.Name}, User: &agentIdentity, Dir: "/", Env: agentEnvironment(m.options.Home), Streams: streams})
		if err != nil {
			return fmt.Errorf("start manager worker as agent: %w", err)
		}
		if code != 0 {
			return fmt.Errorf("manager worker failed with exit status %d", code)
		}
		return nil
	}
	if identity != agentIdentity {
		return errors.New("agents enable must run as root or as UID and GID 1000")
	}
	return m.enable(ctx, entry, streams, run)
}

func agentEnvironment(home string) []string {
	return []string{"HOME=" + home, "USER=agent", "LOGNAME=agent", "SHELL=/bin/bash", "PATH=" + filepath.Join(home, ".local", "bin") + ":/usr/local/bin:/usr/bin:/bin"}
}

type selectedAgent struct {
	Version string `json:"version"`
}

func (m *Manager) enable(ctx context.Context, entry agentcatalog.Entry, streams process.Streams, run process.Runner) error {
	prefix := filepath.Join(m.options.Home, ".local")
	cache := filepath.Join(prefix, "cache", "sandboxed-agents", "npm")
	state := filepath.Join(prefix, "state", "sandboxed-agents")
	if err := os.MkdirAll(state, 0700); err != nil {
		return fmt.Errorf("create agent state: %w", err)
	}
	unlock, err := lockManager(ctx, filepath.Join(state, "manager.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	selectionPath := filepath.Join(state, "selection.json")
	selection := map[string]selectedAgent{}
	content, err := os.ReadFile(selectionPath)
	if err == nil {
		if err := json.Unmarshal(content, &selection); err != nil || selection == nil {
			return errors.New("agent selection is not a valid JSON object")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read agent selection: %w", err)
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
		selection[entry.Name] = selectedAgent{Version: version}
		if err := saveSelection(selectionPath, selection); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(streams.Stdout, "Agent %s is enabled (version %s).\n", entry.Name, version)
	return err
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
func saveSelection(path string, selection map[string]selectedAgent) error {
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
