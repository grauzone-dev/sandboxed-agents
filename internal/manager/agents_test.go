package manager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/agentcatalog"
	"github.com/grauzone-dev/sandboxed-agents/internal/manager"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestEnableInstallsIntoHomeAsAgent(t *testing.T) {
	home := t.TempDir()
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.User == nil || r.User.UID != 1000 || r.User.GID != 1000 {
			t.Fatalf("installation identity=%+v", r.User)
		}
		want := []string{"install", "--global", "--prefix", filepath.Join(home, ".local"), "--cache", filepath.Join(home, ".local", "cache", "sandboxed-agents", "npm"), "@openai/codex@latest"}
		if r.Name != "/usr/bin/npm" || !reflect.DeepEqual(r.Args, want) {
			t.Fatalf("request=%+v", r)
		}
		writeInstalledPackage(t, home, "@openai/codex", "1.2.3")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	var out, err bytes.Buffer
	if status := app.Run(context.Background(), []string{"agents", "enable", "codex"}, process.Streams{Stdout: &out, Stderr: &err}); status != 0 {
		t.Fatalf("status=%d err=%s", status, &err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Fatalf("out=%s", &out)
	}
}
func writeInstalledPackage(t *testing.T, home, pkg, version string) {
	t.Helper()
	path := filepath.Join(home, ".local", "lib", "node_modules", filepath.FromSlash(pkg), "package.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"version":%q}`, version)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEnableSupportsEveryCatalogEntryAndPreservesHome(t *testing.T) {
	home := t.TempDir()
	sentinel := filepath.Join(home, "credentials")
	os.WriteFile(sentinel, []byte("unchanged"), 0600)
	catalog := agentcatalog.Embedded()
	installs := 0
	runner := func(_ context.Context, r process.Request) (int, error) {
		if r.User == nil || r.User.UID != 1000 || r.User.GID != 1000 {
			t.Fatalf("root installation: %+v", r)
		}
		installs++
		pkg := strings.TrimSuffix(r.Args[len(r.Args)-1], "@latest")
		writeInstalledPackage(t, home, pkg, "2.3.4")
		return 0, nil
	}
	options := manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }}
	for _, name := range catalog.Names() {
		app := manager.NewWithOptions("test", runner, options)
		var out, diagnostic bytes.Buffer
		if status := app.Run(context.Background(), []string{"agents", "enable", name}, process.Streams{Stdout: &out, Stderr: &diagnostic}); status != 0 {
			t.Fatalf("%s: %s", name, &diagnostic)
		}
	}
	selectionPath := filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")
	before, err := os.ReadFile(selectionPath)
	if err != nil {
		t.Fatal(err)
	}
	var selected map[string]any
	if err := json.Unmarshal(before, &selected); err != nil || len(selected) != 4 {
		t.Fatalf("selection=%s", before)
	}
	for _, name := range catalog.Names() {
		var out, diagnostic bytes.Buffer
		app := manager.NewWithOptions("test", runner, options)
		if status := app.Run(context.Background(), []string{"agents", "enable", name}, process.Streams{Stdout: &out, Stderr: &diagnostic}); status != 0 || !strings.Contains(out.String(), "2.3.4") {
			t.Fatalf("%s: %s %s", name, &out, &diagnostic)
		}
	}
	after, _ := os.ReadFile(selectionPath)
	credential, _ := os.ReadFile(sentinel)
	if installs != 4 || !bytes.Equal(before, after) || string(credential) != "unchanged" {
		t.Fatalf("installs=%d selection changed=%t credential=%s", installs, !bytes.Equal(before, after), credential)
	}
}

func TestRootOnlyLaunchesTrustedManagerBeforeHomeWork(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "sandboxed-agents")
	os.MkdirAll(state, 0700)
	os.WriteFile(filepath.Join(state, "selection.json"), []byte(`{"evil":{"version":"/home/agent/evil","command":"/home/agent/evil","args":["from-workspace"]}}`), 0600)
	os.WriteFile(filepath.Join(home, ".npmrc"), []byte("script-shell=/home/agent/evil\n"), 0600)
	worker := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if r.User == nil || r.User.UID != 1000 || r.User.GID != 1000 || r.Name != "/usr/bin/npm" {
			t.Fatalf("unsafe user process %+v", r)
		}
		writeInstalledPackage(t, home, "@openai/codex", "1.0.0")
		return 0, nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	rootCalls := 0
	root := manager.NewWithOptions("test", func(ctx context.Context, r process.Request) (int, error) {
		rootCalls++
		if r.Name != "/usr/local/bin/sandboxed-agents-manager" || !reflect.DeepEqual(r.Args, []string{"agents", "enable", "codex"}) || r.User == nil || *r.User != (process.Identity{UID: 1000, GID: 1000}) || r.Dir != "/" {
			t.Fatalf("unsafe administrative process %+v", r)
		}
		for _, value := range r.Env {
			if strings.HasPrefix(value, "NPM_") {
				t.Fatalf("root npm configuration inherited: %s", value)
			}
		}
		return worker.Run(ctx, r.Args, r.Streams), nil
	}, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 0, GID: 0} }})
	var out, diagnostic bytes.Buffer
	if status := root.Run(context.Background(), []string{"agents", "enable", "codex"}, process.Streams{Stdout: &out, Stderr: &diagnostic}); status != 0 || rootCalls != 1 {
		t.Fatalf("calls=%d status=%d err=%s", rootCalls, status, &diagnostic)
	}
}

func TestConcurrentEnablesSerializeInstallationAndKeepBothAgents(t *testing.T) {
	home := t.TempDir()
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	options := manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }}
	runner := func(_ context.Context, r process.Request) (int, error) {
		pkg := strings.TrimSuffix(r.Args[len(r.Args)-1], "@latest")
		if pkg == "@openai/codex" {
			close(firstStarted)
			<-releaseFirst
		} else {
			close(secondStarted)
		}
		writeInstalledPackage(t, home, pkg, "3.4.5")
		return 0, nil
	}
	done := make(chan string, 2)
	enable := func(name string) {
		var out, diagnostic bytes.Buffer
		status := manager.NewWithOptions("test", runner, options).Run(context.Background(), []string{"agents", "enable", name}, process.Streams{Stdout: &out, Stderr: &diagnostic})
		if status != 0 {
			done <- diagnostic.String()
		} else {
			done <- ""
		}
	}
	go enable("codex")
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first installation never started")
	}
	go enable("claude")
	select {
	case <-secondStarted:
		close(releaseFirst)
		t.Fatal("second installation ran while first held lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		select {
		case err := <-done:
			if err != "" {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("installation did not complete")
		}
	}
	selection, _ := os.ReadFile(filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json"))
	var selected map[string]any
	if err := json.Unmarshal(selection, &selected); err != nil || len(selected) != 2 || selected["codex"] == nil || selected["claude"] == nil {
		t.Fatalf("selection=%s", selection)
	}
}

func TestFifthNpmAgentNeedsOnlyCatalogData(t *testing.T) {
	entries := []agentcatalog.Entry{}
	embedded := agentcatalog.Embedded()
	for _, name := range embedded.Names() {
		entry, _ := embedded.Find(name)
		entries = append(entries, entry)
	}
	entries = append(entries, agentcatalog.Entry{Name: "fifth", Delivered: true, Command: "fifth", Install: agentcatalog.Install{Kind: "npm", Package: "@example/fifth"}})
	data, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schema_version"`
		Entries       []agentcatalog.Entry `json:"entries"`
	}{1, entries})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := agentcatalog.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	app := manager.NewWithOptions("test", func(_ context.Context, r process.Request) (int, error) {
		if got := r.Args[len(r.Args)-1]; got != "@example/fifth@latest" {
			t.Fatalf("package=%s", got)
		}
		writeInstalledPackage(t, home, "@example/fifth", "9.8.7")
		return 0, nil
	}, manager.Options{Home: home, Catalog: &catalog, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
	var out, diagnostic bytes.Buffer
	if status := app.Run(context.Background(), []string{"agents", "enable", "fifth"}, process.Streams{Stdout: &out, Stderr: &diagnostic}); status != 0 || !strings.Contains(out.String(), "9.8.7") {
		t.Fatalf("status=%d out=%s err=%s", status, &out, &diagnostic)
	}
	if len(catalog.Names()) != 5 {
		t.Fatal(catalog.Names())
	}
	selection, err := os.ReadFile(filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var selected map[string]struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(selection, &selected); err != nil || selected["fifth"].Version != "9.8.7" {
		t.Fatalf("selection=%s err=%v", selection, err)
	}
}

func TestEnableFailureNeverRecordsAgent(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		err    error
	}{{"exit", 17, nil}, {"launch", 0, fmt.Errorf("launch failed")}} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) { return test.status, test.err }, manager.Options{Home: home, User: func() process.Identity { return process.Identity{UID: 1000, GID: 1000} }})
			var diagnostic bytes.Buffer
			if status := app.Run(context.Background(), []string{"agents", "enable", "codex"}, process.Streams{Stderr: &diagnostic}); status == 0 {
				t.Fatal("installation failure succeeded")
			}
			if _, err := os.Stat(filepath.Join(home, ".local", "state", "sandboxed-agents", "selection.json")); !os.IsNotExist(err) {
				t.Fatalf("selection recorded after failure: %v", err)
			}
		})
	}
}

func TestInvalidAgentWorkStartsNoProcessOrHomeWrites(t *testing.T) {
	for _, args := range [][]string{{"agents"}, {"agents", "enable"}, {"agents", "disable", "codex"}, {"agents", "enable", "unknown"}, {"agents", "enable", "codex", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := t.TempDir()
			app := manager.NewWithOptions("test", func(context.Context, process.Request) (int, error) {
				t.Fatal("invalid request started process")
				return 1, nil
			}, manager.Options{Home: home})
			if status := app.Run(context.Background(), args, process.Streams{}); status == 0 {
				t.Fatal("invalid request succeeded")
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 0 {
				t.Fatal("invalid request wrote home")
			}
		})
	}
}
