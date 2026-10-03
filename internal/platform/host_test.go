package platform_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/grauzone-dev/sandboxed-agents/internal/platform"
)

func TestCurrentHostIdentifiesUnixHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix host identity")
	}
	want := platform.Host{OS: runtime.GOOS, Architecture: runtime.GOARCH}
	if got := platform.CurrentHost(); got != want {
		t.Fatalf("host = %#v; want %#v", got, want)
	}
}

func TestCurrentHostIdentifiesNativeWindowsHost(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows host identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	powershell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	script := `$ErrorActionPreference = 'Stop'
$os = Get-CimInstance Win32_OperatingSystem
$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
$architecture = switch ($cpu.Architecture) { 0 { '386' } 5 { 'arm' } 9 { 'amd64' } 12 { 'arm64' } default { '' } }
@{ OS = 'windows'; Architecture = $architecture; WindowsMajor = ([version]$os.Version).Major; WindowsBuild = [uint32]$os.BuildNumber; WindowsWorkstation = ($os.ProductType -eq 1) } | ConvertTo-Json -Compress`
	output, err := exec.CommandContext(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("read independent Windows host identity: %v: %s", err, output)
	}
	var want platform.Host
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("decode Windows host identity: %v: %s", err, output)
	}
	if got := platform.CurrentHost(); got != want {
		t.Fatalf("host = %#v; want %#v", got, want)
	}
}
