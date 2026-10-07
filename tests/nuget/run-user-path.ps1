param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [Parameter(Mandatory)][string]$UpgradeBinary,
    [Parameter(Mandatory)][ValidateSet('String', 'ExpandString', 'Absent')][string]$PathKind,
    [Parameter(Mandatory)][string]$PowerShell
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$testScript = Join-Path $PSScriptRoot 'user-path.ps1'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
try {
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    $elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
} finally {
    $identity.Dispose()
}
if (-not $elevated) {
    & $testScript -PackageDirectory $PackageDirectory -UpgradeBinary $UpgradeBinary -PathKind $PathKind -PowerShell $PowerShell
    exit 0
}

Add-Type -Path (Join-Path $PSScriptRoot 'restricted-process.cs')
$log = Join-Path $PackageDirectory 'restricted-test.log'
function Quote-Literal([string]$Value) { return "'" + $Value.Replace("'", "''") + "'" }
$invocation = '& ' + (Quote-Literal $testScript) + ' -PackageDirectory ' + (Quote-Literal $PackageDirectory) +
    ' -UpgradeBinary ' + (Quote-Literal $UpgradeBinary) + ' -PathKind ' + (Quote-Literal $PathKind) +
    ' -PowerShell ' + (Quote-Literal $PowerShell)
$code = "try { $invocation *> $(Quote-Literal $log); exit 0 } catch { " +
    '$_ | Out-String | Add-Content -LiteralPath ' + (Quote-Literal $log) + '; exit 1 }'
$encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment', $true)
$existed = @($key.GetValueNames()) -contains 'Path'
$original = $null
$kind = [Microsoft.Win32.RegistryValueKind]::String
if ($existed) {
    $original = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $kind = $key.GetValueKind('Path')
}
try {
    $exitCode = [NuGetRestrictedProcess]::Run($PowerShell, @('-NoLogo', '-NoProfile', '-NonInteractive', '-EncodedCommand', $encoded), $PackageDirectory)
    if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log | Write-Output }
    if ($exitCode -ne 0) { throw "Restricted installer process exited with $exitCode." }
} finally {
    if ($existed) { $key.SetValue('Path', $original, $kind) } else { $key.DeleteValue('Path', $false) }
    $key.Dispose()
}
