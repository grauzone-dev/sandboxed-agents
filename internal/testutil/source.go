package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CopySource copies the module sources a build needs from source to target, without tests and the generated asset bundle, so a test can build without writing to the checkout.
func CopySource(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "cmd", "internal", "tools", "build"} {
		origin := filepath.Join(source, name)
		err := filepath.WalkDir(origin, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			destination := filepath.Join(target, relative)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0755)
			}
			if strings.HasSuffix(path, "_test.go") || entry.Name() == "bundle.zip" {
				return nil
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, contents, 0644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
