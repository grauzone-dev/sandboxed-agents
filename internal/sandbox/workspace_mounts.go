package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

type WorkspaceHost struct {
	OS       string
	ReadFile func(string) ([]byte, error)
}

type hostMount struct {
	device string
	root   string
	target string
}

func (host WorkspaceHost) mounts() ([]hostMount, error) {
	readFile := host.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	data, err := readFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var mounts []hostMount
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !strings.Contains(line, " - ") {
			return nil, os.ErrInvalid
		}
		root, target := unescape.Replace(fields[3]), unescape.Replace(fields[4])
		if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
			return nil, os.ErrInvalid
		}
		mounts = append(mounts, hostMount{device: fields[2], root: root, target: target})
	}
	return mounts, nil
}

func mountedHostPath(path string, mounts []hostMount) (hostMount, string, error) {
	var selected hostMount
	for _, mount := range mounts {
		if pathContains(mount.target, path) && len(mount.target) >= len(selected.target) {
			selected = mount
		}
	}
	if selected.target == "" {
		return hostMount{}, "", os.ErrInvalid
	}
	relative, err := filepath.Rel(selected.target, path)
	if err != nil {
		return hostMount{}, "", err
	}
	return selected, filepath.Join(selected.root, relative), nil
}

func mountedPathsOverlap(workspace, protected string, mounts []hostMount) (bool, error) {
	protectedMount, protectedPath, err := mountedHostPath(protected, mounts)
	if err != nil {
		return false, err
	}
	sources := []string{workspace}
	for _, mount := range mounts {
		if pathContains(workspace, mount.target) {
			sources = append(sources, mount.target)
		}
	}
	for _, source := range sources {
		mount, path, err := mountedHostPath(source, mounts)
		if err != nil {
			return false, err
		}
		if mount.device == protectedMount.device && (pathContains(path, protectedPath) || pathContains(protectedPath, path)) {
			return true, nil
		}
	}
	return false, nil
}
