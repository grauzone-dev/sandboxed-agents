package npmpackage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/release"
)

//go:embed launcher.cjs
var launcher []byte

func Filename(tag string) string {
	return "sandboxed-agents-" + strings.TrimPrefix(tag, "v") + ".tgz"
}

func Write(directory, tag string) error {
	metadata := struct {
		Name       string            `json:"name"`
		Version    string            `json:"version"`
		Bin        map[string]string `json:"bin"`
		Scripts    map[string]string `json:"scripts"`
		Repository map[string]string `json:"repository"`
		License    string            `json:"license"`
	}{
		Name: "sandboxed-agents", Version: strings.TrimPrefix(tag, "v"),
		Bin:        map[string]string{"sandboxed-agents": "launcher.cjs"},
		Scripts:    map[string]string{"postinstall": "node launcher.cjs --verify-install"},
		Repository: map[string]string{"type": "git", "url": "git+https://github.com/grauzone-dev/sandboxed-agents.git"},
		License:    "MIT",
	}
	packageJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	compressed.Header.OS = 255
	writer := tar.NewWriter(compressed)
	for _, name := range []string{"package.json", "launcher.cjs", release.LinuxExecutable, release.WindowsExecutable, release.ChecksumFilename} {
		var data []byte
		switch name {
		case "package.json":
			data = append(packageJSON, '\n')
		case "launcher.cjs":
			data = bytes.ReplaceAll(launcher, []byte("\r\n"), []byte("\n"))
		default:
			data, err = os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				return err
			}
		}
		mode := int64(0644)
		if name == "launcher.cjs" || name == release.LinuxExecutable || name == release.WindowsExecutable {
			mode = 0755
		}
		header := &tar.Header{Name: "package/" + name, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := compressed.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, Filename(tag)), archive.Bytes(), 0644)
}
