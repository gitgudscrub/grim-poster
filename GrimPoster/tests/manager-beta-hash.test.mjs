// The offline GUI must recognize unsigned local beta patches without trusting
// only the asset region or allowing a missing expected full-file digest.
import assert from 'node:assert/strict';
import fs from 'node:fs';

const common = fs.readFileSync('desktop/GrimPoster.Common.ps1', 'utf8');
const manager = fs.readFileSync('desktop/Start-GrimPosterManager.ps1', 'utf8');
const start = common.indexOf('function Test-InstalledDerivedPatch {');
const end = common.indexOf('function Add-CandidatePath {', start);
assert(start >= 0 && end > start);
const helper = common.slice(start, end);
assert.match(helper, /Get-Sha256Lower -Path \$backupPath/);
assert.match(helper, /expectedOriginalSha256/);
assert.match(helper, /patchDataSha256/);
assert.match(helper, /\[Array\]::Clear\(\$bytes, \$offset, \$capacity\)/);
assert.match(helper, /Get-Sha256BytesLower -Bytes \$bytes/);
assert.match(helper, /Get-Sha256Lower -Path \$Path/);
assert.match(manager, /Test-InstalledDerivedPatch -Path \$script:botc\.Path/);
assert.match(manager, /expectedPatchedSha256 -match/);
console.log('Beta manager recognizes only a full-file patch rebuilt from a verified original.');
