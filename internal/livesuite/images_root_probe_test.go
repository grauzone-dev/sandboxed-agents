package livesuite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
)

func TestSelectedImageCoverageRejectsHostKeysHiddenFromTheSandboxUser(t *testing.T) {
	config, fixture := newImageCoverageFixture(t, "linux")
	probed := false
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "podman" && request.Args[0] == "run" {
			probed = true
			return 42, nil
		}
		return fixture.run(ctx, request)
	}
	err := livesuite.Run(context.Background(), config)
	if !probed || err == nil {
		t.Fatalf("root image scan attempted=%t error=%v", probed, err)
	}
	summary := readSummary(t, config, "linux")
	if !hasCheck(summary, "images/base/host-keys", "fail") || summary.ImageCoverageComplete || fixture.removals != 5 || len(fixture.sandboxes) != 0 || fixture.builds != 0 {
		t.Fatalf("summary=%+v removed=%d", summary, fixture.removals)
	}
}

type rootImageProbeFixture struct {
	t                                      *testing.T
	suite                                  *imageCoverageFixture
	name, base, image, id                  string
	exists                                 bool
	retain                                 bool
	runStatus, rmStatus                    int
	foreign                                bool
	incomplete                             bool
	cancel                                 context.CancelFunc
	runs, removes                          int
	changeBeforeProbe, changeDuringCleanup bool
	execute                                func(process.Request, []string) (int, error)
}

func newRootImageProbeFixture(t *testing.T, host string) (livesuite.Config, *rootImageProbeFixture) {
	t.Helper()
	config, suite := newImageCoverageFixture(t, host)
	fixture := &rootImageProbeFixture{t: t, suite: suite, id: "immutable-probe-id"}
	config.Run = fixture.run
	return config, fixture
}

func (fixture *rootImageProbeFixture) run(ctx context.Context, request process.Request) (int, error) {
	if request.Name != "podman" {
		return fixture.suite.run(ctx, request)
	}
	args := request.Args
	if fixture.suite.windows {
		if len(args) > 1 && args[0] == "machine" {
			if args[1] == "list" && fixture.changeBeforeProbe && len(fixture.suite.sandboxes) == 5 && fixture.suite.shells >= 5 {
				fixture.suite.changedTarget = true
			}
			return fixture.suite.run(ctx, request)
		}
		if len(args) < 3 || args[0] != "--connection" || args[1] != "chosen" {
			fixture.t.Fatalf("unbound root probe query: %v", args)
		}
		args = args[2:]
	}
	for _, value := range request.Env {
		key, _, _ := strings.Cut(value, "=")
		if key == "CONTAINER_HOST" || key == "CONTAINER_CONNECTION" || key == "CONTAINER_SSHKEY" {
			fixture.t.Fatalf("remote root probe setting: %s", key)
		}
	}
	if args[0] == "run" {
		fixture.runs++
		fixture.name = rootProbeOption(args, "--name")
		fixture.image = args[len(args)-3]
		for index, arg := range args {
			if arg == "--label" && index+1 < len(args) && strings.HasPrefix(args[index+1], "io.github.sandboxed-agents.live-suite-sandbox-name=") {
				fixture.base = strings.TrimPrefix(args[index+1], "io.github.sandboxed-agents.live-suite-sandbox-name=")
			}
		}
		if !strings.HasPrefix(fixture.name, "sandboxed-agents-live-probe.live.") || fixture.base == "" || fixture.image != "old-base" {
			fixture.t.Fatalf("unpinned/unowned probe: %v", args)
		}
		for _, flag := range []string{"--rm", "--pull=never", "--network=none", "--user=0:0", "--read-only", "--read-only-tmpfs=false", "--image-volume=ignore", "--cap-drop=all", "--cap-add=DAC_READ_SEARCH", "--security-opt=no-new-privileges", "--entrypoint=node"} {
			if !slices.Contains(args, flag) {
				fixture.t.Fatalf("missing isolation flag %s: %v", flag, args)
			}
		}
		if !slices.Contains(args, "io.github.sandboxed-agents.live-suite-group=live") || args[len(args)-2] != "-e" {
			fixture.t.Fatalf("probe arguments: %v", args)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "--volume") || strings.HasPrefix(arg, "--mount") || strings.HasPrefix(arg, "--publish") || arg == "-v" || arg == "-p" || arg == "-t" || arg == "--tty" || arg == "--privileged" {
				fixture.t.Fatalf("unexpected host access: %v", args)
			}
		}
		fixture.exists = true
		if fixture.changeDuringCleanup {
			fixture.suite.changedTarget = true
		}
		if fixture.cancel != nil {
			fixture.cancel()
			return 1, ctx.Err()
		}
		if fixture.execute != nil {
			status, err := fixture.execute(request, args)
			if !fixture.retain {
				fixture.exists = false
			}
			return status, err
		}
		if !fixture.retain {
			fixture.exists = false
		}
		return fixture.runStatus, nil
	}
	if args[0] == "container" && len(args) > 2 && args[2] == fixture.name && fixture.name != "" {
		if args[1] == "exists" {
			if fixture.exists {
				return 0, nil
			}
			return 1, nil
		}
		if args[1] == "inspect" {
			if !fixture.exists {
				return 1, nil
			}
			group := "live"
			if fixture.foreign {
				group = "foreign"
			}
			return 0, json.NewEncoder(request.Streams.Stdout).Encode([]map[string]any{{"ID": fixture.id, "Name": fixture.name, "Image": fixture.image, "Config": map[string]any{"Labels": map[string]string{"io.github.sandboxed-agents.live-suite-group": group, "io.github.sandboxed-agents.live-suite-sandbox-name": fixture.base}}}})
		}
	}
	if args[0] == "rm" {
		fixture.removes++
		if !fixture.exists || fixture.foreign || len(args) != 5 || args[1] != "--force" || args[2] != "--time=2" || args[3] != "--volumes" || args[4] != fixture.id {
			fixture.t.Fatalf("unsafe probe cleanup %v", args)
		}
		deadline, bounded := ctx.Deadline()
		if ctx.Err() != nil || !bounded || time.Until(deadline) > 30*time.Second {
			fixture.t.Fatalf("unbounded/cancelled probe cleanup: %v %v", ctx.Err(), deadline)
		}
		if fixture.rmStatus == 0 && !fixture.incomplete {
			fixture.exists = false
		}
		return fixture.rmStatus, nil
	}
	if args[0] == "ps" && fixture.exists {
		var output bytes.Buffer
		original := request.Streams.Stdout
		request.Streams.Stdout = &output
		status, err := fixture.suite.run(ctx, request)
		if status != 0 || err != nil {
			return status, err
		}
		var records []map[string]any
		if err := json.Unmarshal(output.Bytes(), &records); err != nil {
			fixture.t.Fatal(err)
		}
		records = append(records, map[string]any{"Names": []string{fixture.name}, "Labels": map[string]string{"io.github.sandboxed-agents.live-suite-group": "live"}})
		return 0, json.NewEncoder(original).Encode(records)
	}
	return fixture.suite.run(ctx, request)
}

func rootProbeOption(args []string, key string) string {
	for index, arg := range args {
		if arg == key && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func TestSelectedImageCoverageRootProbeUsesPinnedReadOnlyImageOnBothPlatforms(t *testing.T) {
	for _, host := range []string{"linux", "windows"} {
		t.Run(host, func(t *testing.T) {
			config, fixture := newRootImageProbeFixture(t, host)
			if host == "windows" {
				for _, key := range []string{"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"} {
					t.Setenv(key, "unchecked-target")
				}
			}
			if err := livesuite.Run(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			if fixture.runs != 1 || fixture.exists || fixture.removes != 0 || fixture.suite.removals != 5 || fixture.suite.builds != 1 || fixture.suite.shells != 7 {
				t.Fatalf("fixture=%+v", fixture)
			}
		})
	}
}

func TestSelectedImageCoverageCleansRootProbeAfterCancellation(t *testing.T) {
	config, fixture := newRootImageProbeFixture(t, "linux")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.cancel = cancel
	if err := livesuite.Run(ctx, config); err == nil {
		t.Fatal("cancelled root probe passed")
	}
	summary := readSummary(t, config, "linux")
	if fixture.runs != 1 || fixture.removes != 1 || fixture.exists || fixture.suite.removals != 5 || !hasCheck(summary, "images/base/host-keys", "fail") || !hasCheck(summary, "images/cleanup", "pass") {
		t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
	}
}

func TestSelectedImageCoverageRejectsIncompleteOrForeignRootProbeCleanup(t *testing.T) {
	for _, failure := range []string{"rm-failure", "retained-probe", "foreign-owner"} {
		t.Run(failure, func(t *testing.T) {
			config, fixture := newRootImageProbeFixture(t, "linux")
			fixture.retain = true
			switch failure {
			case "rm-failure":
				fixture.rmStatus = 42
			case "retained-probe":
				fixture.incomplete = true
			case "foreign-owner":
				fixture.foreign = true
			}
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("unsafe/incomplete root probe cleanup passed")
			}
			summary := readSummary(t, config, "linux")
			if !fixture.exists || fixture.suite.removals != 5 || fixture.suite.builds != 0 || summary.ImageCoverageComplete || !hasCheck(summary, "images/base/host-keys", "fail") || !hasCheck(summary, "images/cleanup", "fail") {
				t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
			}
			if failure == "foreign-owner" && fixture.removes != 0 {
				t.Fatal("foreign root probe removed")
			}
		})
	}
}

func TestSelectedImageCoverageRejectsWindowsTargetChangesAroundRootProbe(t *testing.T) {
	for _, phase := range []string{"before-scan", "before-cleanup"} {
		t.Run(phase, func(t *testing.T) {
			config, fixture := newRootImageProbeFixture(t, "windows")
			fixture.changeBeforeProbe = phase == "before-scan"
			fixture.changeDuringCleanup = phase == "before-cleanup"
			fixture.retain = fixture.changeDuringCleanup
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("changed root probe target passed")
			}
			summary := readSummary(t, config, "windows-11")
			if fixture.removes != 0 || fixture.suite.builds != 0 || !hasCheck(summary, "images/base/host-keys", "fail") || !hasCheck(summary, "images/cleanup", "fail") {
				t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
			}
			if phase == "before-scan" && fixture.runs != 0 {
				t.Fatal("root probe ran against changed target")
			}
			if phase == "before-cleanup" && (!fixture.exists || fixture.runs != 1) {
				t.Fatal("changed target cleanup touched root probe")
			}
		})
	}
}

func TestSelectedImageCoverageExecutesRootScannerAcrossHiddenAndVolumePaths(t *testing.T) {
	for _, test := range []struct {
		name, key, errorPath, errorCode string
		pass                            bool
	}{
		{name: "clean-image", pass: true},
		{name: "nested-root-key", key: "root/.private/nested/ssh_host_ed25519_key"},
		{name: "image-ssh-key", key: "etc/ssh/ssh_host_rsa_key"},
		{name: "image-ssh-public-key", key: "etc/ssh/ssh_host_rsa_key.pub"},
		{name: "other-owner-private-key", key: "home/agent/.ssh/ssh_host_ed25519_key"},
		{name: "tmp-key", key: "tmp/ssh_host_ed25519_key"},
		{name: "run-key", key: "run/private/ssh_host_ed25519_key"},
		{name: "var-tmp-key", key: "var/tmp/ssh_host_ed25519_key"},
		{name: "permission-error", errorPath: "/root", errorCode: "EACCES"},
		{name: "io-error", errorPath: "/etc", errorCode: "EIO"},
		{name: "virtual-proc-key", key: "proc/ssh_host_ed25519_key", pass: true},
		{name: "virtual-sys-key", key: "sys/ssh_host_ed25519_key", pass: true},
		{name: "virtual-dev-key", key: "dev/ssh_host_ed25519_key", pass: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := exec.LookPath("node"); err != nil {
				t.Skip("Node is required for the scanner execution fixture")
			}
			config, fixture := newRootImageProbeFixture(t, "linux")
			directory := t.TempDir()
			for _, path := range []string{"root/.private/nested", "etc/ssh", "home/agent/.ssh", "tmp", "run/private", "var/tmp", "proc", "sys", "dev"} {
				if err := os.MkdirAll(filepath.Join(directory, path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if test.key != "" {
				if err := os.WriteFile(filepath.Join(directory, test.key), []byte("private fixture key"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			fixture.execute = func(request process.Request, args []string) (int, error) {
				readable := slices.Contains(args, "--cap-add=DAC_READ_SEARCH")
				bootstrap := `const realfs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const [script,root,errorPath,errorCode,readable]=process.argv.slice(1);
const fs={readdirSync(directory,options){
  if(directory===errorPath) throw Object.assign(new Error(errorCode),{code:errorCode});
  if(directory==='/home/agent/.ssh'&&readable!=='true') throw Object.assign(new Error('EACCES'),{code:'EACCES'});
  return realfs.readdirSync(path.join(root,directory),options);
}};
vm.runInNewContext(script,{require(name){if(name==='node:fs') return fs;return require(name);},process},{timeout:10000});`
				cmd := exec.Command("node", "-e", bootstrap, args[len(args)-1], directory, test.errorPath, test.errorCode, strconv.FormatBool(readable))
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				if err := cmd.Run(); err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						return exit.ExitCode(), nil
					}
					return 1, err
				}
				return 0, nil
			}
			err := livesuite.Run(context.Background(), config)
			if (err == nil) != test.pass {
				t.Fatalf("pass=%t error=%v", test.pass, err)
			}
			summary := readSummary(t, config, "linux")
			expected := "fail"
			if test.pass {
				expected = "pass"
			}
			if fixture.runs != 1 || fixture.suite.removals != 5 || !hasCheck(summary, "images/base/host-keys", expected) {
				t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
			}
		})
	}
}

func TestSelectedImageCoverageCleansRootProbeWhenItsScanFails(t *testing.T) {
	config, fixture := newRootImageProbeFixture(t, "linux")
	fixture.retain = true
	fixture.runStatus = 42
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("failed root scan passed")
	}
	summary := readSummary(t, config, "linux")
	if fixture.runs != 1 || fixture.removes != 1 || fixture.exists || fixture.suite.removals != 5 || fixture.suite.builds != 0 || !hasCheck(summary, "images/base/host-keys", "fail") || !hasCheck(summary, "images/cleanup", "pass") {
		t.Fatalf("summary=%+v fixture=%+v", summary, fixture)
	}
}
