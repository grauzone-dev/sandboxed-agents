param(
    [Parameter(Mandatory)][string]$PackageDirectory,
    [Parameter(Mandatory)][string]$UpgradeBinary,
    [Parameter(Mandatory)][ValidateSet('String', 'ExpandString', 'Absent')][string]$PathKind,
    [Parameter(Mandatory)][string]$PowerShell
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if (-not $IsWindows) { throw 'Native Windows registry tests require Windows.' }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
try {
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Installer tests must run without administrator rights.'
    }
} finally {
    $identity.Dispose()
}

function Invoke-TestProcess([string]$Executable, [string[]]$Arguments, [string]$WorkingDirectory = '') {
    $info = [Diagnostics.ProcessStartInfo]::new($Executable)
    $info.UseShellExecute = $false
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    if ($WorkingDirectory) { $info.WorkingDirectory = $WorkingDirectory }
    foreach ($argument in $Arguments) { $info.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    try {
        $process.Start() | Out-Null
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        return @{ ExitCode = $process.ExitCode; Output = $stdout.GetAwaiter().GetResult() + $stderr.GetAwaiter().GetResult() }
    } finally {
        $process.Dispose()
    }
}

function Invoke-Installer([string]$Script, [string[]]$Arguments = @(), [switch]$Fails, [string]$WorkingDirectory = '') {
    $result = Invoke-TestProcess $PowerShell (@('-NoLogo', '-NoProfile', '-NonInteractive', '-File', (Join-Path $PackageDirectory $Script)) + $Arguments) $WorkingDirectory
    if (($result.ExitCode -eq 0) -eq [bool]$Fails) { throw "$Script returned $($result.ExitCode): $($result.Output)" }
}

function Assert-Path([string]$Expected, [Microsoft.Win32.RegistryValueKind]$Kind) {
    $actual = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($actual -cne $Expected) { throw "Raw user PATH differs: expected <$Expected>; actual <$actual>." }
    if ($key.GetValueKind('Path') -ne $Kind) { throw 'The user PATH registry type changed.' }
}

function Assert-Version([string]$Directory, [string]$Expected) {
    $result = Invoke-TestProcess (Join-Path $Directory 'sandboxed-agents.exe') @('version')
    $lines = $result.Output.TrimEnd().Split("`n")
    if ($result.ExitCode -ne 0 -or $lines[0].TrimEnd("`r") -cne $Expected -or $lines.Count -ne 2 -or $lines[1].TrimEnd("`r") -cnotmatch '^assets [a-f0-9]{64}$') {
        throw "Installed version failed: $($result.Output)"
    }
}

$key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment', $true)
$existed = @($key.GetValueNames()) -contains 'Path'
$original = $null
$originalKind = [Microsoft.Win32.RegistryValueKind]::String
if ($existed) {
    $original = $key.GetValue('Path', $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $originalKind = $key.GetValueKind('Path')
}
try {
    $raw = ' C:\Keep Mixed CASE ;%USERPROFILE%\bin;;C:\Other\;.;bin;C:bin;'
    $kind = [Microsoft.Win32.RegistryValueKind]::String
    if ($PathKind -eq 'ExpandString') { $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString }
    if ($PathKind -eq 'Absent') {
        $key.DeleteValue('Path', $false)
        $raw = ''
    } else {
        $key.SetValue('Path', $raw, $kind)
    }
    $defaultDirectory = Join-Path $env:LOCALAPPDATA 'Programs/sandboxed-agents'
    $expected = $defaultDirectory
    if ($raw) { $expected = $raw + ';' + $defaultDirectory }
    [IO.Directory]::CreateDirectory($defaultDirectory) | Out-Null
    $outside = Join-Path $env:LOCALAPPDATA 'untouched.txt'
    [IO.File]::WriteAllText($outside, 'outside installation')
    Invoke-Installer 'install-command.ps1' -WorkingDirectory $defaultDirectory
    Assert-Path $expected $kind
    Assert-Version $defaultDirectory 'sandboxed-agents v1.0.0-preview.20261007.1'
    Invoke-Installer 'install-command.ps1' -WorkingDirectory $defaultDirectory
    Assert-Path $expected $kind

    $source = Join-Path $PackageDirectory 'sandboxed-agents-windows-amd64.exe'
    Copy-Item -LiteralPath $UpgradeBinary -Destination $source -Force
    $hash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $PackageDirectory 'SHA256SUMS'), "$hash  sandboxed-agents-windows-amd64.exe`n")
    Invoke-Installer 'install-command.ps1' -WorkingDirectory $defaultDirectory
    Assert-Path $expected $kind
    Assert-Version $defaultDirectory 'sandboxed-agents v1.0.0-preview.20261007.2'

    [IO.File]::WriteAllText($source, 'tampered')
    Invoke-Installer 'install-command.ps1' -Fails
    Assert-Path $expected $kind
    Assert-Version $defaultDirectory 'sandboxed-agents v1.0.0-preview.20261007.2'
    $rejectedDirectory = Join-Path $env:LOCALAPPDATA 'must-not-exist'
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $rejectedDirectory) -Fails
    if (Test-Path -LiteralPath $rejectedDirectory) { throw 'Checksum failure created an installation.' }
    Assert-Path $expected $kind

    $unrelated = Join-Path $defaultDirectory 'keep.txt'
    [IO.File]::WriteAllText($unrelated, 'unrelated file')
    Invoke-Installer 'remove-command.ps1' -WorkingDirectory $defaultDirectory
    Assert-Path $raw $kind
    if (Test-Path -LiteralPath (Join-Path $defaultDirectory 'sandboxed-agents.exe')) { throw 'Removal left the command installed.' }
    if ([IO.File]::ReadAllText($unrelated) -cne 'unrelated file') { throw 'Removal changed an unrelated file.' }
    Invoke-Installer 'remove-command.ps1' -WorkingDirectory $defaultDirectory
    Assert-Path $raw $kind

    Copy-Item -LiteralPath $UpgradeBinary -Destination $source -Force
    $custom = Join-Path $env:LOCALAPPDATA 'custom directory'
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $custom)
    $customExpected = $custom
    if ($raw) { $customExpected = $raw + ';' + $custom }
    Assert-Path $customExpected $kind
    Assert-Version $custom 'sandboxed-agents v1.0.0-preview.20261007.2'
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom)
    Assert-Path $raw $kind

    $expandableEntry = '%LOCALAPPDATA%\custom directory'
    $expandableRaw = $expandableEntry
    if ($raw) { $expandableRaw = $raw + ';' + $expandableEntry }
    $key.SetValue('Path', $expandableRaw, $kind)
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $custom)
    $expandableInstalled = $expandableRaw
    $expandableRemoved = $raw
    if ($kind -eq [Microsoft.Win32.RegistryValueKind]::String) {
        $expandableInstalled += ';' + $custom
        $expandableRemoved = $expandableRaw
    }
    Assert-Path $expandableInstalled $kind
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom)
    Assert-Path $expandableRemoved $kind
    $key.SetValue('Path', $raw, $kind)

    $existingEntry = $custom.ToUpperInvariant() + '\'
    $existingRaw = $existingEntry
    if ($raw) { $existingRaw = $raw + ';' + $existingEntry }
    $key.SetValue('Path', $existingRaw, $kind)
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $custom)
    Assert-Path $existingRaw $kind
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom)
    Assert-Path $raw $kind

    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $custom, '-NoPathUpdate')
    Assert-Path $raw $kind
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom, '-NoPathUpdate')
    Assert-Path $raw $kind

    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $custom, '-NoPathUpdate')
    $key.SetValue('Path', 7, [Microsoft.Win32.RegistryValueKind]::DWord)
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $rejectedDirectory) -Fails
    if (Test-Path -LiteralPath $rejectedDirectory) { throw 'Unsupported PATH registry type created an installation.' }
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom) -Fails
    Assert-Version $custom 'sandboxed-agents v1.0.0-preview.20261007.2'
    if ($key.GetValueKind('Path') -ne [Microsoft.Win32.RegistryValueKind]::DWord -or $key.GetValue('Path') -ne 7) {
        throw 'Unsupported PATH registry type was modified.'
    }
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $custom, '-NoPathUpdate')
    $key.SetValue('Path', $raw, $kind)

    $separatorDirectory = Join-Path $env:LOCALAPPDATA 'directory;with separator'
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $separatorDirectory) -Fails
    if (Test-Path -LiteralPath $separatorDirectory) { throw 'PATH separator directory was installed.' }
    Assert-Path $raw $kind
    Invoke-Installer 'install-command.ps1' @('-InstallDirectory', $separatorDirectory, '-NoPathUpdate')
    Assert-Version $separatorDirectory 'sandboxed-agents v1.0.0-preview.20261007.2'
    Invoke-Installer 'remove-command.ps1' @('-InstallDirectory', $separatorDirectory, '-NoPathUpdate')
    Assert-Path $raw $kind

    if ([IO.File]::ReadAllText($outside) -cne 'outside installation') { throw 'Installation changed an outside file.' }
    $expectedFiles = @('Programs\sandboxed-agents\keep.txt', 'untouched.txt')
    $actualFiles = @(Get-ChildItem -LiteralPath $env:LOCALAPPDATA -File -Recurse | ForEach-Object { [IO.Path]::GetRelativePath($env:LOCALAPPDATA, $_.FullName) } | Sort-Object)
    if (($actualFiles -join '|') -cne ($expectedFiles -join '|')) { throw "Installation changed files outside its directory: $($actualFiles -join ', ')." }
} finally {
    if ($existed) { $key.SetValue('Path', $original, $originalKind) } else { $key.DeleteValue('Path', $false) }
    $key.Dispose()
}
