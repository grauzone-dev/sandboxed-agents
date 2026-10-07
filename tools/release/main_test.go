package main_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/npmpackage"
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
	for index, dir := range outputs {
		if index == 1 {
			launcherPath := filepath.Join(source, "internal", "npmpackage", "launcher.cjs")
			launcher, err := os.ReadFile(launcherPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(launcherPath, bytes.ReplaceAll(launcher, []byte("\n"), []byte("\r\n")), 0644); err != nil {
				t.Fatal(err)
			}
			alternateTool := filepath.Join(t.TempDir(), filepath.Base(tool))
			compile := exec.Command("go", "build", "-o", alternateTool, "./tools/release")
			compile.Dir = source
			if output, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("build release tool with Windows line endings: %v\n%s", err, output)
			}
			tool = alternateTool
		}
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
		wantNames := []string{"SHA256SUMS", "SandboxedAgents.1.0.0-preview.20261003.1.nupkg", "sandboxed-agents-1.0.0-preview.20261003.1.tgz", "sandboxed-agents-linux-amd64", "sandboxed-agents-windows-amd64.exe"}
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
		assertNPMPackage(t, artifacts[npmpackage.Filename(tag)], artifacts)
		first = artifacts
		checkNuGetPackage(t, artifacts)
		wantChecksums := fmt.Sprintf("%x  sandboxed-agents-linux-amd64\n%x  sandboxed-agents-windows-amd64.exe\n", sha256.Sum256(artifacts["sandboxed-agents-linux-amd64"]), sha256.Sum256(artifacts["sandboxed-agents-windows-amd64.exe"]))
		if string(artifacts["SHA256SUMS"]) != wantChecksums {
			t.Fatalf("SHA256SUMS does not verify both binaries: %s", artifacts["SHA256SUMS"])
		}
		bundle, err := os.ReadFile(filepath.Join(source, "internal", "assets", "bundle.zip"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"sandboxed-agents-linux-amd64", "sandboxed-agents-windows-amd64.exe"} {
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
			assertInstalledVersion(t, dir, tag, want)
		}
		linux, err := elf.NewFile(bytes.NewReader(artifacts["sandboxed-agents-linux-amd64"]))
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
		windows, err := pe.NewFile(bytes.NewReader(artifacts["sandboxed-agents-windows-amd64.exe"]))
		if err != nil {
			t.Fatal(err)
		}
		if windows.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
			t.Fatalf("Windows architecture = %v", windows.Machine)
		}
		windows.Close()
	}
}

func checkNuGetPackage(t *testing.T, artifacts map[string][]byte) {
	t.Helper()
	contents := artifacts["SandboxedAgents.1.0.0-preview.20261003.1.nupkg"]
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{
		"SandboxedAgents.nuspec", "[Content_Types].xml", "_rels/.rels",
		"tools/SHA256SUMS", "tools/command-path.ps1", "tools/install-command.ps1",
		"tools/remove-command.ps1", "tools/sandboxed-agents-windows-amd64.exe",
	}
	if len(archive.File) != len(wantNames) {
		t.Fatalf("NuGet contents: %v", archive.File)
	}
	files := make(map[string][]byte)
	for i, file := range archive.File {
		if file.Name != wantNames[i] {
			t.Fatalf("NuGet entry %d = %q; want %q", i, file.Name, wantNames[i])
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name], err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"SHA256SUMS", "sandboxed-agents-windows-amd64.exe"} {
		if !bytes.Equal(files["tools/"+name], artifacts[name]) {
			t.Fatalf("NuGet %s differs from the attested release file", name)
		}
	}
	for _, name := range []string{"command-path.ps1", "install-command.ps1", "remove-command.ps1"} {
		script, err := os.ReadFile(filepath.Join("..", "..", "build", "nuget", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files["tools/"+name], bytes.ReplaceAll(script, []byte("\r\n"), []byte("\n"))) {
			t.Fatalf("NuGet %s differs from its source", name)
		}
	}
	var manifest struct {
		XMLName  xml.Name `xml:"package"`
		Metadata struct {
			ID         string `xml:"id"`
			Version    string `xml:"version"`
			Authors    string `xml:"authors"`
			ProjectURL string `xml:"projectUrl"`
			Repository struct {
				Type string `xml:"type,attr"`
				URL  string `xml:"url,attr"`
			} `xml:"repository"`
		} `xml:"metadata"`
	}
	if err := xml.Unmarshal(files["SandboxedAgents.nuspec"], &manifest); err != nil {
		t.Fatal(err)
	}
	metadata := manifest.Metadata
	if metadata.ID != "SandboxedAgents" || metadata.Version != "1.0.0-preview.20261003.1" || metadata.Authors != "grauzone" || metadata.ProjectURL != "https://github.com/grauzone-dev/sandboxed-agents" || metadata.Repository.Type != "git" || metadata.Repository.URL != "https://github.com/grauzone-dev/sandboxed-agents" {
		t.Fatalf("NuGet metadata = %+v", metadata)
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

func assertNPMPackage(t *testing.T, archive []byte, artifacts map[string][]byte) {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	files := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(time.Unix(0, 0)) {
			t.Fatalf("non-normalized npm header: %+v", header)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = data
	}
	for _, name := range []string{"SHA256SUMS", "sandboxed-agents-linux-amd64", "sandboxed-agents-windows-amd64.exe"} {
		if !bytes.Equal(files["package/"+name], artifacts[name]) {
			t.Fatalf("npm package differs for %s", name)
		}
	}
	if len(files) != 5 || len(files["package/launcher.cjs"]) == 0 {
		t.Fatalf("npm package files: %v", slices.Collect(maps.Keys(files)))
	}
	var metadata struct {
		Name       string
		Version    string
		Bin        map[string]string
		Scripts    map[string]string
		Repository struct {
			Type string
			URL  string
		}
	}
	if err := json.Unmarshal(files["package/package.json"], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "sandboxed-agents" || metadata.Version != "1.0.0-preview.20261003.1" || metadata.Bin["sandboxed-agents"] != "launcher.cjs" || metadata.Scripts["postinstall"] != "node launcher.cjs --verify-install" || metadata.Repository.URL != "git+https://github.com/grauzone-dev/sandboxed-agents.git" {
		t.Fatalf("npm metadata: %+v", metadata)
	}
}

func assertInstalledVersion(t *testing.T, directory, tag, want string) {
	t.Helper()
	prefix := t.TempDir()
	archive, err := filepath.Abs(filepath.Join(directory, npmpackage.Filename(tag)))
	if err != nil {
		t.Fatal(err)
	}
	command := testutil.NpmCommand(t, prefix, "install", archive)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install release package: %v\n%s", err, output)
	}
	_, launch := testutil.NpmInstallationPaths(prefix, false)
	commands := []*exec.Cmd{exec.Command(launch, "version")}
	if runtime.GOOS == "windows" {
		commands = []*exec.Cmd{
			exec.Command("cmd.exe", "/d", "/c", launch+".cmd", "version"),
			exec.Command("pwsh", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", launch+".ps1", "version"),
		}
	}
	for _, command := range commands {
		output, err := command.CombinedOutput()
		if err != nil || string(output) != want {
			t.Fatalf("installed version: %v\n%s; want %s", err, output, want)
		}
	}
}
