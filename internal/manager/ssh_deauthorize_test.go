package manager_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSSHDeauthorizeRemovesAuthorizationAndPreservesServerState(t *testing.T) {
	state := t.TempDir()
	files := map[string]string{
		"authorized_keys":          setupPublicKey + "\n",
		"ssh_host_ed25519_key":     "private ed25519 key",
		"ssh_host_ed25519_key.pub": setupPublicKey + "\n",
		"ssh_host_ecdsa_key":       "private ecdsa key",
		"ssh_host_rsa_key":         "private rsa key",
		"sshd_config":              "server configuration",
	}
	before := make(map[string]os.FileInfo)
	for name, contents := range files {
		path := filepath.Join(state, name)
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = info
	}
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("deauthorization started a process")
		return 1, nil
	}, manager.Options{SSHStateDirectory: state, User: func() process.Identity {
		t.Fatal("deauthorization switched to the agent user")
		return process.Identity{}
	}})
	for range 2 {
		var stdout, stderr bytes.Buffer
		status := app.Run(context.Background(), []string{"ssh", "deauthorize"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
		if status != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(filepath.Join(state, "authorized_keys")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("authorization remains: %v", err)
		}
		for name, want := range files {
			if name == "authorized_keys" {
				continue
			}
			path := filepath.Join(state, name)
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != want {
				t.Fatalf("server state %s=%q error=%v", name, contents, err)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before[name], after) || !before[name].ModTime().Equal(after.ModTime()) || before[name].Mode() != after.Mode() {
				t.Fatalf("server state %s metadata changed: before=%v after=%v error=%v", name, before[name], after, err)
			}
		}
	}
}

func TestSSHDeauthorizeWithoutServerStateIsIdempotent(t *testing.T) {
	state := filepath.Join(t.TempDir(), "missing-state")
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("deauthorization started a process")
		return 1, nil
	}, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "deauthorize"}, process.Streams{Stderr: &stderr}); status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deauthorization created server state: %v", err)
	}
}

func TestSSHDeauthorizeReportsRemovalFailureAndPreservesAuthorization(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "authorized_keys")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(path, "retained")
	if err := os.WriteFile(retained, []byte(setupPublicKey), 0600); err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("deauthorization started a process")
		return 1, nil
	}, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "deauthorize"}, process.Streams{Stderr: &stderr}); status == 0 || !strings.Contains(stderr.String(), "authorized_keys") {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	contents, err := os.ReadFile(retained)
	if err != nil || string(contents) != setupPublicKey {
		t.Fatalf("authorization=%q error=%v", contents, err)
	}
}

func TestSSHDeauthorizeRejectsArgumentsBeforeRemovingAuthorization(t *testing.T) {
	for _, suffix := range [][]string{{"extra"}, {"--wait"}, {"--unknown"}} {
		t.Run(strings.Join(suffix, " "), func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "authorized_keys")
			if err := os.WriteFile(path, []byte(setupPublicKey), 0600); err != nil {
				t.Fatal(err)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid deauthorization started a process")
				return 1, nil
			}, manager.Options{SSHStateDirectory: state})
			var stderr bytes.Buffer
			args := append([]string{"ssh", "deauthorize"}, suffix...)
			if status := app.Run(context.Background(), args, process.Streams{Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != setupPublicKey {
				t.Fatalf("authorization=%q error=%v", contents, err)
			}
		})
	}
}
