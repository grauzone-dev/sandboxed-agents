package main_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"debug/pe"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestBuildEmbedsLinuxManagerAndNormalizedContextForBothHosts(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	dir := t.TempDir()
	builder := filepath.Join(dir, "build")
	if runtime.GOOS == "windows" {
		builder += ".exe"
	}
	command := exec.Command("go", "build", "-o", builder, "./tools/build")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build tool: %v\n%s", err, output)
	}
	source := filepath.Join(dir, "source")
	testutil.CopySource(t, root, source)
	contextPath := filepath.Join(source, "build", "context", "Containerfile")
	contextFiles := map[string][]byte{}
	if err := filepath.WalkDir(filepath.Dir(contextPath), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(contextPath), path)
		if err != nil {
			return err
		}
		contextFiles[filepath.ToSlash(relative)] = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fakes := testutil.NewFakePrograms(t)
	build := func(target string) (string, []byte) {
		t.Helper()
		host := filepath.Join(dir, "host-"+target)
		if target == "windows" {
			host += ".exe"
		}
		command := exec.Command(builder, "-goos", target, "-output", host, "-version", "test-version")
		command.Dir = source
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s build: %v\n%s", target, err, output)
		}
		bundle, err := os.ReadFile(filepath.Join(source, "internal", "assets", "bundle.zip"))
		if err != nil {
			t.Fatal(err)
		}
		binary, err := os.ReadFile(host)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(binary, bundle) {
			t.Fatal("host does not contain the embedded bundle")
		}
		return host, bundle
	}
	checkVersion := func(host string, bundle []byte) string {
		t.Helper()
		command := exec.Command(host, "version")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		stdout, err := command.Output()
		if err != nil {
			t.Fatalf("version: %v, stderr %s", err, &stderr)
		}
		want := fmt.Sprintf("sandboxed-agents test-version\nassets %x\n", sha256.Sum256(bundle))
		if string(stdout) != want || stderr.Len() != 0 {
			t.Fatalf("version stdout=%q stderr=%q; want %q", stdout, stderr.String(), want)
		}
		return string(stdout)
	}
	native, first := build(runtime.GOOS)
	version := checkVersion(native, first)
	t.Setenv("GOFLAGS", "-ldflags=-s")
	t.Setenv("GOEXPERIMENT", "not-a-valid-experiment")
	native, unchanged := build(runtime.GOOS)
	if version != checkVersion(native, unchanged) {
		t.Fatal("unchanged rebuild changed the printed asset hash")
	}
	for name, data := range contextFiles {
		if err := os.WriteFile(filepath.Join(filepath.Dir(contextPath), filepath.FromSlash(name)), bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")), 0644); err != nil {
			t.Fatal(err)
		}
	}
	native, crlf := build(runtime.GOOS)
	if version != checkVersion(native, crlf) {
		t.Fatal("CRLF checkout changed the printed asset hash")
	}
	foreign := "windows"
	if runtime.GOOS == "windows" {
		foreign = "linux"
	}
	foreignHost, foreignBundle := build(foreign)
	if !bytes.Equal(first, foreignBundle) {
		t.Fatal("host target changed the asset hash")
	}
	linuxHost, windowsHost := native, foreignHost
	if runtime.GOOS == "windows" {
		linuxHost, windowsHost = foreignHost, native
	}
	linux, err := elf.Open(linuxHost)
	if err != nil {
		t.Fatal(err)
	}
	if linux.Machine != elf.EM_X86_64 {
		t.Fatalf("Linux host machine: %v", linux.Machine)
	}
	linux.Close()
	windows, err := pe.Open(windowsHost)
	if err != nil {
		t.Fatal(err)
	}
	if windows.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Fatalf("Windows host machine: %v", windows.Machine)
	}
	windows.Close()
	archive, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	file, err := archive.Open("manager")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	executable, err := elf.NewFile(bytes.NewReader(manager))
	if err != nil {
		t.Fatalf("embedded manager is not Linux ELF: %v", err)
	}
	defer executable.Close()
	if executable.Machine != elf.EM_X86_64 {
		t.Fatalf("manager machine: %v", executable.Machine)
	}
	for _, program := range executable.Progs {
		if program.Type == elf.PT_INTERP {
			t.Fatal("embedded manager requires a dynamic loader")
		}
	}
	for name, expected := range contextFiles {
		context, err := archive.Open("context/" + name)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(context)
		context.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, expected) {
			t.Fatalf("embedded context %s was not normalized: %q", name, contents)
		}
	}
	if err := os.WriteFile(contextPath, []byte("FROM different\n"), 0644); err != nil {
		t.Fatal(err)
	}
	native, changed := build(runtime.GOOS)
	if version == checkVersion(native, changed) {
		t.Fatal("changed context did not change the printed asset hash")
	}
	for _, args := range [][]string{{"unknown"}, {"version", "--unknown"}, {"--unknown"}, {}} {
		command := exec.Command(native, args...)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		if err := command.Run(); err == nil {
			t.Fatalf("invalid usage %v succeeded", args)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("invalid usage %v: stdout %q stderr %q", args, &stdout, &stderr)
		}
	}
	if calls := fakes.Calls("podman"); len(calls) != 0 {
		t.Fatalf("unexpected Podman calls: %v", calls)
	}
	if calls := fakes.Calls("ssh"); len(calls) != 0 {
		t.Fatalf("unexpected SSH calls: %v", calls)
	}
}
