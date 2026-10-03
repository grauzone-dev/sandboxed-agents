package manager_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSSHServerStartsWithHostKeysInEmptySandboxState(t *testing.T) {
	state := t.TempDir()
	var generated []string
	var server process.Request
	var stderr bytes.Buffer
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		switch request.Name {
		case "ssh-keygen":
			if len(request.Args) != 7 || request.Args[0] != "-q" || request.Args[1] != "-t" || request.Args[3] != "-N" || request.Args[4] != "" || request.Args[5] != "-f" {
				t.Fatalf("host key request = %#v", request)
			}
			path := request.Args[6]
			if filepath.Dir(path) != state {
				t.Fatalf("host key path = %q, want sandbox state %q", path, state)
			}
			generated = append(generated, filepath.Base(path))
			if err := os.WriteFile(path, []byte("sandbox host key"), 0600); err != nil {
				t.Fatal(err)
			}
		case "/usr/sbin/sshd":
			if len(generated) != 3 {
				t.Fatalf("SSH server started before host keys existed: %v", generated)
			}
			server = request
		default:
			t.Fatalf("unexpected process = %#v", request)
		}
		return 0, nil
	}, manager.Options{SSHStateDirectory: state})
	status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr})
	if status != 0 || stderr.Len() != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if !reflect.DeepEqual(generated, []string{"ssh_host_ed25519_key", "ssh_host_ecdsa_key", "ssh_host_rsa_key"}) {
		t.Fatalf("generated keys = %v", generated)
	}
	wantServerArgs := []string{"-f", "/usr/local/etc/sandboxed-agents/sshd_config", "-h", filepath.Join(state, "ssh_host_ed25519_key"), "-h", filepath.Join(state, "ssh_host_ecdsa_key"), "-h", filepath.Join(state, "ssh_host_rsa_key")}
	if !reflect.DeepEqual(server.Args, wantServerArgs) {
		t.Fatalf("server args = %v, want %v", server.Args, wantServerArgs)
	}
}

func TestSSHServerReusesSandboxHostKeysAndRequiresAgentKeyAuthentication(t *testing.T) {
	state := t.TempDir()
	keys := map[string]string{
		"ssh_host_ed25519_key": "persisted ed25519 private key",
		"ssh_host_ecdsa_key":   "persisted ecdsa private key",
		"ssh_host_rsa_key":     "persisted rsa private key",
	}
	for name, contents := range keys {
		if err := os.WriteFile(filepath.Join(state, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var starts int
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		if request.Name != "/usr/sbin/sshd" {
			t.Fatalf("persisted host keys triggered process = %#v", request)
		}
		starts++
		if len(request.Args) < 2 || request.Args[0] != "-f" || request.Args[1] != "/usr/local/etc/sandboxed-agents/sshd_config" {
			t.Fatalf("server configuration = %v", request.Args)
		}
		config, err := os.ReadFile("../../build/context/sshd_config")
		if err != nil {
			t.Fatal(err)
		}
		directives := map[string]string{}
		for _, line := range strings.Split(string(config), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 {
				directives[fields[0]] = fields[1]
			}
		}
		for option, want := range map[string]string{
			"AuthenticationMethods":        "publickey",
			"PubkeyAuthentication":         "yes",
			"PasswordAuthentication":       "no",
			"KbdInteractiveAuthentication": "no",
			"PermitRootLogin":              "no",
			"AllowUsers":                   "agent",
			"AllowAgentForwarding":         "no",
			"UsePAM":                       "yes",
		} {
			if got := directives[option]; got != want {
				t.Errorf("SSH configuration %s = %q, want %q", option, got, want)
			}
		}
		return 0, nil
	}, manager.Options{SSHStateDirectory: state})
	for range 2 {
		var stderr bytes.Buffer
		if status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr}); status != 0 {
			t.Fatalf("status=%d stderr=%q", status, stderr.String())
		}
	}
	if starts != 2 {
		t.Fatalf("server starts = %d, want 2", starts)
	}
	for name, want := range keys {
		contents, err := os.ReadFile(filepath.Join(state, name))
		if err != nil || string(contents) != want {
			t.Fatalf("persisted key %s = %q, error=%v", name, contents, err)
		}
	}
}

func TestSSHServerRejectsInvalidUsageBeforeChangingSandboxState(t *testing.T) {
	for _, args := range [][]string{{"ssh"}, {"ssh", "other"}, {"ssh", "--unknown"}, {"ssh", "start", "extra"}, {"ssh", "start", "--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "missing-state")
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid SSH usage started a process")
				return 1, nil
			}, manager.Options{SSHStateDirectory: state})
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), args, process.Streams{Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
			if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid SSH usage changed state directory: %v", err)
			}
		})
	}
}

func TestSSHServerReportsProcessFailuresAndStopsStartup(t *testing.T) {
	for _, test := range []struct {
		name    string
		process string
		status  int
		err     error
	}{
		{name: "key generation exit", process: "ssh-keygen", status: 7},
		{name: "key generation launch", process: "ssh-keygen", err: errors.New("injected key generation failure")},
		{name: "server exit", process: "/usr/sbin/sshd", status: 9},
		{name: "server launch", process: "/usr/sbin/sshd", err: errors.New("injected server failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			if test.process == "/usr/sbin/sshd" {
				for _, name := range []string{"ssh_host_ed25519_key", "ssh_host_ecdsa_key", "ssh_host_rsa_key"} {
					if err := os.WriteFile(filepath.Join(state, name), []byte("persisted host key"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				calls++
				if request.Name != test.process || calls != 1 {
					t.Fatalf("process after startup failure = %#v", request)
				}
				return test.status, test.err
			}, manager.Options{SSHStateDirectory: state})
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr}); status == 0 || stderr.Len() == 0 || calls != 1 {
				t.Fatalf("status=%d stderr=%q calls=%d", status, stderr.String(), calls)
			}
		})
	}
}

func TestSSHServerRefusesUnusableSandboxStateWithoutStartingProcesses(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state-file")
	if err := os.WriteFile(state, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("unusable sandbox state started a process")
		return 1, nil
	}, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	contents, err := os.ReadFile(state)
	if err != nil || string(contents) != "not a directory" {
		t.Fatalf("unusable state = %q error=%v", contents, err)
	}
}

func TestSSHServerCompletesPartialHostKeysWithoutReplacingPersistedKeys(t *testing.T) {
	state := t.TempDir()
	persisted := filepath.Join(state, "ssh_host_ed25519_key")
	if err := os.WriteFile(persisted, []byte("persisted ed25519 private key"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(persisted)
	if err != nil {
		t.Fatal(err)
	}
	var generated []string
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "ssh-keygen" {
			path := request.Args[len(request.Args)-1]
			generated = append(generated, filepath.Base(path))
			if err := os.WriteFile(path, []byte("new host key"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if request.Name != "/usr/sbin/sshd" {
			t.Fatalf("unexpected process = %#v", request)
		}
		return 0, nil
	}, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr}); status != 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	if !reflect.DeepEqual(generated, []string{"ssh_host_ecdsa_key", "ssh_host_rsa_key"}) {
		t.Fatalf("generated keys = %v", generated)
	}
	contents, err := os.ReadFile(persisted)
	if err != nil || string(contents) != "persisted ed25519 private key" {
		t.Fatalf("persisted host key = %q error=%v", contents, err)
	}
	after, err := os.Stat(persisted)
	if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("persisted host key metadata changed: before=%v after=%v error=%v", before, after, err)
	}
}
