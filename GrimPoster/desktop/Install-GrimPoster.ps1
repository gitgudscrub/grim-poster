param(
    [ValidatePattern('^$|^[a-fA-F0-9]{64}$')][string]$PreviousOriginalSha256 = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'GrimPoster.Common.ps1')

try {
    Assert-BotcClosed

    $manifestPath = Join-Path $PSScriptRoot 'patch\patch-manifest.json'
    $payloadPath = Join-Path $PSScriptRoot 'patch\grim-poster-main.br'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $payloadPath -PathType Leaf)) {
        throw 'The patch files are missing. Extract the complete ZIP before running this installer.'
    }

    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    Write-Host ("BOTC Grim Poster {0} - Windows installer" -f $manifest.modVersion) -ForegroundColor Cyan
    $patchBytes = [IO.File]::ReadAllBytes($payloadPath)
    if ($patchBytes.Length -ne [int]$manifest.patchDataLength -or
        ($manifest.patchDataSha256 -and (Get-Sha256BytesLower -Bytes $patchBytes) -ne ([string]$manifest.patchDataSha256).ToLowerInvariant())) {
        throw 'The patch payload failed its length or checksum check.'
    }
    $appPath = Get-BotcExecutable
    $currentHash = Get-Sha256Lower -Path $appPath
    $originalHash = ([string]$manifest.expectedOriginalSha256).ToLowerInvariant()
    $patchedHash = ([string]$manifest.expectedPatchedSha256).ToLowerInvariant()
    $upgradeHashes = @($manifest.supportedUpgradeSha256 | ForEach-Object {
        ([string]$_).ToLowerInvariant()
    })
    $backupPath = "$appPath.grim-poster-original"
    $verifiedRecoveryCopy = (Test-Path -LiteralPath $backupPath -PathType Leaf) -and
        ((Get-Sha256Lower -Path $backupPath) -eq $originalHash)
    $installedProductVersion = [string](Get-Item -LiteralPath $appPath).VersionInfo.ProductVersion
    $verifiedBackupUpgrade = ($currentHash -ne $originalHash) -and
        $verifiedRecoveryCopy -and
        ($installedProductVersion -eq [string]$manifest.appVersion)
    $previousPatchInstalled = $false
    foreach ($supportedPatch in @($manifest.supportedPatchPayloads)) {
        $supportedPath = Join-Path $PSScriptRoot ([string]$supportedPatch.file)
        if (-not (Test-Path -LiteralPath $supportedPath -PathType Leaf)) { continue }
        $supportedBytes = [IO.File]::ReadAllBytes($supportedPath)
        if ($supportedBytes.Length -ne [int]$supportedPatch.length -or
            (Get-Sha256BytesLower -Bytes $supportedBytes) -ne ([string]$supportedPatch.sha256).ToLowerInvariant()) { continue }
        $supportedManifest = [pscustomobject]@{
            assetDataOffset = $manifest.assetDataOffset
            assetCapacity = $manifest.assetCapacity
            patchDataLength = $supportedPatch.length
            dataLengthOffsets = $manifest.dataLengthOffsets
        }
        if (Test-InstalledPatch -Path $appPath -Manifest $supportedManifest -Payload $supportedBytes) {
            $previousPatchInstalled = $true
            break
        }
    }

    if ($currentHash -eq $patchedHash -or (Test-InstalledPatch -Path $appPath -Manifest $manifest -Payload $patchBytes)) {
        if (-not (Test-Path -LiteralPath $backupPath -PathType Leaf) -or
            (Get-Sha256Lower -Path $backupPath) -ne $originalHash) {
            throw 'Grim Poster is installed, but its verified recovery copy is missing. Reinstall the official app before making further changes.'
        }
        Write-Host "Grim Poster is already installed in:`n$appPath" -ForegroundColor Green
        exit 0
    }
    $isUpgrade = (($currentHash -ne $originalHash) -and ($upgradeHashes -contains $currentHash)) -or
        $previousPatchInstalled -or $verifiedBackupUpgrade
    if ($currentHash -ne $originalHash -and -not $isUpgrade) {
        throw "This BOTC installation is not supported by Grim Poster $($manifest.modVersion). It may be a newer app version or an unrecognized executable. BOTC was left untouched; use a Grim Poster build made for this BOTC version."
    }

    if ($isUpgrade -and
        (-not (Test-Path -LiteralPath $backupPath -PathType Leaf) -or
        (Get-Sha256Lower -Path $backupPath) -ne $originalHash)) {
        throw 'A recognized older Grim Poster build is installed, but its verified official recovery copy is missing.'
    }

    if ((Test-Path -LiteralPath $backupPath -PathType Leaf) -and $currentHash -eq $originalHash) {
        $existingRecoveryHash = Get-Sha256Lower -Path $backupPath
        if ($existingRecoveryHash -ne $originalHash) {
            # A BOTC update replaces botc-app.exe but can leave the prior
            # version's Grim Poster recovery copy beside it. Move ONLY a
            # known/verified prior official executable, never an unknown file.
            $verifiedPreviousHashes = @(
                'efc0abc595ac889d2cb33d11224313cfe83a97da4fc75b76cf2d25ac7c36e843' # official 3.57.3
            )
            if ($PreviousOriginalSha256) { $verifiedPreviousHashes += $PreviousOriginalSha256.ToLowerInvariant() }
            # This field did not exist in v0.1.48. Under StrictMode, reading
            # a missing JSON property throws instead of returning an empty list.
            # Only read it when explicitly present. Never trust an unknown
            # backup hash merely because the field is absent.
            $previousHashField = $manifest.PSObject.Properties['supportedPreviousOfficialSha256']
            if ($null -ne $previousHashField) {
                foreach ($hashValue in @($previousHashField.Value)) {
                    if ([string]$hashValue -match '^[A-Fa-f0-9]{64}$') {
                        $verifiedPreviousHashes += ([string]$hashValue).ToLowerInvariant()
                    }
                }
            }
            if ($verifiedPreviousHashes -notcontains $existingRecoveryHash) {
                throw 'The old recovery copy does not match a verified previous BOTC executable. BOTC was left untouched.'
            }
            $oldBackupPath = "$appPath.grim-poster-original-previous-$($existingRecoveryHash.Substring(0, 16))"
            if (Test-Path -LiteralPath $oldBackupPath) {
                throw "An archived recovery copy already exists: $oldBackupPath. No changes were made."
            }
            Move-Item -LiteralPath $backupPath -Destination $oldBackupPath
            Write-Host "Archived verified previous BOTC recovery copy: $oldBackupPath" -ForegroundColor Yellow
        }
    }
    if (Test-Path -LiteralPath $backupPath) {
        if ((Get-Sha256Lower -Path $backupPath) -ne $originalHash) {
            throw "The existing recovery copy is not the official $($manifest.appVersion) executable. No changes were made."
        }
    } else {
        Copy-Item -LiteralPath $appPath -Destination $backupPath
        if ((Get-Sha256Lower -Path $backupPath) -ne $originalHash) {
            Remove-Item -LiteralPath $backupPath -Force -ErrorAction SilentlyContinue
            throw 'The recovery copy failed verification. No changes were made.'
        }
    }

    $basePath = if ($isUpgrade) { $backupPath } else { $appPath }
    $appBytes = [IO.File]::ReadAllBytes($basePath)
    $dataOffset = [int]$manifest.assetDataOffset
    $capacity = [int]$manifest.assetCapacity
    $expectedLength = [int]$manifest.patchDataLength

    if ($patchBytes.Length -ne $expectedLength) {
        throw 'The patch payload failed its length check.'
    }
    if ($patchBytes.Length -gt $capacity -or $dataOffset -lt 0 -or
        ($dataOffset + $capacity) -gt $appBytes.Length) {
        throw 'The patch layout is invalid for this executable.'
    }

    [Array]::Clear($appBytes, $dataOffset, $capacity)
    [Array]::Copy($patchBytes, 0, $appBytes, $dataOffset, $patchBytes.Length)
    $lengthBytes = [BitConverter]::GetBytes([UInt64]$patchBytes.LongLength)
    if (-not [BitConverter]::IsLittleEndian) { [Array]::Reverse($lengthBytes) }
    foreach ($lengthOffsetValue in $manifest.dataLengthOffsets) {
        $lengthOffset = [int]$lengthOffsetValue
        if ($lengthOffset -lt 0 -or ($lengthOffset + 8) -gt $appBytes.Length) {
            throw 'A patch length-field location is invalid.'
        }
        [Array]::Copy($lengthBytes, 0, $appBytes, $lengthOffset, 8)
    }

    $preparedPath = "$appPath.grim-poster-new"
    if (Test-Path -LiteralPath $preparedPath) {
        Remove-Item -LiteralPath $preparedPath -Force
    }
    [IO.File]::WriteAllBytes($preparedPath, $appBytes)
    if (-not (Test-InstalledPatch -Path $preparedPath -Manifest $manifest -Payload $patchBytes)) {
        Remove-Item -LiteralPath $preparedPath -Force -ErrorAction SilentlyContinue
        throw 'The prepared executable failed embedded-asset verification. The official app was not replaced.'
    }
    $preparedHash = Get-Sha256Lower -Path $preparedPath
    if ($patchedHash -and $preparedHash -ne $patchedHash) {
        Remove-Item -LiteralPath $preparedPath -Force -ErrorAction SilentlyContinue
        throw 'The prepared executable failed its expected hash check. The official app was not replaced.'
    }

    Install-FileWithRollback -PreparedPath $preparedPath -DestinationPath $appPath -ExpectedHash $preparedHash

    try {
        $versionDirectory = Join-Path $env:LOCALAPPDATA 'BOTC Grim Poster'
        [void](New-Item -ItemType Directory -Path $versionDirectory -Force)
        $utf8NoBom = New-Object -TypeName Text.UTF8Encoding -ArgumentList $false
        [IO.File]::WriteAllText((Join-Path $versionDirectory 'installed-version.txt'), [string]$manifest.modVersion, $utf8NoBom)
    } catch {
        Write-Host "The mod installed, but its update-version record could not be saved: $($_.Exception.Message)" -ForegroundColor Yellow
    }

    Write-Host ''
    Write-Host 'Grim Poster was installed successfully.' -ForegroundColor Green
    Write-Host 'Open BOTC Online, place GP, then lock its position in settings.'
    Write-Host "Recovery copy: $backupPath"
} catch {
    Write-Host ''
    Write-Host "Installation stopped: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host 'If the app is installed under Program Files, right-click the BAT file and choose Run as administrator.'
    exit 1
}
