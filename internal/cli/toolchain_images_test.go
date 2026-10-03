package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func TestBuildSelectedNativeImageUsesFreshBaseAndRemovesItsContext(t *testing.T) {
	fakes := linuxHost(t)
	hash := imageBuildAssetHash(t)
	captured := filepath.Join(t.TempDir(), "native-context")
	fakes.Script("podman",
		testutil.Response{Stdout: "podman version 5.0.0\n"},
		testutil.Response{},
		testutil.Response{Stdout: "[]"},
		testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
		testutil.Response{CaptureBuildContext: captured},
	)
	stdout, stderr, status := runCLI(t, "linux-build", "build", "--with", "native")
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	baseTag := "localhost/sandboxed-agents:base-" + hash
	nativeTag := "localhost/sandboxed-agents:toolchains-native-" + hash
	calls := fakes.Calls("podman")
	if len(calls) != 5 {
		t.Fatalf("calls=%v", calls)
	}
	assertImageBuild(t, calls[1].Args, hash, baseTag, "", "")
	assertImageDiscovery(t, calls[2].Args, hash)
	if !reflect.DeepEqual(calls[3].Args, []string{"image", "inspect", baseTag}) {
		t.Fatalf("base inspection=%v", calls[3].Args)
	}
	assertImageBuild(t, calls[4].Args, hash, nativeTag, "native", "sha256:new-base")
	assertBuildReminder(t, stdout)
	for _, tag := range []string{baseTag, nativeTag} {
		if !strings.Contains(stdout, "Built image "+tag) {
			t.Fatalf("missing built image %q in %q", tag, stdout)
		}
	}
	assertNativeBuildContext(t, captured)
}

func imageBuildAssetHash(t *testing.T) string {
	t.Helper()
	stdout, stderr, status := runCLI(t, "linux-build", "version")
	if status != 0 || stderr != "" {
		t.Fatalf("version status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	_, hash, ok := strings.Cut(stdout, "assets ")
	hash = strings.TrimSpace(hash)
	if !ok || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Fatalf("asset hash=%q", hash)
	}
	return hash
}

func assertImageDiscovery(t *testing.T, args []string, hash string) {
	t.Helper()
	want := []string{"images", "--filter", "label=io.github.sandboxed-agents.managed=true", "--filter", "label=io.github.sandboxed-agents.asset-hash=" + hash, "--format", "json"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("image discovery=%v want=%v", args, want)
	}
}

func assertImageBuild(t *testing.T, args []string, hash, tag, selection, baseID string, extraTags ...string) {
	t.Helper()
	if len(args) == 0 {
		t.Fatal("empty image build")
	}
	directory := args[len(args)-1]
	want := []string{"build", "--pull=always", "--no-cache"}
	if selection != "" {
		want = []string{"build", "--pull=never", "--no-cache", "--build-arg", "BASE_IMAGE=" + baseID}
	}
	for _, imageTag := range append([]string{tag}, extraTags...) {
		want = append(want, "--tag", imageTag)
	}
	want = append(want,
		"--label", "io.github.sandboxed-agents.managed=true",
		"--label", "io.github.sandboxed-agents.asset-hash="+hash,
		"--label", "io.github.sandboxed-agents.toolchains="+selection)
	if selection != "" {
		want = append(want, "--label", "io.github.sandboxed-agents.base-image="+baseID)
	}
	want = append(want, "--file", filepath.Join(directory, "Containerfile"), directory)
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("build args=%v want=%v", args, want)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("temporary build context remains at %q: %v", directory, err)
	}
}

func TestBuildRebuildsDiscoveredCurrentImagesOnceUnderTheirExistingTags(t *testing.T) {
	for _, args := range [][]string{{"build"}, {"build", "--with", "none"}, {"build", "--with", "native"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			hash := imageBuildAssetHash(t)
			nativeTag := "localhost/sandboxed-agents:toolchains-native-" + hash
			alias := "localhost/custom-native:preserved"
			fakes.Script("podman",
				testutil.Response{Stdout: "podman version 5.0.0\n"},
				testutil.Response{},
				testutil.Response{Stdout: `[{"Id":"native-current"},{"Id":"old-executable"},{"Id":"new-base"},{"Id":"native-current"}]`},
				imageInspection(t, "native-current", hash, "native", nativeTag, alias),
				imageInspection(t, "old-executable", "previous-assets", "native", "localhost/old-native:preserved"),
				imageInspection(t, "new-base", hash, "", "localhost/sandboxed-agents:base-"+hash),
				testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
				testutil.Response{},
			)
			stdout, stderr, status := runCLI(t, "linux-build", args...)
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != 8 {
				t.Fatalf("expected base plus one native rebuild: calls=%v", calls)
			}
			assertImageBuild(t, calls[1].Args, hash, "localhost/sandboxed-agents:base-"+hash, "", "")
			assertImageDiscovery(t, calls[2].Args, hash)
			for index, id := range []string{"native-current", "old-executable", "new-base"} {
				if !reflect.DeepEqual(calls[index+3].Args, []string{"image", "inspect", id}) {
					t.Fatalf("image inspection=%v", calls[index+3].Args)
				}
			}
			assertImageBuild(t, calls[7].Args, hash, alias, "native", "sha256:new-base", nativeTag)
			assertBuildReminder(t, stdout)
			if strings.Count(stdout, "Built image "+nativeTag) != 1 || !strings.Contains(stdout, "Built image "+alias) || strings.Contains(stdout, "Built image localhost/old-native") {
				t.Fatalf("rebuilt image output=%q", stdout)
			}
		})
	}
}

func TestBuildNativeSelectionSharesItsTagAcrossRepetitionsAndControllerGroups(t *testing.T) {
	fakes := linuxHost(t)
	hash := imageBuildAssetHash(t)
	var responses []testutil.Response
	for range 3 {
		responses = append(responses,
			testutil.Response{Stdout: "podman version 5.0.0\n"},
			testutil.Response{},
			testutil.Response{Stdout: "[]"},
			testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
			testutil.Response{},
		)
	}
	fakes.Script("podman", responses...)
	for index, selection := range []string{"native", "native,native", "native,native,native"} {
		t.Setenv("SANDBOXED_AGENTS_GROUP", []string{"default", "second-group", "third-group"}[index])
		stdout, stderr, status := runCLI(t, "linux-build", "build", "--with", selection)
		if status != 0 || stderr != "" {
			t.Fatalf("selection=%q status=%d stdout=%q stderr=%q", selection, status, stdout, stderr)
		}
		calls := fakes.Calls("podman")
		if len(calls) != 5*(index+1) {
			t.Fatalf("calls=%v", calls)
		}
		assertImageBuild(t, calls[5*index+4].Args, hash, "localhost/sandboxed-agents:toolchains-native-"+hash, "native", "sha256:new-base")
	}
}

func TestBuildWithoutToolchainsAndExplicitNoneUseTheSameBaseImage(t *testing.T) {
	for _, args := range [][]string{{"build"}, {"build", "--with", "none"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			fakes := linuxHost(t)
			hash := imageBuildAssetHash(t)
			fakes.Script("podman",
				testutil.Response{Stdout: "podman version 5.0.0\n"},
				testutil.Response{},
				testutil.Response{Stdout: "[]"},
			)
			stdout, stderr, status := runCLI(t, "linux-build", args...)
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			calls := fakes.Calls("podman")
			if len(calls) != 3 {
				t.Fatalf("calls=%v", calls)
			}
			assertImageBuild(t, calls[1].Args, hash, "localhost/sandboxed-agents:base-"+hash, "", "")
			assertImageDiscovery(t, calls[2].Args, hash)
			assertBuildReminder(t, stdout)
		})
	}
}

func TestBuildBaseFailureStopsBeforeToolchainDiscoveryOrBuild(t *testing.T) {
	fakes := linuxHost(t)
	hash := imageBuildAssetHash(t)
	fakes.Script("podman",
		testutil.Response{Stdout: "podman version 5.0.0\n"},
		testutil.Response{Stderr: "base build failed\n", ExitCode: 42},
	)
	stdout, stderr, status := runCLI(t, "linux-build", "build", "--with", "native")
	if status == 0 || !strings.Contains(stderr, "base build failed") || !strings.Contains(stderr, "exit status 42") || strings.Contains(stdout, "Built image") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")
	if len(calls) != 2 || !reflect.DeepEqual(calls[0].Args, []string{"--version"}) {
		t.Fatalf("calls=%v", calls)
	}
	assertImageBuild(t, calls[1].Args, hash, "localhost/sandboxed-agents:base-"+hash, "", "")
}

func TestBuildToolchainFailureKeepsBuildingOtherImagesAndRemovesContexts(t *testing.T) {
	fakes := linuxHost(t)
	hash := imageBuildAssetHash(t)
	firstTag := "localhost/sandboxed-agents:a-native-" + hash
	secondTag := "localhost/sandboxed-agents:b-native-" + hash
	firstContext := filepath.Join(t.TempDir(), "failed-native-context")
	secondContext := filepath.Join(t.TempDir(), "successful-native-context")
	fakes.Script("podman",
		testutil.Response{Stdout: "podman version 5.0.0\n"},
		testutil.Response{},
		testutil.Response{Stdout: `[{"Id":"first-native"},{"Id":"second-native"}]`},
		imageInspection(t, "first-native", hash, "native", firstTag),
		imageInspection(t, "second-native", hash, "native", secondTag),
		testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
		testutil.Response{Stderr: "native compiler download failed\n", ExitCode: 43, CaptureBuildContext: firstContext},
		testutil.Response{Stdout: "second image built\n", CaptureBuildContext: secondContext},
	)
	stdout, stderr, status := runCLI(t, "linux-build", "build", "--with", "native")
	if status == 0 || !strings.Contains(stderr, "native compiler download failed") || !strings.Contains(stderr, "Failed toolchain sets: native") || !strings.Contains(stderr, "exit status 43") {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	if strings.Contains(stdout, "Built image "+firstTag) || !strings.Contains(stdout, "Built image "+secondTag) {
		t.Fatalf("partial build output=%q", stdout)
	}
	assertBuildReminder(t, stdout)
	calls := fakes.Calls("podman")
	if len(calls) != 8 {
		t.Fatalf("remaining toolchain image was not built: calls=%v", calls)
	}
	assertImageBuild(t, calls[6].Args, hash, firstTag, "native", "sha256:new-base")
	assertImageBuild(t, calls[7].Args, hash, secondTag, "native", "sha256:new-base")
	assertNativeBuildContext(t, firstContext)
	assertNativeBuildContext(t, secondContext)
	for _, call := range calls {
		if len(call.Args) > 0 && (call.Args[0] == "rmi" || call.Args[0] == "tag" || (call.Args[0] == "image" && len(call.Args) > 1 && (call.Args[1] == "rm" || call.Args[1] == "tag"))) {
			t.Fatalf("build removed or retagged an image: %v", call.Args)
		}
	}
}

func assertBuildReminder(t *testing.T, stdout string) {
	t.Helper()
	if !strings.Contains(stdout, "Existing sandboxes keep their current image until you update") || !strings.Contains(stdout, "list marks them as outdated") {
		t.Fatalf("missing sandbox update reminder: %q", stdout)
	}
}

func assertNativeBuildContext(t *testing.T, directory string) {
	t.Helper()
	for file, required := range map[string][]string{
		"Containerfile":      {"ARG BASE_IMAGE", "FROM ${BASE_IMAGE}", "sh /tmp/install-native.sh", "sh /tmp/record-versions.sh", "COPY smoke.sh /usr/local/share/sandboxed-agents/smoke/native.sh"},
		"install.sh":         {"apt-get install -y --no-install-recommends", "build-essential", "cmake", "pkg-config", "ninja-build"},
		"record-versions.sh": {"/usr/local/share/sandboxed-agents/versions.tsv", "dpkg-query -W"},
		"smoke.sh":           {"id -u", "id -g", "1000", "int main(void)", "cc ", "trap 'rm -rf"},
	} {
		contents, err := os.ReadFile(filepath.Join(directory, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(contents), "\r\n") {
			t.Errorf("native recipe %s contains CRLF", file)
		}
		for _, text := range required {
			if !strings.Contains(string(contents), text) {
				t.Errorf("native recipe %s missing %q", file, text)
			}
		}
	}
}

type buildImageRecord struct {
	ID       string            `json:"Id"`
	RepoTags []string          `json:"RepoTags"`
	Labels   map[string]string `json:"Labels"`
}

func imageInspection(t *testing.T, id, hash, selection string, tags ...string) testutil.Response {
	t.Helper()
	record := buildImageRecord{ID: id, RepoTags: tags, Labels: map[string]string{
		"io.github.sandboxed-agents.managed":    "true",
		"io.github.sandboxed-agents.asset-hash": hash,
		"io.github.sandboxed-agents.toolchains": selection,
		"io.github.sandboxed-agents.base-image": "sha256:old-base",
	}}
	data, err := json.Marshal([]buildImageRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	return testutil.Response{Stdout: string(data)}
}
