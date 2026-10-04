package sandbox

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
)

func (up *Up) BindWorkspace(host WorkspaceHost, workspace string) error {
	if workspace == "" {
		return nil
	}
	if host.OS != "linux" && host.OS != "windows" {
		return fmt.Errorf(workspaceUnsupportedError, host.OS)
	}
	path, err := absoluteHostPath(workspace)
	if err != nil {
		return err
	}
	resolved, err := resolveHostPath(path)
	if err != nil {
		return fmt.Errorf(workspaceAliasError, resolved, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf(workspacePathError, resolved, err)
	}
	if !info.IsDir() {
		return fmt.Errorf(workspaceDirectoryError, resolved)
	}
	if host.OS == "windows" {
		if err := validateHostWorkspacePath(resolved); err != nil {
			return err
		}
	}
	paths, err := protectedHostPaths(host.OS, up.group)
	if err != nil {
		return err
	}
	var mounts []hostMount
	if host.OS == "linux" {
		mounts, err = host.mounts()
		if err != nil {
			return fmt.Errorf(workspaceAliasError, "/proc/self/mountinfo", err)
		}
	}
	var protectedPaths []string
	for _, path := range paths {
		protected, err := resolveHostPath(path)
		if err != nil {
			return fmt.Errorf(workspaceAliasError, path, err)
		}
		protectedPaths = append(protectedPaths, protected)
		var overlap bool
		if host.OS == "linux" {
			overlap, err = mountedPathsOverlap(resolved, protected, mounts)
		}
		if err == nil && !overlap {
			overlap, err = hostPathsOverlap(resolved, protected)
		}
		if err != nil {
			return fmt.Errorf(workspaceAliasError, protected, err)
		}
		if overlap {
			return fmt.Errorf(workspaceProtectedError, resolved, protected)
		}
	}
	if host.OS == "windows" {
		if err := checkNestedWorkspaceAliases(resolved, protectedPaths); err != nil {
			return err
		}
	}
	up.workspaceSource = resolved
	up.workspaceHost = resolved
	up.workspaceOS = host.OS
	return nil
}

func protectedHostPaths(hostOS, group string) ([]string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	state, err := controllergroup.StateDirectory(hostOS, group)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	paths := []string{executable, state, filepath.Dir(state), os.TempDir(), filepath.Join(home, ".ssh")}
	if hostOS == "linux" {
		paths = append(paths, "/tmp")
	}
	return paths, nil
}

func absoluteHostPath(path string) (string, error) {
	if runtime.GOOS == "windows" {
		return filepath.Abs(path)
	}
	if !filepath.IsAbs(path) {
		directory, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = directory + string(filepath.Separator) + path
	}
	return path, nil
}

func resolveHostPath(path string) (string, error) {
	path, err := absoluteHostPath(path)
	if err != nil {
		return path, err
	}
	root := filepath.VolumeName(path) + string(filepath.Separator)
	resolved := root
	parts := strings.Split(strings.TrimPrefix(path, root), string(filepath.Separator))
	links := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, part)
		info, err := os.Lstat(candidate)
		if err != nil {
			if !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
				return filepath.Join(append([]string{candidate}, parts...)...), err
			}
			resolved = candidate
			continue
		}
		if !hostPathAlias(info) {
			resolved = candidate
			continue
		}
		links++
		if links > 255 {
			return candidate, os.ErrInvalid
		}
		target, err := os.Readlink(candidate)
		if err != nil {
			return candidate, err
		}
		if filepath.IsAbs(target) {
			resolved = filepath.VolumeName(target) + string(filepath.Separator)
			target = strings.TrimPrefix(target, resolved)
		}
		parts = append(strings.Split(target, string(filepath.Separator)), parts...)
	}
	return normalizeHostPath(resolved)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (up *Up) checkWorkspace() error {
	if up.workspaceSource == "" {
		return nil
	}
	recorded := "volume"
	for _, volume := range up.volumes {
		if volume.target == "/workspace" {
			recorded = volume.name
			break
		}
	}
	if up.containerLabels[WorkspaceKindLabel] == "bind" {
		recorded = "bind"
		for _, mount := range up.containerMounts {
			if mount.Destination == "/workspace" && mount.Type == "bind" {
				recorded = mount.Source
				if up.workspaceOS == "windows" {
					translated, ok := windowsWorkspacePath(recorded)
					if !ok {
						break
					}
					recorded = translated
				}
				resolved, err := resolveHostPath(recorded)
				if err != nil {
					return fmt.Errorf(workspaceAliasError, recorded, err)
				}
				if resolved == up.workspaceHost {
					return nil
				}
				break
			}
		}
	}
	return fmt.Errorf(workspaceConflictError, recorded, up.workspaceHost, up.name, up.name)
}

func (up *Up) workspaceMount() string {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	// The CSV readers of Go and Podman drop one carriage return before a line feed, so doubling it keeps the validated path intact.
	source := strings.ReplaceAll(up.workspaceSource, "\r\n", "\r\r\n")
	_ = writer.Write([]string{"type=bind", "source=" + source, "target=/workspace"})
	writer.Flush()
	return strings.TrimSuffix(output.String(), "\n")
}

func hostPathsOverlap(workspace, protected string) (bool, error) {
	if pathContains(workspace, protected) || pathContains(protected, workspace) {
		return true, nil
	}
	for _, pair := range [][2]string{{workspace, protected}, {protected, workspace}} {
		info, err := os.Stat(pair[0])
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		for path := pair[1]; ; path = filepath.Dir(path) {
			candidate, err := os.Stat(path)
			if err == nil && os.SameFile(info, candidate) {
				return true, nil
			}
			if err != nil && !os.IsNotExist(err) {
				return false, err
			}
			if filepath.Dir(path) == path {
				break
			}
		}
	}
	info, err := os.Stat(protected)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	aliases, err := fileHasAliases(protected, info)
	if err != nil {
		return false, err
	}
	if info.IsDir() || !aliases {
		return false, nil
	}
	overlap := false
	err = filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		candidate, err := entry.Info()
		if err != nil {
			return err
		}
		if os.SameFile(info, candidate) {
			overlap = true
			return filepath.SkipAll
		}
		return nil
	})
	return overlap, err
}
