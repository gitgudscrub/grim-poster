param(
    [string]$PrivateKeyPath = (Join-Path $env:USERPROFILE 'Documents\GrimPosterUpdateSigningKey.xml')
)

$ErrorActionPreference = 'Stop'

$fullPrivatePath = [IO.Path]::GetFullPath($PrivateKeyPath)
$publicPath = [IO.Path]::ChangeExtension($fullPrivatePath, '.public.xml')
$directory = Split-Path -Parent $fullPrivatePath
if (-not (Test-Path -LiteralPath $directory -PathType Container)) {
    [void](New-Item -ItemType Directory -Path $directory -Force)
}
if (Test-Path -LiteralPath $fullPrivatePath) {
    throw "A signing key already exists at '$fullPrivatePath'. Choose a different path rather than replacing it."
}

$rsa = New-Object -TypeName Security.Cryptography.RSACryptoServiceProvider -ArgumentList 3072
$rsa.PersistKeyInCsp = $false
try {
    $privateXml = $rsa.ToXmlString($true)
    $publicXml = $rsa.ToXmlString($false)
    $utf8NoBom = New-Object -TypeName Text.UTF8Encoding -ArgumentList $false
    [IO.File]::WriteAllText($fullPrivatePath, $privateXml, $utf8NoBom)
    [IO.File]::WriteAllText($publicPath, $publicXml, $utf8NoBom)

    $identity = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $privateAcl = New-Object Security.AccessControl.FileSecurity
    $privateAcl.SetAccessRuleProtection($true, $false)
    $privateRule = New-Object -TypeName Security.AccessControl.FileSystemAccessRule -ArgumentList $identity, 'FullControl', 'Allow'
    [void]$privateAcl.AddAccessRule($privateRule)
    [IO.File]::SetAccessControl($fullPrivatePath, $privateAcl)
} finally { $rsa.Dispose() }

Write-Host 'Keep the private key on your own computer. Do not upload it to the homelab or share it.' -ForegroundColor Yellow
Write-Host "Private key: $fullPrivatePath"
Write-Host "Public key:  $publicPath"
Write-Host 'Send only the contents of the .public.xml file to the updater maintainer.'
