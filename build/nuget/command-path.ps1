function Resolve-CommandInstallDirectory([string]$InstallDirectory, [switch]$NoPathUpdate) {
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
    return $InstallDirectory
}

function Get-CommandUserPath {
    if (-not $IsWindows) { throw 'The user PATH can only be updated on Windows. Run the script again with -NoPathUpdate.' }
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $false)
    try {
        $exists = $null -ne $key -and @($key.GetValueNames()) -contains 'Path'
        if (-not $exists) {
            return @{ Value = ''; Kind = [Microsoft.Win32.RegistryValueKind]::String }
        }
        $kind = $key.GetValueKind('Path')
        if ($kind -notin @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) {
            throw ('The user PATH in HKEY_CURRENT_USER\Environment has the registry type {0}; only String and ExpandString are supported. Nothing was changed. Fix the value, or run the script again with -NoPathUpdate.' -f $kind)
        }
        return @{
            Value = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            Kind = $kind
        }
    } finally {
        if ($null -ne $key) { $key.Dispose() }
    }
}

function Test-CommandPathEntry([string]$Entry, [string]$Directory, [Microsoft.Win32.RegistryValueKind]$Kind) {
    if (-not $Entry) { return $false }
    try {
        $candidate = $Entry.Trim('"')
        if ($Kind -eq [Microsoft.Win32.RegistryValueKind]::ExpandString) {
            $candidate = [Environment]::ExpandEnvironmentVariables($candidate)
        }
        if (-not [IO.Path]::IsPathFullyQualified($candidate)) { return $false }
        $candidate = [IO.Path]::GetFullPath($candidate).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
        $target = $Directory.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
        return [string]::Equals($candidate, $target, [StringComparison]::OrdinalIgnoreCase)
    } catch {
        return $false
    }
}

function Set-CommandUserPath([hashtable]$Original, [string]$Value) {
    if ($Value -ceq $Original.Value) { return }
    Initialize-CommandEnvironmentNotification
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment', $true)
    try {
        $key.SetValue('Path', $Value, $Original.Kind)
    } finally {
        $key.Dispose()
    }
    [SandboxedAgentsEnvironmentNotification]::Broadcast()
}

function Initialize-CommandEnvironmentNotification {
    if (-not ('SandboxedAgentsEnvironmentNotification' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class SandboxedAgentsEnvironmentNotification
{
    private static readonly IntPtr HWND_BROADCAST = new IntPtr(0xffff);
    private const uint WM_SETTINGCHANGE = 0x001a;
    private const uint SMTO_ABORTIFHUNG = 0x0002;

    [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
    private static extern IntPtr SendMessageTimeoutW(IntPtr window, uint message, UIntPtr wParam,
        string lParam, uint flags, uint timeout, out UIntPtr result);

    public static void Broadcast()
    {
        UIntPtr result;
        SendMessageTimeoutW(HWND_BROADCAST, WM_SETTINGCHANGE, UIntPtr.Zero, "Environment", SMTO_ABORTIFHUNG, 1000, out result);
    }
}
'@
    }
}

function Get-CommandPathMarker([string]$Directory) {
    return Join-Path $Directory '.sandboxed-agents-path.json'
}

function Get-CommandPathOwnership([string]$Directory) {
    $marker = Get-CommandPathMarker $Directory
    if (-not (Test-Path -LiteralPath $marker)) { return $null }
    try {
        $ownership = [IO.File]::ReadAllText($marker) | ConvertFrom-Json -AsHashtable
        if ($ownership -isnot [Collections.IDictionary] -or $ownership.Count -ne 1 -or
            -not $ownership.Contains('entry') -or $ownership.entry -isnot [string] -or
            -not (Test-CommandPathEntry $ownership.entry $Directory ([Microsoft.Win32.RegistryValueKind]::String))) {
            throw 'invalid'
        }
        return $ownership
    } catch {
        throw ('The PATH ownership marker {0} is not valid. The command and the user PATH were not changed. The installer writes this file only when it adds the install directory to the user PATH, as a JSON object whose only field, "entry", holds that added entry: the full path of this directory. Restore the file from a backup, or delete it; the remover then leaves any entry for this directory on the user PATH for you to remove yourself.' -f $marker)
    }
}

function Add-CommandUserPath([hashtable]$Original, [string]$Directory) {
    $ownership = Get-CommandPathOwnership $Directory
    foreach ($entry in $Original.Value.Split(';')) {
        if (Test-CommandPathEntry $entry $Directory $Original.Kind) {
            Write-Output ('{0} is already on the user PATH.' -f $Directory)
            return
        }
    }
    $value = $Directory
    if ($Original.Value.Length -gt 0) { $value = $Original.Value + ';' + $Directory }
    $marker = Get-CommandPathMarker $Directory
    $previousMarker = $null
    if ($null -ne $ownership) { $previousMarker = [IO.File]::ReadAllText($marker) }
    [IO.File]::WriteAllText($marker, (@{ entry = $Directory } | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
    try {
        Set-CommandUserPath $Original $value
    } catch {
        if ($null -ne $previousMarker) { [IO.File]::WriteAllText($marker, $previousMarker, [Text.UTF8Encoding]::new($false)) }
        else { Remove-Item -LiteralPath $marker -Force }
        throw
    }
    Write-Output ('Added {0} to the user PATH. Terminals that are already running keep their old PATH; start a new terminal from the Start menu, or restart your terminal application, to use sandboxed-agents.' -f $Directory)
}

function Remove-CommandUserPath([hashtable]$Original, [string]$Directory) {
    $ownership = Get-CommandPathOwnership $Directory
    if ($null -eq $ownership) { return }
    $entries = [Collections.Generic.List[string]]::new()
    $entries.AddRange([string[]]$Original.Value.Split(';'))
    for ($index = $entries.Count - 1; $index -ge 0; $index--) {
        if ($entries[$index] -ceq $ownership.entry) {
            $entries.RemoveAt($index)
            Set-CommandUserPath $Original ($entries -join ';')
            Write-Output ('Removed {0} from the user PATH. Terminals that are already running keep their old PATH until you restart them.' -f $ownership.entry)
            break
        }
    }
    Remove-Item -LiteralPath (Get-CommandPathMarker $Directory) -Force
}
