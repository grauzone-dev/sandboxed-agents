package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/grauzone-dev/sandboxed-agents/internal/buildenv"
	"github.com/grauzone-dev/sandboxed-agents/internal/release"
)

var artifactNames = [...]string{release.LinuxExecutable, release.WindowsExecutable, release.ChecksumFilename}

var previewTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.[0-9]{8}\.(0|[1-9][0-9]*)$`)

func main() {
	if err := buildPreview(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildPreview() error {
	tag := flag.String("tag", "", "preview tag vX.Y.Z-preview.YYYYMMDD.N, also the version the binaries report")
	output := flag.String("output", "", "directory for the release files; must be absent or empty")
	checkTag := flag.Bool("check-tag", false, "only validate -tag; build and write nothing")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flag.Args(), " "))
	}
	if !previewTag.MatchString(*tag) {
		return fmt.Errorf("invalid preview tag %q: want vX.Y.Z-preview.YYYYMMDD.N", *tag)
	}
	if *checkTag {
		return nil
	}
	if *output == "" {
		return errors.New("-output is required unless -check-tag is set")
	}
	if err := checkOutput(*output); err != nil {
		return err
	}
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("run the release tool from the repository root: %w", err)
	}
	work, err := os.MkdirTemp("", "sandboxed-agents-release-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	var bundle []byte
	for _, pass := range []string{"first", "second"} {
		dir := filepath.Join(work, pass)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		for _, target := range []struct{ goos, name string }{
			{"linux", release.LinuxExecutable},
			{"windows", release.WindowsExecutable},
		} {
			binaryPath := filepath.Join(dir, target.name)
			command := exec.Command("go", "run", "./tools/build", "-goos", target.goos, "-output", binaryPath, "-version", *tag)
			command.Env = buildenv.ForTarget(runtime.GOOS, runtime.GOARCH)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			if err := command.Run(); err != nil {
				return fmt.Errorf("%s build for %s: %w", pass, target.goos, err)
			}
			assets, err := os.ReadFile(filepath.Join("internal", "assets", "bundle.zip"))
			if err != nil {
				return err
			}
			if bundle == nil {
				bundle = assets
			} else if !bytes.Equal(bundle, assets) {
				return errors.New("embedded build assets differ between builds")
			}
			binary, err := os.ReadFile(binaryPath)
			if err != nil {
				return err
			}
			if !bytes.Contains(binary, bundle) {
				return fmt.Errorf("%s does not embed the expected build assets", target.name)
			}
			if runtime.GOARCH == "amd64" && runtime.GOOS == target.goos {
				version, err := exec.Command(binaryPath, "version").CombinedOutput()
				want := fmt.Sprintf("sandboxed-agents %s\nassets %x\n", *tag, sha256.Sum256(bundle))
				if err != nil {
					return fmt.Errorf("%s version command failed: %w, output %q", target.name, err, version)
				}
				if string(version) != want {
					return fmt.Errorf("%s version output mismatch: got %q, want %q", target.name, version, want)
				}
			}
		}
		checksums, err := release.Checksums(dir)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, release.ChecksumFilename), checksums, 0644); err != nil {
			return err
		}
	}
	for _, name := range artifactNames {
		first, err := os.ReadFile(filepath.Join(work, "first", name))
		if err != nil {
			return err
		}
		second, err := os.ReadFile(filepath.Join(work, "second", name))
		if err != nil {
			return err
		}
		if !bytes.Equal(first, second) {
			return fmt.Errorf("repeated builds differ: %s", name)
		}
	}
	return writeArtifacts(filepath.Join(work, "first"), *output)
}

func checkOutput(output string) error {
	entry, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !entry.IsDir() {
		return fmt.Errorf("output is not a directory: %s", output)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("output directory is not empty: %s", output)
	}
	return nil
}

func writeArtifacts(source, output string) error {
	if err := checkOutput(output); err != nil {
		return err
	}
	parent := filepath.Dir(filepath.Clean(output))
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".sandboxed-agents-release-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	for _, name := range artifactNames {
		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		mode := os.FileMode(0755)
		if name == release.ChecksumFilename {
			mode = 0644
		}
		if err := os.WriteFile(filepath.Join(stage, name), contents, mode); err != nil {
			return err
		}
	}
	if err := checkOutput(output); err != nil {
		return err
	}
	if err := os.Remove(output); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(stage, output)
}
