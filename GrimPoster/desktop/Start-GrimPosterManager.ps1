param([switch]$FirstLaunch)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()

$script:desktopDir = $PSScriptRoot
$script:rootDir = Split-Path -Parent $PSScriptRoot
$script:launcher = Join-Path $script:rootDir 'GrimPoster.exe'
$script:release = $null
$script:botc = $null
$script:compatiblePatchVersion = [Version]'3.58.0'
$script:gpVersion = '0.1.49' # Initial bundled patch; updated from the on-disk manifest after signed upgrades
$script:remoteGrim = $null
$script:officialReleasePage = 'https://github.com/ThePandemoniumInstitute/botc-release/releases/latest'

function Show-Notice([string]$Message, [string]$Title='Grim Poster') {
    [void][System.Windows.Forms.MessageBox]::Show($Message,$Title,[System.Windows.Forms.MessageBoxButtons]::OK,[System.Windows.Forms.MessageBoxIcon]::Information)
}
function Show-Failure([string]$Message) {
    [void][System.Windows.Forms.MessageBox]::Show($Message,'Grim Poster',[System.Windows.Forms.MessageBoxButtons]::OK,[System.Windows.Forms.MessageBoxIcon]::Error)
}
function Ask-YesNo([string]$Message, [string]$Title='Grim Poster') {
    return [System.Windows.Forms.MessageBox]::Show($Message,$Title,[System.Windows.Forms.MessageBoxButtons]::YesNo,[System.Windows.Forms.MessageBoxIcon]::Question) -eq [System.Windows.Forms.DialogResult]::Yes
}
function Get-InstalledBOTC {
    $paths = New-Object 'System.Collections.Generic.List[string]'
    $uninstallRoots = @('HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
      'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*',
      'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*')
    foreach($root in $uninstallRoots) {
        foreach($entry in @(Get-ItemProperty -Path $root -ErrorAction SilentlyContinue)) {
            if ([string]$entry.DisplayName -notlike 'Blood on the Clocktower Online*') {continue}
            if ($entry.InstallLocation) {
                $loc=([string]$entry.InstallLocation).Trim('"')
                [void]$paths.Add((Join-Path -Path $loc -ChildPath 'botc-app.exe'))
            }
            if ($entry.DisplayIcon) {
                $iconPath = [string]$entry.DisplayIcon
                if ($iconPath -match '^"([^"]+)"') { $iconPath=$Matches[1] }
                else { $iconPath=$iconPath.Split(',')[0] }
                if ([IO.Path]::GetFileName($iconPath) -ieq 'botc-app.exe') {[void]$paths.Add($iconPath)}
            }
        }
    }
    $appData = [Environment]::GetFolderPath('LocalApplicationData')
    foreach($p in @(
        (Join-Path $appData 'Blood on the Clocktower Online\botc-app.exe'),
        (Join-Path $appData 'Programs\Blood on the Clocktower Online\botc-app.exe'),
        (Join-Path $appData 'botc-app\botc-app.exe'),
        (Join-Path $env:ProgramFiles 'Blood on the Clocktower Online\botc-app.exe')
    )) { [void]$paths.Add($p) }
    if (${env:ProgramFiles(x86)}) {
        [void]$paths.Add((Join-Path ${env:ProgramFiles(x86)} 'Blood on the Clocktower Online\botc-app.exe'))
    }
    $foundInstalls = @()
    foreach($p in @($paths | Select-Object -Unique)) {
        if(-not (Test-Path -LiteralPath $p -PathType Leaf)) {continue}
        try {
            $file = Get-Item -LiteralPath $p
            $raw = [string]$file.VersionInfo.ProductVersion
            if($raw -match '(\d+\.\d+\.\d+)') {
                $foundInstalls += [pscustomobject]@{ Path=$file.FullName; Version=([Version]$Matches[1]) }
            }
        } catch { }
    }
    if($foundInstalls.Count -eq 0) {return $null}
    return ($foundInstalls | Sort-Object Version -Descending | Select-Object -First 1)
}
function Get-OfficialLatest {
    # GitHub's latest *stable* release. We never accept a URL supplied by a third party.
    $api = 'https://api.github.com/repos/ThePandemoniumInstitute/botc-release/releases/latest'
    $response = Invoke-RestMethod -Method Get -Uri $api -Headers @{
        'User-Agent'='GrimPoster-Windows-Launcher/0.1.52'
        'Accept'='application/vnd.github+json'
    } -TimeoutSec 15
    $tag=[string]$response.tag_name
    if($tag -notmatch '^v(\d+\.\d+\.\d+)$') {throw 'The official latest release has an unexpected version tag.'}
    $v=[Version]$Matches[1]
    $expectedName = 'Blood.on.the.Clocktower.Online_{0}_x64-setup.exe' -f $v.ToString(3)
    $asset = @($response.assets | Where-Object { [string]$_.name -ceq $expectedName }) | Select-Object -First 1
    if($null -eq $asset) {throw 'The official release does not have a Windows x64 installer.'}
    $link=[string]$asset.browser_download_url
    $prefix='https://github.com/ThePandemoniumInstitute/botc-release/releases/download/'+$tag+'/'
    if(-not $link.StartsWith($prefix,[StringComparison]::Ordinal) -or
      $link.Substring($prefix.Length) -cne $expectedName) {throw 'The official release returned an unexpected download URL.'}
    $digest=[string]$asset.digest
    if($digest -notmatch '^sha256:([a-fA-F0-9]{64})$') {throw 'The official release does not publish a SHA-256 hash for its installer.'}
    $sha=$Matches[1].ToLowerInvariant()
    $size=[long]$asset.size
    if($size -lt 1MB -or $size -gt 250MB) {throw 'The official installer has an unexpected size.'}
    return [pscustomobject]@{Version=$v; Url=$link; SHA256=$sha; Size=$size; Tag=$tag}
}
function Get-SignedGrimRelease {
    # The existing updater verifies manifest.sig using the pinned public key.
    # Do not parse the unauthenticated public manifest in the GUI.
    $checker = Join-Path $script:desktopDir 'Check-GrimPosterUpdate.ps1'
    if(-not (Test-Path -LiteralPath $checker -PathType Leaf)) {return $null}
    $installedVersion = if($script:botc) {$script:botc.Version.ToString(3)} else {''}
    $hash = ''
    if($script:botc) {
        $hash=(Get-FileHash -LiteralPath $script:botc.Path -Algorithm SHA256).Hash
    }
    $data = & $checker -StatusOnly -InstalledAppVersion $installedVersion -InstalledExeSha256 $hash
    if(-not $data) {return $null}
    $release = ($data | Out-String | ConvertFrom-Json)
    if(-not $release.signatureVerified) {return $null}
    return $release
}
function Test-LocalGrimInstalled {
    if(-not $script:botc) {return $false}
    $manifest=Get-Content -LiteralPath (Join-Path $script:desktopDir 'patch\patch-manifest.json') -Raw | ConvertFrom-Json
    if($script:botc.Version -ne [Version]([string]$manifest.appVersion)) {return $false}
    try {
        $actual=(Get-FileHash -LiteralPath $script:botc.Path -Algorithm SHA256).Hash
        if ([string]$manifest.expectedPatchedSha256 -match '^[a-fA-F0-9]{64}$') {
            return $actual -ieq [string]$manifest.expectedPatchedSha256
        }
        $payload=Join-Path $script:desktopDir 'patch\grim-poster-main.br'
        # Load strict verification helpers only in this child scope so the
        # manager's existing GUI code is unaffected by their StrictMode.
        return (& {
            . (Join-Path $script:desktopDir 'GrimPoster.Common.ps1')
            Test-InstalledDerivedPatch -Path $script:botc.Path -Manifest $manifest -PayloadPath $payload
        })
    } catch {return $false}
}
function Set-UIState {
    $localPatchManifest=(Get-Content -LiteralPath (Join-Path $script:desktopDir 'patch\patch-manifest.json') -Raw | ConvertFrom-Json)
    $script:compatiblePatchVersion=[Version]([string]$localPatchManifest.appVersion)
    $script:gpVersion=[string]$localPatchManifest.modVersion
    if($null -eq $script:botc) {
        $installedLabel.Text='BOTC installed: Not detected'
        $btnBotc.Text='Install BOTC Online'
    } else {
        $installedLabel.Text='BOTC installed: v'+$script:botc.Version.ToString(3)
        $btnBotc.Text='Check / Update BOTC'
    }
    if($script:release) {
        $latestLabel.Text='Official latest: v'+$script:release.Version.ToString(3)
        if($script:botc -and $script:botc.Version -ge $script:release.Version) {
            $btnBotc.Text='BOTC is up to date'; $btnBotc.Enabled=$false
        } else {
            $btnBotc.Text='Install / Update BOTC v'+$script:release.Version.ToString(3)
            $btnBotc.Enabled=$true
        }
    } else {
        $latestLabel.Text='Official latest: Could not check GitHub'; $btnBotc.Enabled=$false
    }
    $localSupported = [bool]($script:botc -and $script:botc.Version -eq $script:compatiblePatchVersion)
    $remoteSupported = [bool]($script:remoteGrim -and $script:remoteGrim.compatible)
    $updateAvailable = [bool]($remoteSupported -and $script:remoteGrim.updateAvailable)
    $btnPatch.Enabled=[bool]($localSupported -or $updateAvailable)
    $btnLaunch.Enabled=[bool]($localSupported -or $remoteSupported)
    if($script:remoteGrim) {
        $grimReleaseLabel.Text='Latest signed Grim Poster: v'+$script:remoteGrim.modVersion+' (BOTC v'+$script:remoteGrim.appVersion+')'
    } else {
        $grimReleaseLabel.Text='Latest signed Grim Poster: Unavailable - offline bundle remains usable'
    }
    if($updateAvailable) {
        $btnPatch.Text='Install Grim Poster v'+$script:remoteGrim.modVersion
        $patchLabel.Text='New signed version ready for your installed BOTC. The update is downloaded from your relay.'
    } elseif($localSupported) {
        $btnPatch.Text='Install / Repair Grim Poster'
        $patchLabel.Text='Local Grim Poster v'+$script:gpVersion+' supports your BOTC. Use Install / Repair if needed.'
    } elseif($remoteSupported) {
        $btnPatch.Text='Grim Poster up to date'
        $patchLabel.Text='The signed release supports your BOTC. You can launch with the companion.'
    } elseif($script:botc) {
        $btnPatch.Text='Grim Poster not yet available'
        $patchLabel.Text='No compatible patch yet for installed BOTC v'+$script:botc.Version.ToString(3)+'.'
    } else {
        $btnPatch.Text='Install Grim Poster'
        $patchLabel.Text='Install BOTC first. Grim Poster checks the signed release feed automatically.'
    }
}
function Refresh-Status {
    $btnRefresh.Enabled=$false
    $status.Text='Checking official BOTC GitHub and signed Grim Poster releases...'
    [System.Windows.Forms.Application]::DoEvents()
    $script:release=$null
    $script:remoteGrim=$null
    $script:botc=Get-InstalledBOTC
    $messages=@()
    try {$script:release=Get-OfficialLatest}
    catch {$messages+=('GitHub: '+$_.Exception.Message)}
    try {$script:remoteGrim=Get-SignedGrimRelease}
    catch {$messages+=('Grim Poster relay: '+$_.Exception.Message)}
    Set-UIState
    if($messages.Count -gt 0) {$status.Text=($messages -join ' | ')}
    else {$status.Text='Ready - downloads and installation require confirmation.'}
    $btnRefresh.Enabled=$true
}
function Do-InstallSignedGrimPoster {
    if(-not $script:remoteGrim -or -not $script:remoteGrim.updateAvailable) {return $false}
    if(Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {
        Show-Failure 'Close BOTC Online before updating Grim Poster.';return $false
    }
    $checker=Join-Path $script:desktopDir 'Check-GrimPosterUpdate.ps1'
    $status.Text='Opening the verified Grim Poster updater...'
    [System.Windows.Forms.Application]::DoEvents()
    try {
        # This is the existing RSA-verified downloader and installer. It
        # re-fetches and re-verifies everything; the GUI's check is advisory.
        $argsText='-NoLogo -NoProfile -File "'+$checker+'"'
        $p=Start-Process -FilePath 'powershell.exe' -ArgumentList $argsText -Wait -PassThru
        Refresh-Status
        # A signed download can succeed while the patch install fails. Confirm
        # the actual executable was patched, rather than trusting process exit 0.
        if($p.ExitCode -ne 0 -or -not (Test-LocalGrimInstalled)) {return $false}
        return $true
    } catch {
        Show-Failure ('Could not start the signed updater: '+$_.Exception.Message)
        Refresh-Status
        return $false
    }
}
function Make-Shortcut([string]$destination, [string]$launcherPath) {
    $shell=New-Object -ComObject WScript.Shell
    $link=$shell.CreateShortcut($destination)
    $link.TargetPath=$launcherPath
    $link.WorkingDirectory=(Split-Path $launcherPath -Parent)
    $link.Description='Grim Poster - Blood on the Clocktower Online'
    $link.Save()
}
function Do-InstallOfficial {
    if($null -eq $script:release) {Show-Failure 'GitHub has not been checked. Select Refresh first.';return}
    if(Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {Show-Failure 'Close BOTC Online before installing or updating it.';return}
    $msg='Download and run the OFFICIAL BOTC v'+$script:release.Version.ToString(3)+" installer from The Pandemonium Institute on GitHub?`n`nThe download will be checked against GitHub's published SHA-256 hash."
    if($script:release.Version -ne $script:compatiblePatchVersion -and
       (-not $script:remoteGrim -or $script:release.Version.ToString(3) -ne [string]$script:remoteGrim.appVersion)) {
        $msg+="`n`nWarning: Grim Poster does not yet have a compatible patch for this BOTC version. You can install BOTC, but Grim Poster won't launch until a matching signed update is published."
    }
    if(-not (Ask-YesNo $msg 'Install official BOTC Online')) {return}
    $path=Join-Path ([IO.Path]::GetTempPath()) ('BOTC-official-'+[Guid]::NewGuid().ToString('N')+'.exe')
    $client=New-Object Net.WebClient
    $client.Headers.Add('User-Agent','GrimPoster-Windows-Launcher/0.1.52')
    $btnBotc.Enabled=$false
    $btnRefresh.Enabled=$false
    $status.Text='Downloading official BOTC installer; keep Grim Poster open...'
    $progress.Style=[System.Windows.Forms.ProgressBarStyle]::Marquee
    $progress.Visible=$true
    [System.Windows.Forms.Application]::DoEvents()
    try {
        $task=$client.DownloadFileTaskAsync([Uri]$script:release.Url,$path)
        while(-not $task.IsCompleted) {
            [System.Windows.Forms.Application]::DoEvents()
            Start-Sleep -Milliseconds 75
        }
        $task.GetAwaiter().GetResult()
        $actual=(Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()
        $bytes=(Get-Item -LiteralPath $path).Length
        if($bytes -ne $script:release.Size -or $actual -ne $script:release.SHA256) {
            throw 'Installer SHA-256 or size did not match the official GitHub release. It has not been run.'
        }
        $status.Text='Verified the official installer. Opening BOTC setup...'
        [System.Windows.Forms.Application]::DoEvents()
        $setup=Start-Process -FilePath $path -PassThru
        Show-Notice "The verified official BOTC installer has opened. Complete its setup, then select Refresh in Grim Poster.`n`nDo not delete the installer while setup is running."
    } catch {Show-Failure ('BOTC download or verification failed: '+$_.Exception.Message)}
    finally {
        $client.Dispose()
        $progress.Visible=$false
        $btnRefresh.Enabled=$true
        if($null -ne $script:botc) {$btnBotc.Enabled=$true}
        $status.Text='Refresh after BOTC setup finishes.'
        # The temporary installer is retained while the independent Tauri setup runs.
    }
}
function Do-InstallGrimPoster {
    if($script:remoteGrim -and $script:remoteGrim.updateAvailable) {
        [void](Do-InstallSignedGrimPoster)
        return
    }
    if(Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {Show-Failure 'Close BOTC Online before installing Grim Poster.';return}
    if(-not $script:botc -or $script:botc.Version -ne $script:compatiblePatchVersion) {Show-Notice 'This locally bundled patch is not compatible with the installed BOTC version. Do not force an executable patch.';return}
    $installer=Join-Path $script:desktopDir 'Install-GrimPoster.ps1'
    if(-not (Ask-YesNo ('Install or repair Grim Poster '+$script:gpVersion+' for BOTC '+$script:compatiblePatchVersion+'?'))) {return}
    try {
        $p=Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoLogo','-NoProfile','-File',('"'+$installer+'"')) -Wait -PassThru
        if($p.ExitCode -ne 0) {Show-Failure ('The Grim Poster installer exited with code '+$p.ExitCode+'. Check its installer output.');return}
        Show-Notice 'Grim Poster installation complete. You can now launch BOTC through Grim Poster.'
        Refresh-Status
    } catch {Show-Failure $_.Exception.Message}
}
function Do-Launch {
    if(-not $script:botc) {Show-Notice 'Please install BOTC Online first.';return $false}
    $compatible=($script:botc.Version -eq $script:compatiblePatchVersion -or
      ($script:remoteGrim -and $script:remoteGrim.compatible))
    if(-not $compatible) {Show-Notice 'Wait for the signed Grim Poster patch compatible with your installed BOTC version.';return $false}
    if(Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {Show-Notice 'BOTC is already running. Close it before starting the companion.';return $false}
    if($script:remoteGrim -and $script:remoteGrim.updateAvailable) {
        if(-not (Ask-YesNo ('A signed Grim Poster v'+$script:remoteGrim.modVersion+' update is available. Install before launching?') 'Grim Poster update')) {return $false}
        if(-not (Do-InstallSignedGrimPoster)) {return $false}
        if($script:remoteGrim -and $script:remoteGrim.updateAvailable) {return $false}
    }
    if(-not (Test-LocalGrimInstalled)) {
        Show-Notice 'Grim Poster needs to be installed first. Select Install / Repair Grim Poster, then launch.'
        return $false
    }
    $companion=Join-Path $script:desktopDir 'Start-GrimPosterCompanion.ps1'
    try {
        Start-Process -FilePath 'powershell.exe' -WorkingDirectory $script:desktopDir -ArgumentList @('-NoLogo','-NoProfile','-File',('"'+$companion+'"'))
        $status.Text='Launching BOTC with the Grim Poster companion...'
        return $true
    } catch {Show-Failure $_.Exception.Message;return $false}
}
function Test-ReadyForAutoLaunch {
    # Fail closed on unknown online status or pending update: show the manager instead.
    if($FirstLaunch -or -not $script:botc -or -not $script:release -or -not $script:remoteGrim) {return $false}
    if($script:botc.Version -lt $script:release.Version) {return $false}
    if(-not $script:remoteGrim.signatureVerified -or -not $script:remoteGrim.compatible -or
        $script:remoteGrim.updateAvailable -or -not $script:remoteGrim.patchInstalled) {return $false}
    return [bool](Test-LocalGrimInstalled)
}

$form=New-Object System.Windows.Forms.Form
$form.Text='Grim Poster - App Manager'
$form.Size=[System.Drawing.Size]::new(675,566)
$form.MinimumSize=[System.Drawing.Size]::new(675,566)
$form.MaximumSize=[System.Drawing.Size]::new(675,566)
$form.StartPosition='CenterScreen'
$form.BackColor=[System.Drawing.Color]::FromArgb(22,21,31)
$form.ForeColor=[System.Drawing.Color]::White
$form.Font=[System.Drawing.Font]::new('Segoe UI',10)

function Make-Label([string]$text,[int]$x,[int]$y,[int]$w,[int]$h,[int]$size=10) {
    $l=New-Object System.Windows.Forms.Label
    $l.Location=[System.Drawing.Point]::new($x,$y)
    $l.Size=[System.Drawing.Size]::new($w,$h)
    $l.ForeColor=[System.Drawing.Color]::FromArgb(228,221,236)
    $l.Text=$text
    $l.Font=[System.Drawing.Font]::new('Segoe UI',$size)
    $form.Controls.Add($l)
    return $l
}
function Make-Button([string]$text,[int]$x,[int]$y,[int]$w,[int]$h,[bool]$primary=$false) {
    $b=New-Object System.Windows.Forms.Button
    $b.Location=[System.Drawing.Point]::new($x,$y)
    $b.Size=[System.Drawing.Size]::new($w,$h)
    $b.Text=$text
    $b.FlatStyle=[System.Windows.Forms.FlatStyle]::Flat
    $b.FlatAppearance.BorderSize=1
    $b.FlatAppearance.BorderColor=[System.Drawing.Color]::FromArgb(112,92,134)
    $b.ForeColor=[System.Drawing.Color]::White
    if($primary) {$b.BackColor=[System.Drawing.Color]::FromArgb(100,59,138)}
    else {$b.BackColor=[System.Drawing.Color]::FromArgb(44,39,55)}
    $b.Cursor=[System.Windows.Forms.Cursors]::Hand
    $form.Controls.Add($b)
    return $b
}
$title=Make-Label 'GRIM POSTER' 24 17 440 39 21
$title.ForeColor=[System.Drawing.Color]::FromArgb(255,189,70)
$subtitle=Make-Label 'Blood on the Clocktower Online - launcher & update manager' 25 62 590 23 10
$installedLabel=Make-Label 'BOTC installed: Checking...' 25 106 500 27 12
$latestLabel=Make-Label 'Official latest: Checking...' 25 140 500 27 12
$grimReleaseLabel=Make-Label 'Checking signed Grim Poster release...' 25 181 625 27 11
$patchLabel=Make-Label 'Checking Grim Poster compatibility...' 25 216 625 44 10
$btnBotc=Make-Button 'Check / Update BOTC' 25 278 280 44
$btnPatch=Make-Button 'Install / Update Grim Poster' 323 278 310 44
$btnLaunch=Make-Button 'Launch BOTC with Grim Poster' 25 339 608 50 $true
$btnRefresh=Make-Button 'Refresh' 25 403 105 33
$btnOfficial=Make-Button 'Official releases' 146 403 153 33
$status=Make-Label 'Checking official GitHub and Grim Poster...' 25 452 610 38 9
$small=Make-Label 'BOTC comes from official GitHub; Grim Poster uses your signed relay. Both require consent.' 25 497 625 18 8
$small.ForeColor=[System.Drawing.Color]::FromArgb(169,155,177)
$progress=New-Object System.Windows.Forms.ProgressBar
$progress.Location=[System.Drawing.Point]::new(315,413)
$progress.Size=[System.Drawing.Size]::new(318,13)
$progress.Visible=$false
$form.Controls.Add($progress)
$btnBotc.Add_Click({Do-InstallOfficial})
$btnPatch.Add_Click({Do-InstallGrimPoster})
$btnLaunch.Add_Click({[void](Do-Launch)})
$btnRefresh.Add_Click({Refresh-Status})
$btnOfficial.Add_Click({Start-Process $script:officialReleasePage})

$form.Add_Shown({
    if($FirstLaunch) {
        try {
            $lnk=Join-Path ([Environment]::GetFolderPath('Programs')) 'Grim Poster.lnk'
            Make-Shortcut -destination $lnk -launcherPath $script:launcher
            if(Ask-YesNo 'Grim Poster is ready. Create a desktop shortcut too?') {
                Make-Shortcut -destination (Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) 'Grim Poster.lnk') -launcherPath $script:launcher
            }
        } catch { $status.Text='Could not create a shortcut: '+$_.Exception.Message }
    }
    Refresh-Status
    if(Test-ReadyForAutoLaunch) {
        # Everything is current: skip requiring a second click and close the manager.
        if(Do-Launch) {$form.Close()}
    }
})
[void]$form.ShowDialog()
$form.Dispose()
