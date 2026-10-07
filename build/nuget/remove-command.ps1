param([string]$InstallDirectory, [switch]$NoPathUpdate)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $InstallDirectory) {
    if (-not $env:LOCALAPPDATA) { throw 'LOCALAPPDATA is not set. Set it, or choose a directory with -InstallDirectory.' }
    $InstallDirectory = Join-Path $env:LOCALAPPDATA 'Programs/sandboxed-agents'
}
try {
    $InstallDirectory = [IO.Path]::GetFullPath($InstallDirectory)
} catch {
    throw ('The install directory ''{0}'' is not a valid path: {1}' -f $InstallDirectory, $_.Exception.Message)
}
if (-not $NoPathUpdate -and $InstallDirectory.Contains(';')) {
    throw ('The install directory ''{0}'' contains '';'', which separates PATH entries. Choose another directory, or run the script again with -NoPathUpdate.' -f $InstallDirectory)
}
if (-not $NoPathUpdate) {
    . (Join-Path $PSScriptRoot 'command-path.ps1')
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
