package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

func azureSelections() []toolchainSelectionExample {
	return []toolchainSelectionExample{
		{"azure", "azure", "azure"},
		{"azure,native", "azure,native", "azure-native"},
		{"native,azure,azure,native", "azure,native", "azure-native"},
	}
}

func TestBuildAzureImageInstallsSystemExtensionAndRecordsVersions(t *testing.T) {
	for _, fixture := range []string{"linux-build", "windows-build"} {
		for _, example := range azureSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				checkAzureBuild(t, fixture, example)
			})
		}
	}
}

func checkAzureBuild(t *testing.T, fixture string, example toolchainSelectionExample) {
	t.Helper()
	fakes, preflight := toolchainCLIHost(t, fixture)
	hash := imageBuildAssetHash(t)
	captured := filepath.Join(t.TempDir(), "azure-context")
	responses := append(preflight,
		testutil.Response{},
		testutil.Response{Stdout: "[]"},
		testutil.Response{Stdout: `[{"Id":"sha256:new-base"}]`},
		testutil.Response{CaptureBuildContext: captured},
	)
	fakes.Script("podman", responses...)
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
	assertAzureBuildContext(t, captured)
	recipe, err := os.ReadFile(filepath.Join(captured, "Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(recipe), "FROM ") != 1 || strings.Count(string(recipe), "RUN sh /tmp/install-azure.sh") != 1 {
		t.Fatalf("recipe must use one base and install Azure once: %s", recipe)
	}
	if strings.Count(string(recipe), "RUN sh /tmp/record-versions.sh") != 1 {
		t.Fatalf("recipe must record versions once after installation: %s", recipe)
	}
	if example.set == "azure,native" {
		assertNativeBuildContext(t, captured)
	} else if strings.Contains(string(recipe), "install-native") {
		t.Fatalf("Azure-only recipe installs native: %s", recipe)
	}
}

func assertAzureBuildContext(t *testing.T, directory string) {
	t.Helper()
	for file, required := range map[string][]string{
		"Containerfile":      {"ARG BASE_IMAGE\nFROM ${BASE_IMAGE}", "COPY azure/install.sh /tmp/install-azure.sh", "sh /tmp/install-azure.sh", "sh /tmp/record-versions.sh"},
		"azure/install.sh":   {"https://packages.microsoft.com/keys/microsoft.asc", "signed-by=/etc/apt/keyrings/microsoft.gpg", "https://packages.microsoft.com/repos/azure-cli/ bookworm main", "apt-get install -y --no-install-recommends azure-cli", "az extension add --system --name azure-devops", "chmod -R a+rX"},
		"azure/record.sh":    {"az version", ".extensions.\"azure-devops\"", "printf 'azure-devops\\t%s\\n'"},
		"record-versions.sh": {"/usr/local/share/sandboxed-agents/versions.tsv", "dpkg-query -W", "versions.d/*.sh", "sh \"$recorder\""},
	} {
		data, err := os.ReadFile(filepath.Join(directory, file))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range required {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s missing %q", file, want)
			}
		}
		if strings.Contains(string(data), "\r") {
			t.Errorf("%s contains CR", file)
		}
	}
}

func TestUpAzureRecordsTheSelectedSetAndCreatesFromItsImage(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, example := range azureSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				checkToolchainCreation(t, fixture, example, assertAzureBuildContext)
			})
		}
	}
}

func TestListShowsRecordedAzureToolchains(t *testing.T) {
	for _, set := range []string{"azure", "azure,native"} {
		t.Run(set, func(t *testing.T) {
			checkListedToolchains(t, set)
		})
	}
}
