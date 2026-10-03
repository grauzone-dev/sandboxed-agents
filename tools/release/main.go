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

	releaseassets "github.com/grauzone-dev/sandboxed-agents/internal/release"
)

var previewTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-preview\.[0-9]{8}\.(0|[1-9][0-9]*)$`)

func main() {
	if err := release(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func release() error {
	tag := flag.String("tag", "", "preview tag vX.Y.Z-preview.YYYYMMDD.N, also the version the binaries report")
	output := flag.String("output", "", "directory for the release files; must be absent or empty")
	checkTag := flag.Bool("check-tag", false, "only validate -tag; build and write nothing")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected arguments")
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
			{"linux", releaseassets.LinuxExecutable},
			{"windows", releaseassets.WindowsExecutable},
		} {
			binaryPath := filepath.Join(dir, target.name)
			command := exec.Command("go", "run", "./tools/build", "-goos", target.goos, "-output", binaryPath, "-version", *tag)
			command.Env = goEnvironment()
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
				if err != nil || string(version) != want {
					return fmt.Errorf("%s version check failed: %v, output %q", target.name, err, version)
				}
			}
		}
		checksums, err := releaseassets.Checksums(dir)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), checksums, 0644); err != nil {
			return err
		}
	}
	for _, name := range []string{releaseassets.LinuxExecutable, releaseassets.WindowsExecutable, "SHA256SUMS"} {
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
	return publish(filepath.Join(work, "first"), *output)
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

func publish(source, output string) error {
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
	for _, name := range []string{releaseassets.LinuxExecutable, releaseassets.WindowsExecutable, "SHA256SUMS"} {
		contents, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			return err
		}
		mode := os.FileMode(0755)
		if name == "SHA256SUMS" {
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

func goEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+12)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(key) {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOAMD64", "GOPROXY", "GOSUMDB", "GOFLAGS", "GOEXPERIMENT", "GOWORK", "GOTOOLCHAIN", "GO111MODULE":
		default:
			env = append(env, value)
		}
	}
	return append(env, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "GOAMD64=v1", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=", "GOEXPERIMENT=", "GOWORK=off", "GOTOOLCHAIN=local", "GO111MODULE=on")
}
