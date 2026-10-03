package livesuite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/assets"
	"github.com/grauzone-dev/sandboxed-agents/internal/cli"
	"github.com/grauzone-dev/sandboxed-agents/internal/livesuite"
	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
	"github.com/grauzone-dev/sandboxed-agents/internal/preflight"
	"github.com/grauzone-dev/sandboxed-agents/internal/process"
	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

const commit = "0123456789abcdef0123456789abcdef01234567"

var nativeSummaryPlatform = map[string]string{"linux": "linux", "windows": "windows-11"}[runtime.GOOS]

func TestNormalRunNeedsNoPodmanOrSummary(t *testing.T) {
	fakes := testutil.NewFakePrograms(t)
	dir := filepath.Join(t.TempDir(), "records")
	err := livesuite.Run(context.Background(), livesuite.Config{OutputDirectory: dir, Host: platform.CurrentHost(), Run: platform.Run})
	if err != nil {
		t.Fatal(err)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("normal run called Podman")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("summary created: %v", err)
	}
}

func TestLiveSuiteRefusesDefaultAndInvalidGroupsBeforePodman(t *testing.T) {
	for _, group := range []string{"<unset>", "default", "", "Live", "-live", "live.group", "live/group", "live\n"} {
		t.Run(group, func(t *testing.T) {
			t.Setenv("SANDBOXED_AGENTS_GROUP", group)
			if group == "<unset>" {
				if err := os.Unsetenv("SANDBOXED_AGENTS_GROUP"); err != nil {
					t.Fatal(err)
				}
			}
			fakes := testutil.NewFakePrograms(t)
			config := livesuite.Config{OptIn: true, Commit: commit, OutputDirectory: t.TempDir(), Host: platform.Host{OS: "linux", Architecture: "amd64"}, Run: platform.Run}
			err := livesuite.Run(context.Background(), config)
			if err == nil {
				t.Fatal("unsafe group accepted")
			}
			want := "invalid controller group"
			if group == "<unset>" || group == "default" {
				want = "default"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v want %q", err, want)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("rejected group called Podman")
			}
			data, err := os.ReadFile(filepath.Join(config.OutputDirectory, "live-suite-linux.json"))
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record["commit"] != commit || record["result"] != "fail" || record["image_part_ran"] != false || record["platform"] != "linux" || record["schema_version"] != float64(1) {
				t.Fatalf("record=%s", data)
			}
		})
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("SANDBOXED_AGENTS_LIVE_HOST_FIXTURE") == "1" {
		files := map[string]string{
			"/etc/subuid": "fixture:100000:65536\n", "/etc/subgid": "fixture:200000:65536\n",
			"/proc/self/cgroup":    "0::/user.slice/user-1000.slice/session-3.scope\n",
			"/proc/self/mountinfo": fmt.Sprintf("32 24 0:28 / %s rw - cgroup2 cgroup rw\n", filepath.Clean("/sys/fs/cgroup")),
			"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.controllers":     "cpu memory pids\n",
			"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.procs":           "",
			"/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/cgroup.subtree_control": "cpu memory pids\n",
		}
		host := preflight.Host{Platform: "linux", UID: 1000, Username: "fixture", Run: platform.Run,
			LookPath: func(name string) (string, error) { return name, nil },
			ReadFile: func(path string) ([]byte, error) {
				value, ok := files[filepath.ToSlash(filepath.Clean(path))]
				if !ok {
					return nil, os.ErrNotExist
				}
				return []byte(value), nil
			},
			Writable: func(string) bool { return true },
		}
		os.Exit(cli.RunWithHost(os.Args[1:], os.Stdout, os.Stderr, commit, assets.Hash(), host))
	}
	os.Exit(m.Run())
}

func fixtureConfig(t *testing.T) livesuite.Config {
	t.Helper()
	t.Setenv("SANDBOXED_AGENTS_GROUP", "live")
	t.Setenv("SANDBOXED_AGENTS_LIVE_HOST_FIXTURE", "1")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "live-host")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	return livesuite.Config{OptIn: true, Commit: commit, Repository: t.TempDir(), OutputDirectory: t.TempDir(), Host: platform.Host{OS: runtime.GOOS, Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}, Run: func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "git" {
			return 0, nil
		}
		if request.Name == "go" {
			data, err := os.ReadFile(executable)
			if err != nil {
				return 1, err
			}
			return 0, os.WriteFile(request.Args[len(request.Args)-1], data, 0700)
		}
		return platform.Run(ctx, request)
	}}
}

func readSummary(t *testing.T, config livesuite.Config, platformName string) livesuite.Summary {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(config.OutputDirectory, "live-suite-"+platformName+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary livesuite.Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	return summary
}

func TestLiveRunReachesPodmanWithoutRebuildingSharedImages(t *testing.T) {
	config := fixtureConfig(t)
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"})
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	want := []testutil.Call{{Args: []string{"ps", "--all", "--format", "json"}}, {Args: []string{"volume", "ls", "--format", "json"}}}
	if calls := fakes.Calls("podman"); !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v", calls)
	}
	summary := readSummary(t, config, nativeSummaryPlatform)
	if summary.Commit != commit || summary.Result != "pass" || summary.ImagePartSelected || summary.ImagePartRan || summary.Kind != "live-suite" {
		t.Fatalf("summary=%+v", summary)
	}
	if !reflect.DeepEqual(summary.Checks, []livesuite.Check{{Name: "build-host", Result: "pass"}, {Name: "version", Result: "pass"}, {Name: "list", Result: "pass"}}) {
		t.Fatalf("checks=%v", summary.Checks)
	}
}

func TestSelectedImagePartRebuildsOnceAndRecordsItsOutcome(t *testing.T) {
	for _, status := range []int{0, 42} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			config := fixtureConfig(t)
			config.Images = true
			config.Host = platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
			fakes := testutil.NewFakePrograms(t)
			fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "podman version 5.0.0\n"}, testutil.Response{ExitCode: status}, testutil.Response{Stdout: "[]"})
			err := livesuite.Run(context.Background(), config)
			if (err == nil) != (status == 0) {
				t.Fatalf("error=%v status=%d", err, status)
			}
			builds := 0
			for _, call := range fakes.Calls("podman") {
				if len(call.Args) > 0 && call.Args[0] == "build" {
					builds++
				}
			}
			if builds != 1 {
				t.Fatalf("builds=%d calls=%v", builds, fakes.Calls("podman"))
			}
			summary := readSummary(t, config, "windows-11")
			want := "pass"
			if status != 0 {
				want = "fail"
			}
			if summary.Result != want || summary.ImagePartRan != (status == 0) || !summary.ImagePartSelected || summary.Platform != "windows-11" || summary.ImageCoverageComplete {
				t.Fatalf("summary=%+v", summary)
			}
			if last := summary.Checks[len(summary.Checks)-1]; last.Name != "images" || last.Result != want {
				t.Fatalf("check=%+v", last)
			}
		})
	}
}

func TestFailedSummaryOmitsHostDetailsAndCredentials(t *testing.T) {
	config := fixtureConfig(t)
	config.Images = true
	var console bytes.Buffer
	config.Stderr = &console
	secrets := []string{"private-user", "private-host", "/home/private-user/workspace", "C:\\Users\\private-user", "SANDBOXED_AGENTS_GROUP=live", "ghp_secret", "PRIVATE KEY", "ssh-agent01.live", "SHA256:secret", "http://private.example:7777"}
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{ExitCode: 42, Stderr: strings.Join(secrets, "\n")})
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("Podman failure passed")
	}
	summary := readSummary(t, config, nativeSummaryPlatform)
	if summary.Result != "fail" || summary.ImagePartRan || !summary.ImagePartSelected {
		t.Fatalf("summary=%+v", summary)
	}
	data, err := os.ReadFile(filepath.Join(config.OutputDirectory, "live-suite-"+nativeSummaryPlatform+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range secrets {
		if !strings.Contains(console.String(), secret) {
			t.Fatalf("fixture diagnostic missing %q", secret)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("summary leaked %q: %s", secret, data)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 9 {
		t.Fatalf("unexpected summary fields: %s", data)
	}
}

func TestRejectedRunReplacesAnEarlierPassingSummary(t *testing.T) {
	config := fixtureConfig(t)
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"})
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDBOXED_AGENTS_GROUP", "default")
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("default accepted")
	}
	if summary := readSummary(t, config, nativeSummaryPlatform); summary.Result != "fail" || len(summary.Checks) != 0 {
		t.Fatalf("stale summary=%+v", summary)
	}
	if len(fakes.Calls("podman")) != 2 {
		t.Fatal("rejected run called Podman")
	}
}

func TestCannotStartPodmanWithoutAValidFailingRecord(t *testing.T) {
	config := fixtureConfig(t)
	fakes := testutil.NewFakePrograms(t)
	config.OutputDirectory = filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(config.OutputDirectory, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("unwritable summary accepted")
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("Podman called without a record")
	}
}

func TestLiveSuiteRejectsDirtySourcesAndFailedCompilation(t *testing.T) {
	for _, failure := range []string{"dirty", "compile"} {
		t.Run(failure, func(t *testing.T) {
			config := fixtureConfig(t)
			fakes := testutil.NewFakePrograms(t)
			config.Run = func(_ context.Context, request process.Request) (int, error) {
				if request.Name == "git" {
					if failure == "dirty" {
						fmt.Fprint(request.Streams.Stdout, " M source.go\n")
					}
					return 0, nil
				}
				if request.Name == "go" {
					return 42, nil
				}
				t.Fatalf("unexpected process=%s %v", request.Name, request.Args)
				return 1, nil
			}
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("preparation failure passed")
			}
			if summary := readSummary(t, config, nativeSummaryPlatform); summary.Result != "fail" || summary.ImagePartRan {
				t.Fatalf("summary=%+v", summary)
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("preparation failure called Podman")
			}
		})
	}
}

func TestMismatchedExecutableCannotProducePassingValidation(t *testing.T) {
	config := fixtureConfig(t)
	config.Commit = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	fakes := testutil.NewFakePrograms(t)
	if err := livesuite.Run(context.Background(), config); err == nil {
		t.Fatal("mismatched binary passed")
	}
	summary := readSummary(t, config, nativeSummaryPlatform)
	if summary.Commit != config.Commit || summary.Result != "fail" || !reflect.DeepEqual(summary.Checks, []livesuite.Check{{Name: "build-host", Result: "pass"}, {Name: "version", Result: "fail"}}) {
		t.Fatalf("summary=%+v", summary)
	}
	if len(fakes.Calls("podman")) != 0 {
		t.Fatal("mismatched binary reached Podman")
	}
}

func TestUnsupportedHostsCannotStartLiveChecks(t *testing.T) {
	for _, host := range []platform.Host{
		{OS: "linux", Architecture: "arm64"},
		{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 19045, WindowsWorkstation: true},
		{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 26100, WindowsWorkstation: false},
		{OS: "windows", Architecture: "arm64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true},
		{OS: "darwin", Architecture: "amd64"},
	} {
		t.Run(fmt.Sprintf("%+v", host), func(t *testing.T) {
			config := fixtureConfig(t)
			config.Host = host
			fakes := testutil.NewFakePrograms(t)
			if err := livesuite.Run(context.Background(), config); err == nil {
				t.Fatal("unsupported host passed")
			}
			if len(fakes.Calls("podman")) != 0 {
				t.Fatal("unsupported host reached Podman")
			}
		})
	}
}

func TestDefaultRunBuildsTheCommitBeforeItReachesPodman(t *testing.T) {
	config := fixtureConfig(t)
	runner := config.Run
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "go" {
			want := []string{"-C", config.Repository, "run", "./tools/build", "-version", commit, "-output"}
			if !reflect.DeepEqual(request.Args[:len(request.Args)-1], want) {
				t.Fatalf("build=%v", request.Args)
			}
		}
		return runner(ctx, request)
	}
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"})
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	summary := readSummary(t, config, nativeSummaryPlatform)
	if summary.Result != "pass" || len(summary.Checks) != 3 || summary.Checks[0].Name != "build-host" {
		t.Fatalf("summary=%+v", summary)
	}
	if len(fakes.Calls("podman")) != 2 {
		t.Fatalf("calls=%v", fakes.Calls("podman"))
	}
}

func TestCustomSummaryDirectoryDoesNotMakeTheSourceDirty(t *testing.T) {
	config := fixtureConfig(t)
	config.OutputDirectory = filepath.Join(config.Repository, "records")
	runner := config.Run
	config.Run = func(ctx context.Context, request process.Request) (int, error) {
		if request.Name == "git" {
			want := []string{"-C", config.Repository, "status", "--porcelain", "--untracked-files=all", "--", ".", ":(exclude,literal)records/live-suite-" + nativeSummaryPlatform + ".json"}
			if !reflect.DeepEqual(request.Args, want) {
				t.Fatalf("dirty check hides more than the generated record: %v", request.Args)
			}
		}
		return runner(ctx, request)
	}
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"})
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if summary := readSummary(t, config, nativeSummaryPlatform); summary.Result != "pass" || summary.ImageCoverageComplete {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestWindowsOutputOnAnotherDriveStillRuns(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows volume path handling")
	}
	config := fixtureConfig(t)
	config.Host = platform.Host{OS: "windows", Architecture: "amd64", WindowsMajor: 10, WindowsBuild: 22631, WindowsWorkstation: true}
	config.Repository = `D:\source`
	if strings.EqualFold(filepath.VolumeName(config.OutputDirectory), "D:") {
		config.Repository = `C:\source`
	}
	fakes := testutil.NewFakePrograms(t)
	fakes.Script("podman", testutil.Response{Stdout: "[]"}, testutil.Response{Stdout: "[]"})
	if err := livesuite.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if summary := readSummary(t, config, "windows-11"); summary.Result != "pass" {
		t.Fatalf("summary=%+v", summary)
	}
}
