param([string]$InstallDirectory, [switch]$NoPathUpdate)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

. (Join-Path $PSScriptRoot 'command-path.ps1')
$InstallDirectory = Resolve-CommandInstallDirectory $InstallDirectory -NoPathUpdate:$NoPathUpdate
$source = Join-Path $PSScriptRoot 'sandboxed-agents-windows-amd64.exe'
$checksumLines = @(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'SHA256SUMS') | Where-Object {
    $_ -cmatch '^[a-fA-F0-9]{64} [ *]sandboxed-agents-windows-amd64\.exe$'
})
if ($checksumLines.Count -eq 0) { throw ('SHA256SUMS has no entry for {0}. Nothing was changed.' -f 'sandboxed-agents-windows-amd64.exe') }
if ($checksumLines.Count -gt 1) { throw ('SHA256SUMS has more than one entry for {0}. Nothing was changed.' -f 'sandboxed-agents-windows-amd64.exe') }
$expected = $checksumLines[0].Substring(0, 64)
if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $expected) {
    throw ('{0} does not match its checksum in SHA256SUMS. Nothing was changed. Download and extract the package again.' -f $source)
}
if (-not $NoPathUpdate) {
    $originalPath = Get-CommandUserPath
}
[IO.Directory]::CreateDirectory($InstallDirectory) | Out-Null
$destination = Join-Path $InstallDirectory 'sandboxed-agents.exe'
$staged = Join-Path $InstallDirectory ([IO.Path]::GetRandomFileName())
try {
    Copy-Item -LiteralPath $source -Destination $staged
    if ((Get-FileHash -LiteralPath $staged -Algorithm SHA256).Hash -ne $expected) {
        throw ('The installed copy {0} does not match SHA256SUMS. The user PATH was not changed. Run the installer again.' -f $staged)
    }
    [IO.File]::Move($staged, $destination, $true)
} finally {
    if (Test-Path -LiteralPath $staged) { Remove-Item -LiteralPath $staged -Force }
}
Write-Output ('Installed sandboxed-agents to {0}.' -f $destination)
if ($NoPathUpdate) { Write-Output ('The user PATH was not changed. Run {0} by its full path, or add its directory to PATH yourself.' -f $destination) }
else { Add-CommandUserPath $originalPath $InstallDirectory }
