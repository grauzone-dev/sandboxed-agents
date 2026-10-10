package testutil_test

import (
	"path/filepath"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestKnownHostsFileReadsTheArgumentAsSSHDoes(t *testing.T) {
	path, ok := testutil.KnownHostsFile("", []string{"-F", "none", "-o", "BatchMode=yes", "-o", `UserKnownHostsFile="C:/Users/a b/100%%/known_hosts"`, "127.0.0.1"})
	if !ok || path != filepath.FromSlash("C:/Users/a b/100%/known_hosts") {
		t.Fatalf("path=%q found=%v", path, ok)
	}
	path, ok = testutil.KnownHostsFile("", []string{"-o", "UserKnownHostsFile=/tmp/plain/known_hosts"})
	if !ok || path != filepath.FromSlash("/tmp/plain/known_hosts") {
		t.Fatalf("unquoted path=%q found=%v", path, ok)
	}
	path, ok = testutil.KnownHostsFile("", []string{"-o", `UserKnownHostsFile="/tmp/a\"b/known_hosts"`})
	if !ok || path != filepath.FromSlash(`/tmp/a"b/known_hosts`) {
		t.Fatalf("escaped quote path=%q found=%v", path, ok)
	}
	path, ok = testutil.KnownHostsFile(filepath.FromSlash("/tmp/probe dir"), []string{"-o", "UserKnownHostsFile=known_hosts"})
	if !ok || path != filepath.Join(filepath.FromSlash("/tmp/probe dir"), "known_hosts") {
		t.Fatalf("relative path=%q found=%v", path, ok)
	}
	if _, ok := testutil.KnownHostsFile("", []string{"UserKnownHostsFile=x", "-o", "BatchMode=yes"}); ok {
		t.Fatal("a value without its -o was taken as the known_hosts file")
	}
}
