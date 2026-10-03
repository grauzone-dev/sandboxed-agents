package release_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/release"
)

func TestChecksumsCoverBothReleaseExecutablesInStableOrder(t *testing.T) {
	dir := t.TempDir()
	for name, contents := range map[string]string{
		"sandboxed-agents-linux-amd64":       "abc",
		"sandboxed-agents-windows-amd64.exe": "",
		"unrelated":                          "ignored",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := release.Checksums(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  sandboxed-agents-linux-amd64\n" +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  sandboxed-agents-windows-amd64.exe\n"
	if string(got) != want {
		t.Fatalf("SHA256SUMS = %q; want %q", got, want)
	}
}
