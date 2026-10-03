package main_test

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func releaseTool(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	command := exec.Command("go", "build", "-o", path, "./tools/release")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build release tool: %v\n%s", err, output)
	}
	return path
}

func TestReleaseBuildsReproducibleArtifactsWithTaggedVersion(t *testing.T) {
	tool := releaseTool(t)
	root := filepath.Join("..", "..")
	source := filepath.Join(t.TempDir(), "source")
	testutil.CopySource(t, root, source)
	tag := "v1.0.0-preview.20261003.1"
	outputs := []string{filepath.Join(t.TempDir(), "first"), t.TempDir()}
	var first map[string][]byte
	for _, dir := range outputs {
		command := exec.Command(tool, "-tag", tag, "-output", dir)
		command.Dir = source
		command.Env = append(os.Environ(), "GOFLAGS=-ldflags=-s", "GOEXPERIMENT=not-a-valid-experiment", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("release: %v\n%s", err, output)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		wantNames := []string{"SHA256SUMS", "sandboxed-agents-linux-amd64", "sandboxed-agents-windows-amd64.exe"}
		if len(entries) != len(wantNames) {
			t.Fatalf("release files: %v", entries)
		}
		artifacts := make(map[string][]byte)
		for i, entry := range entries {
			if entry.Name() != wantNames[i] || entry.IsDir() {
				t.Fatalf("release file %d = %s", i, entry.Name())
			}
			contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			artifacts[entry.Name()] = contents
			if first != nil && !bytes.Equal(first[entry.Name()], contents) {
				t.Fatalf("repeated release changed %s", entry.Name())
			}
		}
		first = artifacts
		wantChecksums := fmt.Sprintf("%x  sandboxed-agents-linux-amd64\n%x  sandboxed-agents-windows-amd64.exe\n", sha256.Sum256(artifacts[wantNames[1]]), sha256.Sum256(artifacts[wantNames[2]]))
		if string(artifacts["SHA256SUMS"]) != wantChecksums {
			t.Fatalf("SHA256SUMS does not verify both binaries: %s", artifacts["SHA256SUMS"])
		}
		bundle, err := os.ReadFile(filepath.Join(source, "internal", "assets", "bundle.zip"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range wantNames[1:] {
			if !bytes.Contains(artifacts[name], bundle) {
				t.Fatalf("%s does not contain the shared embedded bundle", name)
			}
		}
		native := "sandboxed-agents-linux-amd64"
		if runtime.GOOS == "windows" {
			native = "sandboxed-agents-windows-amd64.exe"
		}
		if runtime.GOARCH == "amd64" && (runtime.GOOS == "linux" || runtime.GOOS == "windows") {
			output, err := exec.Command(filepath.Join(dir, native), "version").CombinedOutput()
			want := fmt.Sprintf("sandboxed-agents %s\nassets %x\n", tag, sha256.Sum256(bundle))
			if err != nil || string(output) != want {
				t.Fatalf("native version = %q, %v; want %q", output, err, want)
			}
		}
		linux, err := elf.NewFile(bytes.NewReader(artifacts[wantNames[1]]))
		if err != nil {
			t.Fatal(err)
		}
		if linux.Machine != elf.EM_X86_64 {
			t.Fatalf("Linux architecture = %v", linux.Machine)
		}
		for _, program := range linux.Progs {
			if program.Type == elf.PT_INTERP {
				t.Fatal("Linux release requires a dynamic loader")
			}
		}
		linux.Close()
		windows, err := pe.NewFile(bytes.NewReader(artifacts[wantNames[2]]))
		if err != nil {
			t.Fatal(err)
		}
		if windows.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Fatalf("Windows architecture = %v", windows.Machine)
		}
		windows.Close()
	}
}

func TestReleaseChecksPreviewTagsWithoutBuildingOrWriting(t *testing.T) {
	tool := releaseTool(t)
	for _, example := range []struct {
		tag   string
		valid bool
	}{
		{"v1.0.0-preview.20261003.1", true},
		{"v0.0.0-preview.00000000.0", true},
		{"v20.103.4-preview.20260231.100", true},
		{"", false},
		{"v1.0.0", false},
		{"1.0.0-preview.20261003.1", false},
		{"v01.0.0-preview.20261003.1", false},
		{"v1.00.0-preview.20261003.1", false},
		{"v1.0.00-preview.20261003.1", false},
		{"v1.0.0-preview.20261003.01", false},
		{"v1.0.0-preview.2026103.1", false},
		{"v1.0.0-preview.202610033.1", false},
		{"v1.0.0-preview.20261003.-1", false},
		{"v1.0.0-preview.20261003.1+build", false},
		{"v1.0.0-preview.20261003.1\n", false},
		{" v1.0.0-preview.20261003.1", false},
	} {
		t.Run(example.tag, func(t *testing.T) {
			dir := t.TempDir()
			outputDir := filepath.Join(dir, "output")
			command := exec.Command(tool, "-tag", example.tag, "-check-tag", "-output", outputDir)
			command.Dir = dir
			output, err := command.CombinedOutput()
			if (err == nil) != example.valid {
				t.Fatalf("tag %q valid=%v: %v\n%s", example.tag, example.valid, err, output)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("tag check wrote files: %v", entries)
			}
		})
	}
}

func TestReleaseRefusesInvalidTagsAndOccupiedOutputBeforeBuilding(t *testing.T) {
	tool := releaseTool(t)
	for _, example := range []struct {
		tag      string
		occupied bool
	}{
		{"v1.0.0", false},
		{"v1.0.0-preview.20261003.1", true},
	} {
		t.Run(example.tag, func(t *testing.T) {
			dir := t.TempDir()
			outputDir := filepath.Join(dir, "output")
			if example.occupied {
				if err := os.Mkdir(outputDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(outputDir, "keep"), []byte("unrelated"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command(tool, "-tag", example.tag, "-output", outputDir)
			command.Dir = dir
			command.Env = append(os.Environ(), "PATH="+t.TempDir())
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("release succeeded: %s", output)
			}
			refused := example.tag
			if example.occupied {
				refused = outputDir
			}
			if !strings.Contains(string(output), refused) {
				t.Fatalf("release did not name the refused input %q: %s", refused, output)
			}
			if example.occupied {
				contents, err := os.ReadFile(filepath.Join(outputDir, "keep"))
				if err != nil || string(contents) != "unrelated" {
					t.Fatalf("existing output changed: %q, %v", contents, err)
				}
				entries, err := os.ReadDir(outputDir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("existing output files changed: %v, %v", entries, err)
				}
			} else if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
				t.Fatalf("invalid release created output: %v", err)
			}
		})
	}
}

func TestReleasePublishesNothingWhenRepeatedBinariesDiffer(t *testing.T) {
	tool := releaseTool(t)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	wrapper := filepath.Join(dir, goName)
	command := exec.Command(realGo, "build", "-o", wrapper, "./tools/release/testdata/varying-go")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build varying Go wrapper: %v\n%s", err, output)
	}
	source := filepath.Join(t.TempDir(), "source")
	testutil.CopySource(t, filepath.Join("..", ".."), source)
	outputDir := filepath.Join(t.TempDir(), "release")
	command = exec.Command(tool, "-tag", "v1.0.0-preview.20261003.1", "-output", outputDir)
	command.Dir = source
	command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "RELEASE_TEST_GO="+realGo, "RELEASE_TEST_VARIATION="+filepath.Join(dir, "variation"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("inconsistent builds succeeded: %s", output)
	}
	if !strings.Contains(string(output), "repeated builds differ: sandboxed-agents-windows-amd64.exe") {
		t.Fatalf("release did not report inconsistent Windows binaries: %s", output)
	}
	variation, err := os.ReadFile(filepath.Join(dir, "variation"))
	if err != nil || string(variation) != "xx" {
		t.Fatalf("varying Go wrapper did not run for both builds: %q, %v\n%s", variation, err, output)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("inconsistent builds created output: %v", err)
	}
}
