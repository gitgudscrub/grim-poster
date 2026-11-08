Set-StrictMode -Version 2.0

function Get-Sha256Lower {
    param([Parameter(Mandatory = $true)][string]$Path)
    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Get-Sha256BytesLower {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return [BitConverter]::ToString($sha.ComputeHash($Bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Test-InstalledPatch {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object]$Manifest,
        [Parameter(Mandatory = $true)][byte[]]$Payload
    )
    try {
        $dataOffset = [int]$Manifest.assetDataOffset
        $capacity = [int]$Manifest.assetCapacity
        if ($Payload.Length -ne [int]$Manifest.patchDataLength -or
            $Payload.Length -gt $capacity -or $dataOffset -lt 0) { return $false }
        $bytes = [IO.File]::ReadAllBytes($Path)
        if (($dataOffset + $capacity) -gt $bytes.Length) { return $false }
        for ($index = 0; $index -lt $Payload.Length; $index++) {
            if ($bytes[$dataOffset + $index] -ne $Payload[$index]) { return $false }
        }
        for ($index = $Payload.Length; $index -lt $capacity; $index++) {
            if ($bytes[$dataOffset + $index] -ne 0) { return $false }
        }
        foreach ($offsetValue in $Manifest.dataLengthOffsets) {
            $offset = [int]$offsetValue
            if ($offset -lt 0 -or ($offset + 8) -gt $bytes.Length -or
                [BitConverter]::ToUInt64($bytes, $offset) -ne [UInt64]$Payload.LongLength) { return $false }
        }
        return $true
    } catch {
        return $false
    }
}

# Unsigned local beta patches derive their full executable SHA-256 from a
# verified, untouched official BOTC recovery copy. Check the entire result,
# not merely the injected asset region, before the manager permits launch.
function Test-InstalledDerivedPatch {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][object]$Manifest,
        [Parameter(Mandatory = $true)][string]$PayloadPath
    )
    try {
        if ([string]$Manifest.patchVerification -ne 'derive-from-verified-original' -or
            -not [string]::IsNullOrEmpty([string]$Manifest.expectedPatchedSha256)) { return $false }
        $backupPath = "$Path.grim-poster-original"
        if (-not (Test-Path -LiteralPath $backupPath -PathType Leaf) -or
            (Get-Sha256Lower -Path $backupPath) -ne ([string]$Manifest.expectedOriginalSha256).ToLowerInvariant()) {
            return $false
        }
        $payload = [IO.File]::ReadAllBytes($PayloadPath)
        if ($payload.Length -ne [int]$Manifest.patchDataLength -or
            (Get-Sha256BytesLower -Bytes $payload) -ne ([string]$Manifest.patchDataSha256).ToLowerInvariant()) {
            return $false
        }
        $bytes = [IO.File]::ReadAllBytes($backupPath)
        $offset = [int]$Manifest.assetDataOffset
        $capacity = [int]$Manifest.assetCapacity
        if ($offset -lt 0 -or $payload.Length -gt $capacity -or
            ($offset + $capacity) -gt $bytes.Length) { return $false }
        [Array]::Clear($bytes, $offset, $capacity)
        [Array]::Copy($payload, 0, $bytes, $offset, $payload.Length)
        $lengthBytes = [BitConverter]::GetBytes([UInt64]$payload.LongLength)
        if (-not [BitConverter]::IsLittleEndian) { [Array]::Reverse($lengthBytes) }
        foreach ($offsetValue in $Manifest.dataLengthOffsets) {
            $lengthOffset = [int]$offsetValue
            if ($lengthOffset -lt 0 -or ($lengthOffset + 8) -gt $bytes.Length) { return $false }
            [Array]::Copy($lengthBytes, 0, $bytes, $lengthOffset, 8)
        }
        return (Get-Sha256BytesLower -Bytes $bytes) -eq (Get-Sha256Lower -Path $Path)
    } catch {
        return $false
    }
}

function Add-CandidatePath {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][System.Collections.Generic.List[string]]$List,
        [AllowNull()][AllowEmptyString()][string]$Path
    )
    if ([string]::IsNullOrWhiteSpace($Path)) { return }
    $expanded = [Environment]::ExpandEnvironmentVariables($Path.Trim().Trim('"'))
    if ([IO.Path]::GetFileName($expanded) -ine 'botc-app.exe') {
        $expanded = Join-Path $expanded 'botc-app.exe'
    }
    if (-not $List.Contains($expanded)) { [void]$List.Add($expanded) }
}

function Get-OptionalProperty {
    param(
        [Parameter(Mandatory = $true)][object]$InputObject,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $property = $InputObject.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) { return '' }
    return [string]$property.Value
}

function Get-BotcExecutable {
    $candidates = New-Object 'System.Collections.Generic.List[string]'
    $registryRoots = @(
        'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
        'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
        'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
    )

    foreach ($root in $registryRoots) {
        $entries = Get-ItemProperty -Path $root -ErrorAction SilentlyContinue |
            Where-Object { (Get-OptionalProperty -InputObject $_ -Name 'DisplayName') -like 'Blood on the Clocktower Online*' }
        foreach ($entry in $entries) {
            Add-CandidatePath -List $candidates -Path (Get-OptionalProperty -InputObject $entry -Name 'InstallLocation')

            $displayIcon = Get-OptionalProperty -InputObject $entry -Name 'DisplayIcon'
            if (-not [string]::IsNullOrWhiteSpace($displayIcon)) {
                if ($displayIcon -match '^"([^"]+)"') {
                    Add-CandidatePath -List $candidates -Path $Matches[1]
                } else {
                    Add-CandidatePath -List $candidates -Path ($displayIcon.Split(',')[0])
                }
            }

            $uninstall = Get-OptionalProperty -InputObject $entry -Name 'UninstallString'
            if (-not [string]::IsNullOrWhiteSpace($uninstall)) {
                if ($uninstall -match '^"([^"]+)"') {
                    Add-CandidatePath -List $candidates -Path (Split-Path $Matches[1] -Parent)
                } elseif ($uninstall -match '^(\S+\.exe)') {
                    Add-CandidatePath -List $candidates -Path (Split-Path $Matches[1] -Parent)
                }
            }
        }
    }

    $commonDirectories = @(
        (Join-Path $env:LOCALAPPDATA 'Blood on the Clocktower Online'),
        (Join-Path $env:LOCALAPPDATA 'Programs\Blood on the Clocktower Online'),
        (Join-Path $env:LOCALAPPDATA 'botc-app'),
        (Join-Path $env:ProgramFiles 'Blood on the Clocktower Online')
    )
    $programFilesX86 = [Environment]::GetEnvironmentVariable('ProgramFiles(x86)')
    if ($programFilesX86) {
        $commonDirectories += Join-Path $programFilesX86 'Blood on the Clocktower Online'
    }
    foreach ($directory in $commonDirectories) {
        Add-CandidatePath -List $candidates -Path $directory
    }

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }

    Add-Type -AssemblyName System.Windows.Forms
    $dialog = New-Object System.Windows.Forms.OpenFileDialog
    $dialog.Title = 'Find the Blood on the Clocktower Online application'
    $dialog.Filter = 'BOTC application (botc-app.exe)|botc-app.exe'
    $dialog.FileName = 'botc-app.exe'
    if ($dialog.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) {
        throw 'Installation cancelled: botc-app.exe was not selected.'
    }
    if ([IO.Path]::GetFileName($dialog.FileName) -ine 'botc-app.exe') {
        throw 'The selected file is not botc-app.exe.'
    }
    return (Resolve-Path -LiteralPath $dialog.FileName).Path
}

function Assert-BotcClosed {
    if (Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {
        throw 'Blood on the Clocktower Online is running. Close it completely, then try again.'
    }
}

function Install-FileWithRollback {
    param(
        [Parameter(Mandatory = $true)][string]$PreparedPath,
        [Parameter(Mandatory = $true)][string]$DestinationPath,
        [Parameter(Mandatory = $true)][string]$ExpectedHash
    )

    $switchPath = "$DestinationPath.grim-poster-switch"
    if (Test-Path -LiteralPath $switchPath) {
        throw "A previous switch file exists at '$switchPath'. Remove it only after confirming the official app is recoverable."
    }

    Move-Item -LiteralPath $DestinationPath -Destination $switchPath
    try {
        Move-Item -LiteralPath $PreparedPath -Destination $DestinationPath
        $actualHash = Get-Sha256Lower -Path $DestinationPath
        if ($actualHash -ne $ExpectedHash) {
            throw 'The installed executable failed its final hash check.'
        }
        Remove-Item -LiteralPath $switchPath -Force
    } catch {
        if (Test-Path -LiteralPath $DestinationPath) {
            Remove-Item -LiteralPath $DestinationPath -Force -ErrorAction SilentlyContinue
        }
        if (Test-Path -LiteralPath $switchPath) {
            Move-Item -LiteralPath $switchPath -Destination $DestinationPath -Force
        }
        throw
    }
}
