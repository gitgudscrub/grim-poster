param(
    [Parameter(Mandatory = $true)][string]$PrivateKeyPath,
    [Parameter(Mandatory = $true)][string]$PackagePath,
    [Parameter(Mandatory = $true)][string]$ModVersion,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'

$version = [Version]'0.0'
if (-not [Version]::TryParse($ModVersion, [ref]$version)) { throw 'ModVersion must be a dotted numeric version.' }
if (-not (Test-Path -LiteralPath $PrivateKeyPath -PathType Leaf)) { throw 'The private signing key file does not exist.' }
if (-not (Test-Path -LiteralPath $PackagePath -PathType Leaf)) { throw 'The update package ZIP does not exist.' }

# Read the BOTC compatibility version from the package being signed.
# Do NOT hard-code it: Grim Poster updates must follow BOTC version upgrades.
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [IO.Compression.ZipFile]::OpenRead((Resolve-Path -LiteralPath $PackagePath).Path)
try {
    $patchManifestEntries = @($archive.Entries | Where-Object { $_.FullName -match '(^|/)desktop/patch/patch-manifest\.json$' })
    if ($patchManifestEntries.Count -ne 1) { throw 'The release ZIP must contain exactly one desktop patch manifest.' }
    $entryStream = $patchManifestEntries[0].Open()
    try {
        $reader = [IO.StreamReader]::new($entryStream, [Text.Encoding]::UTF8)
        try { $patchManifest = $reader.ReadToEnd() | ConvertFrom-Json }
        finally { $reader.Dispose() }
    } finally { $entryStream.Dispose() }
} finally { $archive.Dispose() }
if ([string]$patchManifest.modVersion -ne $version.ToString()) {
    throw 'The ZIP patch version does not match -ModVersion.'
}
$appVersion = [Version]'0.0'
if (-not [Version]::TryParse([string]$patchManifest.appVersion, [ref]$appVersion)) {
    throw 'The release ZIP declares an invalid BOTC app version.'
}

$manifestDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$releaseDirectory = Join-Path $manifestDirectory 'releases'
[void](New-Item -ItemType Directory -Path $releaseDirectory -Force)
$packageName = 'BOTC-Grim-Poster-{0}.zip' -f $version.ToString()
$publishedPackage = Join-Path $releaseDirectory $packageName
Copy-Item -LiteralPath $PackagePath -Destination $publishedPackage -Force
$package = Get-Item -LiteralPath $publishedPackage
$manifest = [ordered]@{
    formatVersion = 1
    appVersion = $appVersion.ToString()
    modVersion = $version.ToString()
    asset = "releases/$packageName"
    packageBytes = [long]$package.Length
    sha256 = (Get-FileHash -LiteralPath $publishedPackage -Algorithm SHA256).Hash.ToLowerInvariant()
}
$manifestText = ($manifest | ConvertTo-Json -Compress)
$utf8NoBom = New-Object -TypeName Text.UTF8Encoding -ArgumentList $false
$manifestBytes = $utf8NoBom.GetBytes($manifestText)

$privateXmlText = Get-Content -LiteralPath $PrivateKeyPath -Raw
$privateXml = [xml]$privateXmlText
if (-not $privateXml.RSAKeyValue.D -or -not $privateXml.RSAKeyValue.Modulus) { throw 'The selected XML file does not contain a private RSA key.' }
$rsa = New-Object Security.Cryptography.RSACryptoServiceProvider
$sha = [Security.Cryptography.SHA256]::Create()
try {
    $rsa.FromXmlString($privateXmlText)
    $hash = $sha.ComputeHash($manifestBytes)
    $signer = New-Object -TypeName Security.Cryptography.RSAPKCS1SignatureFormatter -ArgumentList $rsa
    $signer.SetHashAlgorithm('SHA256')
    $signature = $signer.CreateSignature($hash)
} finally {
    $sha.Dispose()
    $rsa.Dispose()
}

[IO.File]::WriteAllBytes((Join-Path $manifestDirectory 'manifest.json'), $manifestBytes)
[IO.File]::WriteAllText((Join-Path $manifestDirectory 'manifest.sig'), [Convert]::ToBase64String($signature), $utf8NoBom)
Write-Host "Signed update is ready in: $manifestDirectory"
Write-Host 'Copy manifest.json, manifest.sig, and the releases directory to the update host.'
