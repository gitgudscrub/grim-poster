import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const dir = new URL('../desktop/', import.meta.url);
const manager=fs.readFileSync(new URL('Start-GrimPosterManager.ps1',dir),'utf8');
const update=fs.readFileSync(new URL('Check-GrimPosterUpdate.ps1',dir),'utf8');
const signed=fs.readFileSync(new URL('../tools/Sign-GrimPosterRelease.ps1',dir),'utf8');
test('GUI retrieves verified latest Grim Poster release and can install it',()=>{
  assert.match(manager,/function Get-SignedGrimRelease/);
  assert.match(manager,/& \$checker -StatusOnly -InstalledAppVersion/);
  assert.match(manager,/\$script:remoteGrim=Get-SignedGrimRelease/);
  assert.match(manager,/if\(\$script:remoteGrim -and \$script:remoteGrim\.updateAvailable\)/);
  assert.match(manager,/Do-InstallSignedGrimPoster/);
  assert.match(manager,/Start-Process -FilePath 'powershell\.exe' -ArgumentList \$argsText -Wait/);
});
test('read-only signed metadata check never downloads the release ZIP',()=>{
  assert.match(update,/\[switch\]\$StatusOnly/);
  assert.match(update,/Test-ManifestSignature -ManifestBytes \$manifestBytes -SignatureBytes \$signatureBytes/);
  assert.match(update,/signatureVerified = \$true/);
  assert.match(update,/updateAvailable = \[bool\]\(\$compatible/);
  assert.ok(update.indexOf('if ($StatusOnly) {\n        $compatible') < update.indexOf('Get-UpdateBytes -Uri $assetUri'));
  assert.match(update,/if \(\$installedAppVersion -ne \$targetAppVersion\)/);
});
test('updater verifies signed package ZIP before applying patch',()=>{
  assert.match(update,/Get-FileHash -LiteralPath \$zipPath -Algorithm SHA256/);
  assert.match(update,/packageManifest\.appVersion/);
  assert.match(update,/Get-FileHash -LiteralPath \$path -Algorithm SHA256|Get-UpdateBytes/);
  assert.match(signed,/SignatureFormatter/);
});
