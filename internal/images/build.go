package images

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/toolchains"
)

const (
	ManagedLabel    = "io.github.sandboxed-agents.managed"
	AssetHashLabel  = "io.github.sandboxed-agents.asset-hash"
	ToolchainsLabel = "io.github.sandboxed-agents.toolchains"
	BaseImageLabel  = "io.github.sandboxed-agents.base-image"
)

func BaseTag(assetHash string) string {
	return "localhost/sandboxed-agents:base-" + assetHash
}

func Tag(assetHash string, set toolchains.Set) string {
	if set.String() == "" {
		return BaseTag(assetHash)
	}
	return "localhost/sandboxed-agents:toolchains-" + strings.ReplaceAll(set.String(), ",", "-") + "-" + assetHash
}

func BuildBase(ctx context.Context, assetHash string, run process.Runner, streams process.Streams) (result error) {
	directory, err := os.MkdirTemp("", "sandboxed-agents-context-*")
	if err != nil {
		return fmt.Errorf("create build context directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			result = errors.Join(result, fmt.Errorf("remove build context directory %s: %w", directory, err))
		}
	}()
	buildContext, err := assets.Context()
	if err != nil {
		return fmt.Errorf("read embedded build context: %w", err)
	}
	if err := copyBaseContext(directory, buildContext); err != nil {
		return fmt.Errorf("write build context to %s: %w", directory, err)
	}
	manager, err := assets.Manager()
	if err != nil {
		return fmt.Errorf("read embedded manager: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manager"), manager, 0755); err != nil {
		return fmt.Errorf("write manager to build context: %w", err)
	}
	status, err := run(ctx, process.Request{
		Name: "podman",
		Args: []string{
			"build", "--pull=always", "--no-cache",
			"--tag", BaseTag(assetHash),
			"--label", ManagedLabel + "=true",
			"--label", AssetHashLabel + "=" + assetHash,
			"--label", ToolchainsLabel + "=",
			"--file", filepath.Join(directory, "Containerfile"), directory,
		},
		Streams: streams,
	})
	if err != nil {
		return fmt.Errorf("start podman build: %w", err)
	}
	if status != 0 {
		return fmt.Errorf("podman build failed with exit status %d", status)
	}
	return nil
}

func copyBaseContext(directory string, buildContext fs.FS) error {
	return fs.WalkDir(buildContext, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "toolchains" {
			return fs.SkipDir
		}
		if name == "." {
			return nil
		}
		path := filepath.Join(directory, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(path, 0755)
		}
		contents, err := fs.ReadFile(buildContext, name)
		if err != nil {
			return err
		}
		return os.WriteFile(path, contents, 0644)
	})
}
