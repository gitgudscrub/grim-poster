param(
    # Read-only mode used by the Windows GUI. Outputs JSON only after verifying
    # the manifest against the pinned release-signing public key.
    [switch]$StatusOnly,
    [ValidatePattern('^$|^\d+\.\d+\.\d+$')][string]$InstalledAppVersion = '',
    [ValidatePattern('^$|^[a-fA-F0-9]{64}$')][string]$InstalledExeSha256 = ''
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'GrimPoster.Common.ps1')

# The release signing public key is pinned in this bootstrap script. Replace
# this value once, before publishing the first update-enabled package.
$updatePublicKeyXml = '<RSAKeyValue><Modulus>rPVTQ9A1W3KLCmRa/SkWQ9V5sNS17sVd4yHDtVBQyAoUPaPVilZgVKuuT/ZHgL4QIbyO4PPKAMrBJ+JifFrOg8qm2MrdOZ1Vuq55iJuqDI1Mf2DRlc5N2NJEz1rAtTmMXF94VvvdckwDkjCZzkSA75P2L0RiABQ2fX6o4wg8YBaarIEXOiHZQq8XbmO3rerYHv55DEEttYWhXmYkHtqmvHDfTAyt53Qadyn4hp9ep1OyO743JbeE3TEA1HFKi89Yp9QM9H1un4lh59thx14xv7ckTMNiBSpRDb7MQHk5iaUCQb9D/5/HFV/lzbkwYBfUWBs/XCQYqS9wZPm1xBBqQM5I3gkzP+H119pN/C/5qbFm/ve4sNhtena2du7Lnb6Sys+HO/YL/UD08FfssToGCIm9HdGu+H37dkqNJ8MkbvcYfu7XwUrvdvfPWg8r/3RQNA9+sCg4wSJ5MpKDWPXxFPjT6KREE1yOkWHD31voXjwx8nAZjIOMVxqwtj+f95BB</Modulus><Exponent>AQAB</Exponent></RSAKeyValue>'
$updateBaseUri = 'https://updates.themavryan.com/'
$maximumPackageBytes = 100MB

function Get-UpdateBytes {
    param([Uri]$Uri, [long]$MaximumBytes)
    $request = [System.Net.HttpWebRequest]::Create($Uri)
    $request.Timeout = 5000
    $request.ReadWriteTimeout = 30000
    $response = $request.GetResponse()
    $memory = New-Object IO.MemoryStream
    try {
        if ($response.ContentLength -gt $MaximumBytes) { throw 'An update file exceeded its size limit.' }
        $stream = $response.GetResponseStream()
        try {
            $buffer = New-Object byte[] 65536
            while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
                if (($memory.Length + $read) -gt $MaximumBytes) { throw 'An update file exceeded its size limit.' }
                $memory.Write($buffer, 0, $read)
            }
            return ,$memory.ToArray()
        } finally { $stream.Dispose() }
    } finally {
        $memory.Dispose()
        $response.Dispose()
    }
}

function Show-UpdateMessage {
    param([string]$Message, [string]$Title, [object]$Icon)
    [void][System.Windows.Forms.MessageBox]::Show(
        $Message,
        $Title,
        [System.Windows.Forms.MessageBoxButtons]::OK,
        $Icon
    )
}

# A Grim Poster release targets ONE specific official BOTC executable. The
# signed server manifest is allowed to move forward to any BOTC version,
# but the installed application must match before an update is offered.
function Get-InstalledBotcAppVersion {
    $path = Get-BotcExecutable
    $value = [string](Get-Item -LiteralPath $path).VersionInfo.ProductVersion
    if ($value -notmatch '^(\d+\.\d+\.\d+)(?:\D.*)?$') {
        Write-Host "Could not determine BOTC version from '$path' (ProductVersion='$value'). Skipping Grim Poster update." -ForegroundColor Yellow
        return $null
    }
    return [Version]$Matches[1]
}

function Test-ManifestSignature {
    param([byte[]]$ManifestBytes, [byte[]]$SignatureBytes)

    if ([string]::IsNullOrWhiteSpace($updatePublicKeyXml) -or $updatePublicKeyXml -eq '__PUBLIC_KEY_XML__') {
        throw 'The update signing key has not been configured in this build.'
    }

    [xml]$keyXml = $updatePublicKeyXml
    if (-not $keyXml.RSAKeyValue.Modulus -or -not $keyXml.RSAKeyValue.Exponent) { throw 'The embedded update public key is invalid.' }
    $rsa = New-Object Security.Cryptography.RSACryptoServiceProvider
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $rsa.FromXmlString($updatePublicKeyXml)
        $hash = $sha.ComputeHash($ManifestBytes)
        $verifier = New-Object -TypeName Security.Cryptography.RSAPKCS1SignatureDeformatter -ArgumentList $rsa
        $verifier.SetHashAlgorithm('SHA256')
        return $verifier.VerifySignature($hash, $SignatureBytes)
    } finally {
        $sha.Dispose()
        $rsa.Dispose()
    }
}

try {
    Add-Type -AssemblyName System.Windows.Forms

    if (-not $StatusOnly -and (Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue)) {
        throw 'BOTC is already running. Close it before installing an update.'
    }

    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    try {
        $manifestBytes = Get-UpdateBytes -Uri ([Uri]::new([Uri]$updateBaseUri, 'manifest.json')) -MaximumBytes 65536
        if ($manifestBytes.Length -lt 2 -or $manifestBytes.Length -gt 65536) { throw 'The update manifest size is invalid.' }
        $signatureText = [Text.Encoding]::ASCII.GetString((Get-UpdateBytes -Uri ([Uri]::new([Uri]$updateBaseUri, 'manifest.sig')) -MaximumBytes 4096)).Trim()
    } catch { throw "The update server could not be reached: $($_.Exception.Message)" }

    try { $signatureBytes = [Convert]::FromBase64String($signatureText) }
    catch { throw 'The update signature file is not valid Base64.' }
    if (-not (Test-ManifestSignature -ManifestBytes $manifestBytes -SignatureBytes $signatureBytes)) {
        throw 'The update signature did not verify. No update was installed.'
    }

    $manifestText = [Text.Encoding]::UTF8.GetString($manifestBytes)
    $release = $manifestText | ConvertFrom-Json
    if ([int]$release.formatVersion -ne 1) {
        throw 'The update manifest format is unsupported.'
    }
    $targetAppVersion = [Version]'0.0'
    if (-not [Version]::TryParse([string]$release.appVersion, [ref]$targetAppVersion)) {
        throw 'The signed release manifest has an invalid BOTC app version.'
    }
    # Verify the release version and download metadata before exposing it to
    # the manager. The manager never trusts an unsigned manifest for updates.
    $releaseVersion = [Version]'0.0'
    if (-not [Version]::TryParse([string]$release.modVersion, [ref]$releaseVersion)) {
        throw 'The signed release has an invalid Grim Poster version.'
    }
    if ([string]$release.asset -notmatch '^releases/[A-Za-z0-9._-]+\.zip$' -or
        [string]$release.sha256 -notmatch '^[A-Fa-f0-9]{64}$' -or
        [long]$release.packageBytes -lt 1 -or [long]$release.packageBytes -gt $maximumPackageBytes) {
        throw 'The signed release contains invalid package details.'
    }
    $installedManifestPath = Join-Path $PSScriptRoot 'patch\patch-manifest.json'
    if (-not (Test-Path -LiteralPath $installedManifestPath -PathType Leaf)) {
        throw 'The local patch manifest is missing.'
    }
    $installedManifest = Get-Content -LiteralPath $installedManifestPath -Raw | ConvertFrom-Json
    # A new launcher may bundle a newer fixed patch than the server's current
    # signed release. The saved version record can be stale after an installer
    # failure, so it must NOT make us offer a downgrade to an older signed ZIP.
    $installedVersion = [Version]'0.0'
    if (-not [Version]::TryParse([string]$installedManifest.modVersion, [ref]$installedVersion)) {
        throw 'The bundled Grim Poster patch has an invalid version.'
    }
    $versionStatePath = Join-Path $env:LOCALAPPDATA 'BOTC Grim Poster\installed-version.txt'
    if (Test-Path -LiteralPath $versionStatePath -PathType Leaf) {
        $savedText = (Get-Content -LiteralPath $versionStatePath -Raw).Trim()
        $savedVersion = [Version]'0.0'
        if ($savedText -and [Version]::TryParse($savedText, [ref]$savedVersion) -and
            $savedVersion -gt $installedVersion) {
            $installedVersion = $savedVersion
        }
    }

    # Reading only the version record is insufficient: an official BOTC
    # reinstall can replace the patched executable with a clean one. Detect
    # that and offer to reinstall the newest signed release, even if its
    # version equals the saved mod version.
    $localBotcVersion = [string]$installedManifest.appVersion
    $localPatchedHash = [string]$installedManifest.expectedPatchedSha256
    if ($StatusOnly) {
        $localPatchPresent = ($InstalledAppVersion -ne '' -and
            $InstalledAppVersion -eq $localBotcVersion -and
            $InstalledExeSha256 -ne '' -and
            $InstalledExeSha256 -ieq $localPatchedHash)
    } else {
        $localPatchPresent = $false
    }

    # A read-only query MUST NOT open a file picker, mutate game files, or
    # display an update prompt. An empty version means BOTC is not installed.
    if ($StatusOnly) {
        $compatible = ($InstalledAppVersion -ne '' -and
            ([Version]$InstalledAppVersion -eq $targetAppVersion))
        [pscustomobject]@{
            modVersion = $releaseVersion.ToString(3)
            appVersion = $targetAppVersion.ToString(3)
            installedModVersion = $installedVersion.ToString(3)
            compatible = [bool]$compatible
            updateAvailable = [bool]($compatible -and ($releaseVersion -gt $installedVersion -or
                ($releaseVersion -eq $installedVersion -and -not $localPatchPresent)))
            patchInstalled = [bool]$localPatchPresent
            signatureVerified = $true
        } | ConvertTo-Json -Compress
        return
    }

    # Check the actual installed BOTC executable before changing anything.
    $installedAppVersion = Get-InstalledBotcAppVersion
    if ($null -eq $installedAppVersion) { return }
    if ($installedAppVersion -ne $targetAppVersion) {
        Write-Host ("Installed BOTC {0} does not match the latest signed Grim Poster patch target {1}; no update applied." -f $installedAppVersion, $targetAppVersion) -ForegroundColor Yellow
        return
    }
    # Skip downgrades. Allow reinstall of the same signed release if the
    # official game update or a repair has replaced the modified executable.
    if ($releaseVersion -lt $installedVersion) { return }
    if ($releaseVersion -eq $installedVersion) {
        $currentAppPath = Get-BotcExecutable
        $currentHash = Get-Sha256Lower -Path $currentAppPath
        $alreadyPatched = ([string]$installedManifest.appVersion -eq $installedAppVersion.ToString(3) -and
            $currentHash -ieq [string]$installedManifest.expectedPatchedSha256)
        if ($alreadyPatched) { return }
    }

    $confirm = [System.Windows.Forms.MessageBox]::Show(
        "Grim Poster $($release.modVersion) is available. Install it now? BOTC will remain closed while the update is checked and installed.",
        'BOTC Grim Poster update',
        [System.Windows.Forms.MessageBoxButtons]::YesNo,
        [System.Windows.Forms.MessageBoxIcon]::Information
    )
    if ($confirm -ne [System.Windows.Forms.DialogResult]::Yes) { return }

    $tempRoot = Join-Path $env:TEMP ('GrimPosterUpdate-' + [Guid]::NewGuid().ToString('N'))
    $zipPath = Join-Path $tempRoot 'release.zip'
    $extractPath = Join-Path $tempRoot 'package'
    [void](New-Item -ItemType Directory -Path $tempRoot -Force)
    try {
        $assetUri = [Uri]::new([Uri]$updateBaseUri, [string]$release.asset)
        if ($assetUri.Scheme -ne 'https' -or $assetUri.Host -ne 'updates.themavryan.com') {
            throw 'The update package URL is outside the configured update host.'
        }
        $packageBytes = Get-UpdateBytes -Uri $assetUri -MaximumBytes $maximumPackageBytes
        [IO.File]::WriteAllBytes($zipPath, $packageBytes)
        [Array]::Clear($packageBytes, 0, $packageBytes.Length)

        $file = Get-Item -LiteralPath $zipPath
        if ($file.Length -ne [long]$release.packageBytes -or
            (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash -ine [string]$release.sha256) {
            throw 'The downloaded update failed its signed length or SHA-256 check.'
        }

        Expand-Archive -LiteralPath $zipPath -DestinationPath $extractPath -Force
        $installer = Get-ChildItem -LiteralPath $extractPath -Filter 'Install-GrimPoster.ps1' -File -Recurse |
            Select-Object -First 1
        if (-not $installer) { throw 'The verified package does not contain its Windows installer.' }
        $packageManifestPath = Join-Path (Split-Path -Parent $installer.FullName) 'patch\patch-manifest.json'
        if (-not (Test-Path -LiteralPath $packageManifestPath -PathType Leaf)) {
            throw 'The verified package lacks its BOTC compatibility manifest.'
        }
        $packageManifest = Get-Content -LiteralPath $packageManifestPath -Raw | ConvertFrom-Json
        if ([string]$packageManifest.modVersion -ne [string]$release.modVersion -or
            [string]$packageManifest.appVersion -ne [string]$release.appVersion) {
            throw 'The downloaded package does not match its signed version or BOTC target.'
        }

        # Supply the official-executable hash from the installed (previous)
        # Grim Poster manifest. The new installer can archive that verified
        # recovery copy when an official BOTC upgrade leaves it behind.
        $priorOfficialHash = [string]$installedManifest.expectedOriginalSha256
        if ($priorOfficialHash -notmatch '^[0-9a-fA-F]{64}$') {
            throw 'The installed Grim Poster manifest has an invalid recovery hash.'
        }
        # BOTC normally installs into the current user's profile. Elevating
        # unconditionally can switch to a DIFFERENT Windows account and make
        # the installer inspect the wrong profile's copy of botc-app.exe.
        # Run in the current user's session first and capture the actual reason
        # when the strict backup/executable verification refuses an update.
        $logDirectory = Join-Path $env:LOCALAPPDATA 'Programs\Grim Poster\logs'
        [void](New-Item -ItemType Directory -Path $logDirectory -Force)
        $stamp = (Get-Date).ToString('yyyyMMdd-HHmmss')
        $logOutput = Join-Path $logDirectory ("install-$stamp-out.log")
        $logErrors = Join-Path $logDirectory ("install-$stamp-err.log")
        $arguments = '-NoLogo -NoProfile -File "{0}" -PreviousOriginalSha256 {1}' -f $installer.FullName, $priorOfficialHash
        $process = Start-Process -FilePath 'powershell.exe' -ArgumentList $arguments -Wait -PassThru `
            -RedirectStandardOutput $logOutput -RedirectStandardError $logErrors -WindowStyle Hidden
        if ($process.ExitCode -ne 0) {
            $details = @()
            if (Test-Path -LiteralPath $logOutput) {$details += @(Get-Content -LiteralPath $logOutput -Tail 12)}
            if (Test-Path -LiteralPath $logErrors) {$details += @(Get-Content -LiteralPath $logErrors -Tail 8)}
            $detailText = ($details | Out-String).Trim()
            if ($detailText.Length -gt 1000) {$detailText = $detailText.Substring($detailText.Length - 1000)}
            Show-UpdateMessage ("The Grim Poster installer exited with code $($process.ExitCode).`n`n$detailText`n`nFull logs: $logDirectory`n`nBOTC was not intentionally replaced if verification failed.") 'Update not completed' ([System.Windows.Forms.MessageBoxIcon]::Warning)
            exit 1
        } else {
            try {
                $versionDirectory = Join-Path $env:LOCALAPPDATA 'BOTC Grim Poster'
                [void](New-Item -ItemType Directory -Path $versionDirectory -Force)
                $utf8NoBom = New-Object -TypeName Text.UTF8Encoding -ArgumentList $false
                [IO.File]::WriteAllText((Join-Path $versionDirectory 'installed-version.txt'), [string]$release.modVersion, $utf8NoBom)
            } catch { Write-Host ("The update installed, but its version record could not be saved: {0}" -f $_.Exception.Message) -ForegroundColor Yellow }

            try {
                $sourceDesktop = Split-Path -Parent $installer.FullName
                $desktopFiles = @(
                    'Start-GrimPosterCompanion.ps1',
                    'Start-GrimPosterManager.ps1',
                    'Check-GrimPosterUpdate.ps1',
                    'GrimPoster.Common.ps1',
                    'Install-GrimPoster.ps1',
                    'Uninstall-GrimPoster.ps1',
                    'Start BOTC with Grim Poster.bat',
                    'Install Grim Poster.bat',
                    'Uninstall Grim Poster.bat'
                )
                foreach ($relativeFile in $desktopFiles) {
                    # The v0.1.48 patch predates the launcher's corrected
                    # updater, so don't replace the diagnostic-aware checker
                    # with that older copy after a successful installation.
                    if ($relativeFile -eq 'Check-GrimPosterUpdate.ps1' -and
                        $releaseVersion -le [Version]'0.1.48') { continue }
                    $sourceFile = Join-Path $sourceDesktop $relativeFile
                    if (Test-Path -LiteralPath $sourceFile -PathType Leaf) {
                        Copy-Item -LiteralPath $sourceFile -Destination (Join-Path $PSScriptRoot $relativeFile) -Force
                    }
                }
                $sourcePatchDirectory = Join-Path $sourceDesktop 'patch'
                $targetPatchDirectory = Join-Path $PSScriptRoot 'patch'
                [void](New-Item -ItemType Directory -Path $targetPatchDirectory -Force)
                # Update the native launcher if one is shipped in a newer signed release.
                $sourceLauncher = Join-Path (Split-Path -Parent $sourceDesktop) 'GrimPoster.exe'
                $targetLauncher = Join-Path (Split-Path -Parent $PSScriptRoot) 'GrimPoster.exe'
                if ((Test-Path -LiteralPath $sourceLauncher -PathType Leaf) -and
                    (Test-Path -LiteralPath $targetLauncher -PathType Leaf)) {
                    Copy-Item -LiteralPath $sourceLauncher -Destination $targetLauncher -Force
                }
                Copy-Item -Path (Join-Path $sourcePatchDirectory '*') -Destination $targetPatchDirectory -Recurse -Force
            } catch { Write-Host ("The BOTC patch installed, but the companion files could not all be refreshed: {0}" -f $_.Exception.Message) -ForegroundColor Yellow }

            Show-UpdateMessage "Grim Poster $($release.modVersion) was installed. BOTC will now start." 'Update installed' ([System.Windows.Forms.MessageBoxIcon]::Information)
        }
    } finally {
        if (Test-Path -LiteralPath $tempRoot) { Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue }
    }
} catch {
    if ($StatusOnly) { throw }
    $message = $_.Exception.Message
    if ($message -match 'signature|signed|SHA-256|outside the configured update host') {
        try {
            Add-Type -AssemblyName System.Windows.Forms -ErrorAction SilentlyContinue
            Show-UpdateMessage ("An update failed its security check. Nothing was installed; BOTC can still start.`n`n{0}" -f $message) 'Grim Poster update check' ([System.Windows.Forms.MessageBoxIcon]::Warning)
        } catch { }
    } elseif ($message -ne 'The update signing key has not been configured in this build.') {
        Write-Host ("Update check skipped; BOTC can still start. {0}" -f $message) -ForegroundColor Yellow
    }
}
