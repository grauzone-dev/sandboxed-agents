package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func playwrightSelections() []toolchainSelectionExample {
	return []toolchainSelectionExample{
		{"playwright", "playwright", "playwright"},
		{"playwright,native", "native,playwright", "native-playwright"},
		{"native,playwright,playwright,native", "native,playwright", "native-playwright"},
		{"playwright,dotnet,azure,native", "azure,dotnet,native,playwright", "azure-dotnet-native-playwright"},
	}
}

func TestBuildPlaywrightImageInstallsBrowsersAndRecordsVersions(t *testing.T) {
	for _, fixture := range []string{"linux-build", "windows-build"} {
		for _, example := range playwrightSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				checkPlaywrightBuild(t, fixture, example)
			})
		}
	}
}

func checkPlaywrightBuild(t *testing.T, fixture string, example toolchainSelectionExample) {
	t.Helper()
	fakes, preflight := toolchainCLIHost(t, fixture)
	hash := imageBuildAssetHash(t)
	captured := filepath.Join(t.TempDir(), "playwright-context")
	fakes.Script("podman", append(preflight,
		testutil.Response{},
		testutil.Response{Stdout: "[]"},
		testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
		testutil.Response{CaptureBuildContext: captured},
	)...)
	stdout, stderr, status := runCLI(t, fixture, "build", "--with", example.selection)
	if status != 0 || stderr != "" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
	}
	calls := fakes.Calls("podman")[len(preflight):]
	if fixture == "windows-build" {
		calls = windowsOperationCalls(t, calls, "podman-machine-default")
	}
	if len(calls) != 4 {
		t.Fatalf("calls=%v", calls)
	}
	assertImageBuild(t, calls[0].Args, hash, "localhost/sandboxed-agents:base-"+hash, "", "")
	assertImageBuild(t, calls[3].Args, hash, "localhost/sandboxed-agents:toolchains-"+example.tagNames+"-"+hash, example.set, "sha256:new-base")
	assertPlaywrightBuildContext(t, captured)
	if strings.Contains(example.set, "native") {
		assertNativeBuildContext(t, captured)
	}
	if strings.Contains(example.set, "azure") {
		assertAzureBuildContext(t, captured)
	}
	if strings.Contains(example.set, "dotnet") {
		assertDotnetBuildContext(t, captured)
	}
	data, err := os.ReadFile(filepath.Join(captured, "Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	recipe := string(data)
	if strings.Count(recipe, "FROM ") != 1 || strings.Count(recipe, "RUN sh /tmp/install-playwright.sh") != 1 || strings.Count(recipe, "RUN sh /tmp/record-versions.sh") != 1 {
		t.Fatalf("recipe must use one base, one Playwright installation, and one inventory: %s", recipe)
	}
	if strings.LastIndex(recipe, "install-") > strings.Index(recipe, "RUN sh /tmp/record-versions.sh") {
		t.Fatalf("versions recorded before installation: %s", recipe)
	}
	if _, err := os.Stat(calls[3].Args[len(calls[3].Args)-1]); !os.IsNotExist(err) {
		t.Fatalf("build context remains: %v", err)
	}
}

func assertPlaywrightBuildContext(t *testing.T, directory string) {
	t.Helper()
	for file, required := range map[string][]string{
		"Containerfile":         {"ARG BASE_IMAGE\nFROM ${BASE_IMAGE}", "COPY playwright/install.sh /tmp/install-playwright.sh", "sh /tmp/install-playwright.sh", "ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers", "COPY playwright/cli.sh /usr/local/bin/playwright", "COPY playwright/record.sh /usr/local/share/sandboxed-agents/versions.d/playwright.sh", "COPY playwright/record.cjs /usr/local/share/sandboxed-agents/record-playwright.cjs", "COPY playwright/smoke.sh /usr/local/share/sandboxed-agents/smoke/playwright.sh", "COPY playwright/smoke.cjs /usr/local/share/sandboxed-agents/smoke/playwright.cjs", "sh /tmp/record-versions.sh"},
		"playwright/install.sh": {"@playwright/test@1.63.0", "--prefix /opt/playwright", "install --with-deps chromium firefox webkit", "chmod -R a+rX /opt/playwright /opt/playwright-browsers"},
		"playwright/record.sh":  {"node /usr/local/share/sandboxed-agents/record-playwright.cjs"},
		"playwright/record.cjs": {"browsers.json", "browserVersion", "revision", "playwright"},
		"playwright/smoke.sh":   {`test "$(id -u)" = 1000`, `test "$(id -g)" = 1000`, "node /usr/local/share/sandboxed-agents/smoke/playwright.cjs"},
		"playwright/smoke.cjs":  {"chromium", "firefox", "webkit", ".launch(", ".newPage(", ".setContent(", ".close("},
		"playwright/cli.sh":     {"PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers", `"$@"`},
		"record-versions.sh":    {"versions.tsv", "versions.d/*.sh"},
	} {
		data, err := os.ReadFile(filepath.Join(directory, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\r") {
			t.Errorf("%s contains CR", file)
		}
		for _, want := range required {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q", file, want)
			}
		}
	}
}

func TestUpPlaywrightRecordsTheSelectedSetAndCreatesFromItsImage(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, example := range playwrightSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				checkToolchainCreation(t, fixture, example, assertPlaywrightBuildContext)
			})
		}
	}
}

func TestListShowsRecordedPlaywrightToolchains(t *testing.T) {
	for _, set := range []string{"playwright", "native,playwright", "azure,dotnet,native,playwright"} {
		t.Run(set, func(t *testing.T) {
			checkListedToolchains(t, set)
		})
	}
}

func TestBuildWithoutPlaywrightInstallsNoBrowsers(t *testing.T) {
	for _, selection := range []string{"none", "native", "azure", "dotnet", "azure,dotnet,native"} {
		t.Run(selection, func(t *testing.T) {
			fakes := linuxHost(t)
			baseContext := filepath.Join(t.TempDir(), "base-context")
			setContext := filepath.Join(t.TempDir(), "set-context")
			responses := []testutil.Response{
				{Stdout: "podman version 5.0.0\n"},
				{CaptureBuildContext: baseContext},
				{Stdout: "[]"},
			}
			if selection != "none" {
				responses = append(responses,
					testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
					testutil.Response{CaptureBuildContext: setContext},
				)
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "linux-build", "build", "--with", selection)
			if status != 0 || stderr != "" {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			directories := []string{baseContext}
			if selection != "none" {
				directories = append(directories, setContext)
			}
			for _, directory := range directories {
				err := filepath.WalkDir(directory, func(file string, entry os.DirEntry, err error) error {
					if err != nil || entry.IsDir() || entry.Name() == "manager" {
						return err
					}
					data, err := os.ReadFile(file)
					if err != nil {
						return err
					}
					for _, forbidden := range []string{"playwright", "chromium", "firefox", "webkit"} {
						if strings.Contains(string(data), forbidden) {
							t.Errorf("recipe without Playwright %s contains %q", file, forbidden)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(directory, "playwright")); !os.IsNotExist(err) {
					t.Fatalf("context without Playwright contains its recipe: %v", err)
				}
			}
		})
	}
}
