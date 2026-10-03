package sandbox

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/controllergroup"
)

func (up *Up) SetWorkspace(hostOS, workspace string) error {
	if workspace == "" {
		return nil
	}
	if hostOS != "linux" {
		return errors.New(workspaceWindowsError)
	}
	path := workspace
	if !filepath.IsAbs(path) {
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		path = directory + string(filepath.Separator) + path
	}
	resolved, err := resolveHostPath(path)
	if err != nil {
		return fmt.Errorf(workspaceAliasError, path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf(workspacePathError, resolved, err)
	}
	if !info.IsDir() {
		return fmt.Errorf(workspaceDirectoryError, resolved)
	}
	paths, err := up.protectedHostPaths()
	if err != nil {
		return err
	}
	for _, path := range paths {
		protected, err := resolveHostPath(path)
		if err != nil {
			return fmt.Errorf(workspaceAliasError, path, err)
		}
		if pathContains(resolved, protected) || pathContains(protected, resolved) {
			return fmt.Errorf(workspaceProtectedError, resolved, protected)
		}
	}
	up.workspace = resolved
	return nil
}

func (up *Up) protectedHostPaths() ([]string, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	state, err := controllergroup.StateDirectory("linux", up.group)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return []string{executable, state, filepath.Dir(state), os.TempDir(), "/tmp", filepath.Join(home, ".ssh")}, nil
}

func resolveHostPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		directory, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = directory + string(filepath.Separator) + path
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
			if !os.IsNotExist(err) {
				return "", err
			}
			resolved = candidate
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = candidate
			continue
		}
		links++
		if links > 255 {
			return "", os.ErrInvalid
		}
		target, err := os.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved = filepath.VolumeName(target) + string(filepath.Separator)
			target = strings.TrimPrefix(target, resolved)
		}
		parts = append(strings.Split(target, string(filepath.Separator)), parts...)
	}
	return resolved, nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (up *Up) checkWorkspace() error {
	if up.workspace == "" {
		return nil
	}
	recorded := up.volumes[0].name
	if up.containerLabels[WorkspaceKindLabel] == "bind" {
		recorded = "bind"
		for _, mount := range up.containerMounts {
			if mount.Destination == "/workspace" && mount.Type == "bind" {
				recorded = mount.Source
				resolved, err := resolveHostPath(recorded)
				if err != nil {
					return fmt.Errorf(workspaceAliasError, recorded, err)
				}
				if resolved == up.workspace {
					return nil
				}
				break
			}
		}
	}
	return fmt.Errorf(workspaceConflictError, recorded, up.workspace, up.name, up.name)
}

func (up *Up) workspaceMount() string {
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	_ = writer.Write([]string{"type=bind", "source=" + up.workspace, "target=/workspace"})
	writer.Flush()
	return strings.TrimSuffix(output.String(), "\n")
}
