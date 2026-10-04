package manager_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestAgentSelectionListsEnabledAgentsWithoutInstallingOrCreatingState(t *testing.T) {
	home := t.TempDir()
	installs := 0
	app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
		installs++
		pkg := strings.TrimSuffix(request.Args[len(request.Args)-1], "@latest")
		writeInstalledPackage(t, home, pkg, "1.2.3")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	list := func(want string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if status := app.Run(context.Background(), []string{"agents", "list"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status != 0 || stderr.Len() != 0 || stdout.String() != want {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
	}
	list("[]\n")
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("read-only list created state: entries=%v err=%v", entries, err)
	}
	for _, agent := range []string{"codex", "claude", "codex"} {
		if status := app.Run(context.Background(), []string{"agents", "enable", agent}, process.Streams{}); status != 0 {
			t.Fatalf("enable %s status=%d", agent, status)
		}
	}
	path := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list("[\"claude\",\"codex\"]\n")
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || installs != 2 {
		t.Fatalf("installs=%d selection changed=%t err=%v", installs, !bytes.Equal(before, after), err)
	}
}

func TestAgentSelectionIsReadOnlyAfterDroppingRootIdentity(t *testing.T) {
	for _, identity := range []process.Identity{{UID: 0, GID: 0}, {UID: 1000, GID: 0}} {
		t.Run(fmt.Sprint(identity), func(t *testing.T) {
			calls := 0
			app := manager.NewWithOptions("test", func(_ context.Context, request process.Request) (int, error) {
				calls++
				if request.Name != "/usr/local/bin/sandboxed-agents-manager" || !reflect.DeepEqual(request.Args, []string{"agents", "list"}) || request.User == nil || *request.User != (process.Identity{UID: 1000, GID: 1000}) || request.Dir != "/" {
					t.Fatalf("worker request=%+v", request)
				}
				fmt.Fprintln(request.Streams.Stdout, `["codex"]`)
				return 0, nil
			}, manager.Options{Home: t.TempDir(), User: func() process.Identity { return identity }})
			var stdout, stderr bytes.Buffer
			status := app.Run(context.Background(), []string{"agents", "list"}, process.Streams{Stdout: &stdout, Stderr: &stderr})
			if identity.UID == 0 {
				if status != 0 || calls != 1 || stdout.String() != "[\"codex\"]\n" || stderr.Len() != 0 {
					t.Fatalf("status=%d calls=%d out=%q err=%q", status, calls, stdout.String(), stderr.String())
				}
			} else if status == 0 || calls != 0 || stdout.Len() != 0 {
				t.Fatalf("status=%d calls=%d out=%q", status, calls, stdout.String())
			}
		})
	}
}

func TestAgentSelectionRefusesUnreadableOrInvalidHomeStateWithoutReportingAgents(t *testing.T) {
	for _, content := range []string{"null", "[]", "bad-json", `{"unknown":{"version":"1.2.3"}}`, `{"codex":1}`} {
		t.Run(content, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("list started a process")
				return 1, nil
			}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var stdout, stderr bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "list"}, process.Streams{Stdout: &stdout, Stderr: &stderr}); status == 0 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("status=%d out=%q err=%q", status, stdout.String(), stderr.String())
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != content {
				t.Fatalf("state changed: %q err=%v", after, err)
			}
		})
	}
}
