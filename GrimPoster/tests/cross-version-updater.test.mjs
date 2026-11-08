import assert from 'node:assert/strict';
import fs from 'node:fs';

const updater = fs.readFileSync('desktop/Check-GrimPosterUpdate.ps1', 'utf8');
const installer = fs.readFileSync('desktop/Install-GrimPoster.ps1', 'utf8');
const signer = fs.readFileSync('tools/Sign-GrimPosterRelease.ps1', 'utf8');

// Source-level safeguards. Live PowerShell execution requires Windows.
assert(!updater.includes("if ([string]$release.appVersion -ne '3.58.0')"));
assert.match(updater, /function Get-InstalledBotcAppVersion/);
assert.match(updater, /VersionInfo\.ProductVersion/);
assert.match(updater, /\$installedAppVersion -ne \$targetAppVersion/);
assert.match(updater, /\$installedAppVersion -ne \$targetAppVersion/);
assert.match(updater, /Test-ManifestSignature -ManifestBytes \$manifestBytes -SignatureBytes \$signatureBytes/);
assert.match(updater, /\$releaseVersion -lt \$installedVersion/);
assert.match(updater, /\$releaseVersion -eq \$installedVersion/);
assert.match(updater, /\$currentHash -ieq \[string\]\$installedManifest.expectedPatchedSha256/);
assert.match(updater, /\$packageManifest\.appVersion -ne \[string\]\$release\.appVersion/);
assert.match(updater, /-PreviousOriginalSha256 \{1\}/);
assert.match(installer, /param\([\s\S]*PreviousOriginalSha256/);
assert.match(installer, /verifiedPreviousHashes -notcontains \$existingRecoveryHash/);
assert.match(installer, /expectedOriginalSha256/);
assert.match(installer, /Install-FileWithRollback/);
assert.match(signer, /ZipFile\]::OpenRead/);
assert.match(signer, /\$patchManifest\.appVersion/);
assert.match(signer, /appVersion = \$appVersion\.ToString\(\)/);
assert(!signer.includes("appVersion = '3.58.0'"));

// Expected selection rules, independent of specific app version.
function eligible({ installedBotc, signedTargetBotc, installedGP, signedGP }) {
  return installedBotc === signedTargetBotc &&
    signedGP.localeCompare(installedGP, undefined, {numeric: true}) > 0;
}
assert(eligible({installedBotc:'3.58.0',signedTargetBotc:'3.58.0',installedGP:'0.1.47',signedGP:'0.1.48'}));
assert(eligible({installedBotc:'3.59.0',signedTargetBotc:'3.59.0',installedGP:'0.1.48',signedGP:'0.1.49'}));
assert(!eligible({installedBotc:'3.59.0',signedTargetBotc:'3.58.0',installedGP:'0.1.48',signedGP:'0.1.49'}));
assert(!eligible({installedBotc:'3.58.0',signedTargetBotc:'3.59.0',installedGP:'0.1.48',signedGP:'0.1.49'}));
assert(!eligible({installedBotc:'3.59.0',signedTargetBotc:'3.59.0',installedGP:'0.1.49',signedGP:'0.1.49'}));
console.log('Cross-version updater: signed matching-version selection, prior backup migration, safety guards (source-level) passed');
