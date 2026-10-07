param([string]$InstallDirectory, [switch]$NoPathUpdate)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'command-path.ps1')
$InstallDirectory = Resolve-CommandInstallDirectory $InstallDirectory -NoPathUpdate:$NoPathUpdate
if (-not $NoPathUpdate) {
    $originalPath = Get-CommandUserPath
}
$destination = Join-Path $InstallDirectory 'sandboxed-agents.exe'
if (Test-Path -LiteralPath $destination) {
    Remove-Item -LiteralPath $destination -Force
    Write-Output ('Removed {0}.' -f $destination)
} else {
    Write-Output ('{0} does not exist; there is no command to remove.' -f $destination)
}
if (-not $NoPathUpdate) { Remove-CommandUserPath $originalPath $InstallDirectory }
