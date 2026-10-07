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

function Test-CommandPathEntry([string]$Entry, [string]$Directory) {
    if (-not $Entry) { return $false }
    try {
        $candidate = [IO.Path]::GetFullPath([Environment]::ExpandEnvironmentVariables($Entry.Trim('"'))).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
        $target = $Directory.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
        return [string]::Equals($candidate, $target, [StringComparison]::OrdinalIgnoreCase)
    } catch {
        return $false
    }
}

function Set-CommandUserPath([hashtable]$Original, [string]$Value) {
    if ($Value -ceq $Original.Value) { return }
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment', $true)
    try {
        $key.SetValue('Path', $Value, $Original.Kind)
    } finally {
        $key.Dispose()
    }
}

function Add-CommandUserPath([hashtable]$Original, [string]$Directory) {
    foreach ($entry in $Original.Value.Split(';')) {
        if (Test-CommandPathEntry $entry $Directory) {
            Write-Output ('{0} is already on the user PATH.' -f $Directory)
            return
        }
    }
    $value = $Directory
    if ($Original.Value.Length -gt 0) { $value = $Original.Value + ';' + $Directory }
    Set-CommandUserPath $Original $value
    Write-Output ('Added {0} to the user PATH. Open a new terminal to use sandboxed-agents.' -f $Directory)
}

function Remove-CommandUserPath([hashtable]$Original, [string]$Directory) {
    $remaining = @($Original.Value.Split(';') | Where-Object { -not (Test-CommandPathEntry $_ $Directory) })
    $value = $remaining -join ';'
    Set-CommandUserPath $Original $value
    if ($value -cne $Original.Value) { Write-Output ('Removed {0} from the user PATH. Open a new terminal for the change to take effect.' -f $Directory) }
}
