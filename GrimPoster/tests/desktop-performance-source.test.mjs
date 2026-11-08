import assert from 'node:assert/strict';
import fs from 'node:fs';
const companion = fs.readFileSync('desktop/Start-GrimPosterCompanion.ps1','utf8');
const src = fs.readFileSync('src/grim-poster.js','utf8');
const regexes = [
  /\$script:cdpSocket\s*=\s*\$null/,
  /function Open-CdpConnection/,
  /WebView2 screenshot connection ready/,
  /if \(-not \$listener\.Pending\(\)\) \{[\s\S]*Open-CdpConnection -Attempts 1/,
  /optimizeForSpeed\s*=\s*\$true/,
  /format\s*=\s*'png'/,
  /return ,\$png/,
  /Capture timings: connection/,
  /local HTTP transfer/,
  /The cached DevTools connection ended; reconnecting once/,
];
for (const re of regexes) assert.match(companion,re);
assert.match(src,/const MOD_VERSION = "0\.2\.0";/);
assert.match(src,/state\.stagePromise = prepareStage\(screenshot, cycle\)/);
assert.match(src,/resolveRevealReady\(\)/);
console.log('Desktop source performance and existing server-staging markers passed');
