package testutil_test

import (
	"path/filepath"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestKnownHostsFileReadsTheArgumentAsSSHDoes(t *testing.T) {
	path, ok := testutil.KnownHostsFile([]string{"-F", "none", "-o", "BatchMode=yes", "-o", `UserKnownHostsFile="C:/Users/a b/100%%/known_hosts"`, "127.0.0.1"})
	if !ok || path != filepath.FromSlash("C:/Users/a b/100%/known_hosts") {
		t.Fatalf("path=%q found=%v", path, ok)
	}
	if _, ok := testutil.KnownHostsFile([]string{"UserKnownHostsFile=x", "-o", "BatchMode=yes"}); ok {
		t.Fatal("a value without its -o was taken as the known_hosts file")
	}
}
