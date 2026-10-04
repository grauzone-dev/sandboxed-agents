package manager_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/sshkeys"
)

const setupPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINdamAGCsQq31Uv+08lkBzoO4XLz2qYjJa8CGmj3B1Ea"

const replacementPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAID1AF8PoQ4lakrcKp00bfrycmCzPLsSWjMDNVfEq9GYM"

func TestSSHSetupReplacesOneAuthorizationAndKeepsItAcrossServerStarts(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "authorized_keys")
	if err := os.WriteFile(path, []byte("previous authorization\nprevious second authorization\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		if request.Name == "ssh-keygen" {
			if err := os.WriteFile(request.Args[len(request.Args)-1], []byte("host key"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if request.Name != "/usr/sbin/sshd" {
			t.Fatalf("unexpected process = %#v", request)
		}
		return 0, nil
	}, manager.Options{SSHStateDirectory: state})
	for _, key := range []string{setupPublicKey, replacementPublicKey} {
		var stderr bytes.Buffer
		status := app.Run(context.Background(), []string{"ssh", "authorize"}, process.Streams{Stdin: strings.NewReader(key + " dedicated sandbox key\n"), Stderr: &stderr})
		if status != 0 || stderr.Len() != 0 {
			t.Fatalf("status=%d stderr=%q", status, stderr.String())
		}
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != key+"\n" {
			t.Fatalf("authorization=%q error=%v", contents, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil || after.Mode().Perm() != 0644 || os.SameFile(before, after) {
		t.Fatalf("authorization metadata before=%v after=%v error=%v", before, after, err)
	}
	for range 2 {
		var stderr bytes.Buffer
		if status := app.Run(context.Background(), []string{"ssh", "start"}, process.Streams{Stderr: &stderr}); status != 0 {
			t.Fatalf("status=%d stderr=%q", status, stderr.String())
		}
	}
	contents, err := os.ReadFile(path)
	persisted, statErr := os.Stat(path)
	if err != nil || statErr != nil || string(contents) != replacementPublicKey+"\n" || !os.SameFile(after, persisted) || !persisted.ModTime().Equal(after.ModTime()) {
		t.Fatalf("persisted authorization=%q error=%v metadata=%v stat error=%v", contents, err, persisted, statErr)
	}
}

func TestSSHHostKeyReturnsCanonicalSandboxKeyWithoutStartingProcesses(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "ssh_host_ed25519_key.pub")
	if err := os.WriteFile(path, []byte(setupPublicKey+" sandbox host key\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
		t.Fatal("reading the host key started a process")
		return 1, nil
	}, manager.Options{SSHStateDirectory: state})
	var stdout, stderr bytes.Buffer
	status := app.Run(context.Background(), []string{"ssh", "host-key"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
	if status != 0 || stderr.Len() != 0 || stdout.String() != setupPublicKey+"\n" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
}

func TestSSHAuthorizationRejectsMalformedOrUnreadableKeysWithoutReplacement(t *testing.T) {
	for _, test := range []struct {
		name  string
		input io.Reader
	}{
		{name: "missing input"},
		{name: "empty", input: strings.NewReader("")},
		{name: "invalid base64", input: strings.NewReader("ssh-ed25519 invalid")},
		{name: "invalid wire key", input: strings.NewReader("ssh-ed25519 YWJj")},
		{name: "two keys", input: strings.NewReader(setupPublicKey + "\n" + replacementPublicKey)},
		{name: "authorization options", input: strings.NewReader("command=\"x\" " + setupPublicKey)},
		{name: "oversized", input: strings.NewReader(setupPublicKey + strings.Repeat("x", sshkeys.MaxPublicKeyBytes))},
		{name: "read failure", input: failingSSHInput{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "authorized_keys")
			if err := os.WriteFile(path, []byte(replacementPublicKey+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("authorization failure started a process")
				return 1, nil
			}, manager.Options{SSHStateDirectory: state})
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"ssh", "authorize"}, process.Streams{Stdin: test.input, Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
			contents, err := os.ReadFile(path)
			after, statErr := os.Stat(path)
			if err != nil || statErr != nil || string(contents) != replacementPublicKey+"\n" || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || after.Mode() != before.Mode() {
				t.Fatalf("authorization=%q error=%v metadata=%v stat error=%v", contents, err, after, statErr)
			}
			entries, err := os.ReadDir(state)
			if err != nil || len(entries) != 1 || entries[0].Name() != "authorized_keys" {
				t.Fatalf("authorization failure changed state: %v error=%v", entries, err)
			}
		})
	}
}

type failingSSHInput struct{}

func (failingSSHInput) Read(data []byte) (int, error) {
	return copy(data, setupPublicKey), errors.New("injected input failure")
}

func TestSSHAuthorizationBoundsUntrustedInput(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", 128*1024))
	state := filepath.Join(t.TempDir(), "missing-state")
	app := manager.NewWithOptions("test", nil, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "authorize"}, process.Streams{Stdin: input, Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	consumed := 128*1024 - input.Len()
	if consumed > 16*1024+1 {
		t.Fatalf("input consumed=%d", consumed)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid input created SSH state: %v", err)
	}
}

func TestSSHAuthorizationWriteFailureLeavesDestinationAndCleansTemporaryFile(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, "authorized_keys")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", nil, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "authorize"}, process.Streams{Stdin: strings.NewReader(setupPublicKey), Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "preserved" {
		t.Fatalf("destination contents=%q error=%v", contents, err)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "authorized_keys" {
		t.Fatalf("write failure changed state: %v error=%v", entries, err)
	}
}

func TestSSHAuthorizationCannotReplaceUnusableState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state-file")
	if err := os.WriteFile(state, []byte("preserved state"), 0600); err != nil {
		t.Fatal(err)
	}
	app := manager.NewWithOptions("test", nil, manager.Options{SSHStateDirectory: state})
	var stderr bytes.Buffer
	if status := app.Run(context.Background(), []string{"ssh", "authorize"}, process.Streams{Stdin: strings.NewReader(setupPublicKey), Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
	contents, err := os.ReadFile(state)
	if err != nil || string(contents) != "preserved state" {
		t.Fatalf("state=%q error=%v", contents, err)
	}
}

func TestSSHHostKeyReportsReadAndOutputFailures(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "ssh_host_ed25519_key.pub")
			if unreadable {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(setupPublicKey), 0644); err != nil {
				t.Fatal(err)
			}
			app := manager.NewWithOptions("test", nil, manager.Options{SSHStateDirectory: state})
			var stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"ssh", "host-key"}, process.Streams{Stdout: failingSSHOutput{}, Stderr: &stderr}); status == 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d stderr=%q", status, stderr.String())
			}
		})
	}
}

type failingSSHOutput struct{}

func (failingSSHOutput) Write([]byte) (int, error) {
	return 0, errors.New("injected output failure")
}

func TestSSHHostKeyFailureReturnsNoKeyAndPreservesState(t *testing.T) {
	for _, input := range []string{"", "ssh-ed25519 YWJj", setupPublicKey + "\n" + replacementPublicKey, setupPublicKey + strings.Repeat("x", sshkeys.MaxPublicKeyBytes)} {
		t.Run(input[:min(len(input), 30)], func(t *testing.T) {
			state := t.TempDir()
			path := filepath.Join(state, "ssh_host_ed25519_key.pub")
			if input != "" {
				if err := os.WriteFile(path, []byte(input), 0644); err != nil {
					t.Fatal(err)
				}
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("host key failure started a process")
				return 1, nil
			}, manager.Options{SSHStateDirectory: state})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"ssh", "host-key"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status == 0 || stderr.Len() == 0 || stdout.Len() != 0 {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
			}
			if input != "" {
				contents, err := os.ReadFile(path)
				if err != nil || string(contents) != input {
					t.Fatalf("host key changed: contents=%q error=%v", contents, err)
				}
			}
		})
	}
}

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
			"AuthorizedKeysFile":           "/etc/ssh/authorized_keys",
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
	for _, args := range [][]string{{"ssh"}, {"ssh", "other"}, {"ssh", "--unknown"}, {"ssh", "start", "extra"}, {"ssh", "start", "--unknown"}, {"ssh", "host-key", "extra"}, {"ssh", "authorize", "extra"}, {"ssh", "remove"}} {
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
