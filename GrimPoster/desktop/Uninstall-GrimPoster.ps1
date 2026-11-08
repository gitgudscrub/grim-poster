$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'GrimPoster.Common.ps1')

try {
    Assert-BotcClosed

    $manifestPath = Join-Path $PSScriptRoot 'patch\patch-manifest.json'
    $payloadPath = Join-Path $PSScriptRoot 'patch\grim-poster-main.br'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf) -or
        -not (Test-Path -LiteralPath $payloadPath -PathType Leaf)) {
        throw 'The patch files are missing. Extract the complete ZIP before running this uninstaller.'
    }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    Write-Host ("BOTC Grim Poster {0} - Windows uninstaller" -f $manifest.modVersion) -ForegroundColor Cyan
    $patchBytes = [IO.File]::ReadAllBytes($payloadPath)
    if ($patchBytes.Length -ne [int]$manifest.patchDataLength -or
        ($manifest.patchDataSha256 -and (Get-Sha256BytesLower -Bytes $patchBytes) -ne ([string]$manifest.patchDataSha256).ToLowerInvariant())) {
        throw 'The patch payload failed its length or checksum check.'
    }
    $appPath = Get-BotcExecutable
    $backupPath = "$appPath.grim-poster-original"
    $currentHash = Get-Sha256Lower -Path $appPath
    $originalHash = ([string]$manifest.expectedOriginalSha256).ToLowerInvariant()
    $patchedHash = ([string]$manifest.expectedPatchedSha256).ToLowerInvariant()

    if ($currentHash -eq $originalHash) {
        if (Test-Path -LiteralPath $backupPath) {
            if ((Get-Sha256Lower -Path $backupPath) -ne $originalHash) {
                throw 'The app is official, but the adjacent recovery copy is unrecognized. It was left untouched.'
            }
            Remove-Item -LiteralPath $backupPath -Force
        }
        Write-Host 'The official executable is already active. Grim Poster is not installed.' -ForegroundColor Green
        exit 0
    }
    if ($currentHash -ne $patchedHash -and -not (Test-InstalledPatch -Path $appPath -Manifest $manifest -Payload $patchBytes)) {
        throw "The current BOTC executable is not the official $($manifest.appVersion) file or this Grim Poster build. BOTC may have updated, or the file may be unrecognized. Nothing was changed."
    }
    if (-not (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
        throw "The recovery copy is missing: $backupPath"
    }
    if ((Get-Sha256Lower -Path $backupPath) -ne $originalHash) {
        throw 'The recovery copy failed verification. No changes were made.'
    }

    $preparedPath = "$appPath.grim-poster-restore"
    if (Test-Path -LiteralPath $preparedPath) {
        Remove-Item -LiteralPath $preparedPath -Force
    }
    Copy-Item -LiteralPath $backupPath -Destination $preparedPath
    if ((Get-Sha256Lower -Path $preparedPath) -ne $originalHash) {
        Remove-Item -LiteralPath $preparedPath -Force -ErrorAction SilentlyContinue
        throw 'The prepared official executable failed verification.'
    }

    Install-FileWithRollback -PreparedPath $preparedPath -DestinationPath $appPath -ExpectedHash $originalHash
    if ((Get-Sha256Lower -Path $appPath) -ne $originalHash) {
        throw 'The restored executable failed its final verification.'
    }
    Remove-Item -LiteralPath $backupPath -Force
    $versionStatePath = Join-Path $env:LOCALAPPDATA 'BOTC Grim Poster\installed-version.txt'
    Remove-Item -LiteralPath $versionStatePath -Force -ErrorAction SilentlyContinue

    Write-Host ''
    Write-Host 'Grim Poster was removed and the official executable was restored.' -ForegroundColor Green
} catch {
    Write-Host ''
    Write-Host "Uninstall stopped: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host 'No unrecognized executable was replaced.'
    exit 1
}
