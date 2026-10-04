package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grauzone-dev/sandboxed-agents/internal/testutil"
)

type dotnetSelectionExample struct{ selection, set, tagNames string }

func dotnetSelections() []dotnetSelectionExample {
	return []dotnetSelectionExample{
		{"dotnet", "dotnet", "dotnet"},
		{"dotnet,native", "dotnet,native", "dotnet-native"},
		{"native,dotnet,dotnet,native", "dotnet,native", "dotnet-native"},
		{"dotnet,azure", "azure,dotnet", "azure-dotnet"},
		{"native,azure,dotnet", "azure,dotnet,native", "azure-dotnet-native"},
	}
}

func TestBuildDotnetImageInstallsSDKsAndRecordsVersions(t *testing.T) {
	for _, fixture := range []string{"linux-build", "windows-build"} {
		for _, example := range dotnetSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				checkDotnetBuild(t, fixture, example)
			})
		}
	}
}

func checkDotnetBuild(t *testing.T, fixture string, example dotnetSelectionExample) {
	t.Helper()
	fakes, preflight := toolchainCLIHost(t, fixture)
	hash := imageBuildAssetHash(t)
	captured := filepath.Join(t.TempDir(), "dotnet-context")
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
	assertDotnetBuildContext(t, captured)
	recipe, err := os.ReadFile(filepath.Join(captured, "Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(recipe), "FROM ") != 1 || strings.Count(string(recipe), "RUN sh /tmp/install-dotnet.sh") != 1 {
		t.Fatalf("recipe must use one base and install .NET once: %s", recipe)
	}
	if strings.Count(string(recipe), "RUN sh /tmp/record-versions.sh") != 1 {
		t.Fatalf("recipe must record versions once after installation: %s", recipe)
	}
	if strings.Contains(example.set, "native") {
		assertNativeBuildContext(t, captured)
	} else if strings.Contains(string(recipe), "install-native") {
		t.Fatalf(".NET-only recipe installs native: %s", recipe)
	}
	if strings.Contains(example.set, "azure") {
		assertAzureBuildContext(t, captured)
	}
	if strings.LastIndex(string(recipe), "install-") > strings.Index(string(recipe), "RUN sh /tmp/record-versions.sh") {
		t.Fatalf("versions recorded before installation: %s", recipe)
	}
}

func assertDotnetBuildContext(t *testing.T, directory string) {
	t.Helper()
	for file, required := range map[string][]string{
		"Containerfile":      {"ARG BASE_IMAGE\nFROM ${BASE_IMAGE}", "COPY dotnet/install.sh /tmp/install-dotnet.sh", "sh /tmp/install-dotnet.sh", "sh /tmp/record-versions.sh"},
		"dotnet/install.sh":  {"https://packages.microsoft.com/keys/microsoft.asc", "signed-by=/etc/apt/keyrings/microsoft-dotnet.gpg", "https://packages.microsoft.com/debian/12/prod bookworm main", "apt-get install -y --no-install-recommends", "dotnet-sdk-8.0", "dotnet-sdk-9.0", "dotnet-sdk-10.0", "chmod 0644 /etc/apt/keyrings/microsoft-dotnet.gpg"},
		"record-versions.sh": {"/usr/local/share/sandboxed-agents/versions.tsv", `dpkg-query -W -f='${binary:Package}\t${Version}\n'`},
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
func TestUpDotnetRecordsTheSelectedSetAndCreatesFromItsImage(t *testing.T) {
	for _, fixture := range []string{"linux-preflight", "windows"} {
		for _, example := range dotnetSelections() {
			t.Run(fixture+"/"+example.selection, func(t *testing.T) {
				fakes, preflight := toolchainCLIHost(t, fixture)
				captured := filepath.Join(t.TempDir(), "up-context")
				responses := append(preflight, upObjectResponses(nil, false, nil, nil)[1:]...)
				responses = append(responses,
					testutil.Response{},
					testutil.Response{Stdout: `[{"Id":"sha256:current-base"}]`},
					testutil.Response{ExitCode: 1},
					testutil.Response{CaptureBuildContext: captured},
					testutil.Response{Stdout: `[{"Id":"sha256:dotnet-set","Labels":{"io.github.sandboxed-agents.base-image":"sha256:current-base"}}]`},
				)
				responses = append(responses, make([]testutil.Response, 5)...)
				fakes.Script("podman", responses...)
				stdout, stderr, status := runCLI(t, fixture, "up", "agent01", "--with", example.selection)
				if status != 0 || stderr != "" || !strings.Contains(stdout, "Sandbox agent01 is running") {
					t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
				}
				calls := fakes.Calls("podman")[len(preflight):]
				if fixture == "windows" {
					calls = windowsOperationCalls(t, calls, "podman-machine-default")
				}
				var build, create []string
				for _, call := range calls {
					if call.Args[0] == "build" {
						build = call.Args
					}
					if call.Args[0] == "create" {
						create = call.Args
					}
				}
				assertImageBuild(t, build, "fixture-assets", "localhost/sandboxed-agents:toolchains-"+example.tagNames+"-fixture-assets", example.set, "sha256:current-base")
				if len(create) == 0 || create[len(create)-1] != "sha256:dotnet-set" || !strings.Contains(strings.Join(create, " "), "--label io.github.sandboxed-agents.toolchains="+example.set) {
					t.Fatalf("create=%v", create)
				}
				assertDotnetBuildContext(t, captured)
			})
		}
	}
}

func TestListShowsRecordedDotnetToolchains(t *testing.T) {
	for _, set := range []string{"dotnet", "dotnet,native"} {
		t.Run(set, func(t *testing.T) {
			fakes := testutil.NewFakePrograms(t)
			owner := "default"
			responses := listOneSandboxResponses("default", "agent01", &owner, true, map[string]string{"workspace": owner}, nil)
			for index := range responses {
				responses[index].Stdout = strings.ReplaceAll(responses[index].Stdout, `"io.github.sandboxed-agents.workspace-kind":"volume"`, fmt.Sprintf(`"io.github.sandboxed-agents.workspace-kind":"volume","io.github.sandboxed-agents.toolchains":%q`, set))
			}
			fakes.Script("podman", responses...)
			stdout, stderr, status := runCLI(t, "sandbox-host", "list")
			if status != 0 || stderr != "" || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "agent01 running volume - "+set+" -") {
				t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout, stderr)
			}
			assertListReadOnly(t, fakes, false)
		})
	}
}

func TestBuildWithoutDotnetInstallsNoSDKs(t *testing.T) {
	for _, selection := range []string{"none", "native", "azure", "azure,native"} {
		t.Run(selection, func(t *testing.T) {
			fakes := linuxHost(t)
			baseContext := filepath.Join(t.TempDir(), "base-context")
			setContext := filepath.Join(t.TempDir(), "toolchain-context")
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
			for _, file := range []string{"Containerfile", "install-base.sh", "record-versions.sh"} {
				assertNoDotnetInstallation(t, filepath.Join(baseContext, file))
			}
			if selection == "none" {
				return
			}
			assertNoDotnetInstallation(t, filepath.Join(setContext, "Containerfile"))
			assertNoDotnetInstallation(t, filepath.Join(setContext, "record-versions.sh"))
			for _, name := range strings.Split(selection, ",") {
				assertNoDotnetInstallation(t, filepath.Join(setContext, name, "install.sh"))
			}
			if _, err := os.Stat(filepath.Join(setContext, "dotnet")); !os.IsNotExist(err) {
				t.Fatalf("context without dotnet contains its recipe: %v", err)
			}
		})
	}
}

func assertNoDotnetInstallation(t *testing.T, file string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"dotnet-sdk", "install-dotnet", "dotnet-install"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("recipe without dotnet %s contains %q", file, forbidden)
		}
	}
}
