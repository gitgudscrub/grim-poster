import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const manifest = JSON.parse(fs.readFileSync('desktop/patch/patch-manifest.json','utf8'));
const source = fs.readFileSync('desktop/Install-GrimPoster.ps1','utf8');
test('missing supportedPreviousOfficialSha256 is handled with strict mode', ()=>{
  assert.match(source, /\$manifest\.PSObject\.Properties\['supportedPreviousOfficialSha256'\]/);
  assert.match(source, /if \(\$null -ne \$previousHashField\)/);
  assert.doesNotMatch(source, /@\(\$manifest\.supportedPreviousOfficialSha256\)/);
});
test('known previously supported BOTC executable hash is validated', ()=>{
  const known='efc0abc595ac889d2cb33d11224313cfe83a97da4fc75b76cf2d25ac7c36e843';
  assert.ok(manifest.supportedPreviousOfficialSha256.includes(known));
  assert.match(source, /verifiedPreviousHashes -notcontains \$existingRecoveryHash/);
  assert.match(source, /Install-FileWithRollback/);
});

test('new bundled patch prevents downgrade to older signed package after failed install', ()=>{
  const checker = fs.readFileSync('desktop/Check-GrimPosterUpdate.ps1','utf8');
  assert.match(checker, /\$savedVersion -gt \$installedVersion/);
  assert.match(checker, /\$releaseVersion -gt \$installedVersion/);
});
