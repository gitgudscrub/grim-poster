import fs from 'node:fs';
import assert from 'node:assert/strict';
import vm from 'node:vm';
const runtime = fs.readFileSync('launcher-src/runtime.js','utf8');
const source = fs.readFileSync('src/grim-poster.js','utf8');
assert(runtime.includes(source));
assert(!fs.readdirSync('desktop/patch').some(name => name.endsWith('.br')));
const m = JSON.parse(fs.readFileSync('desktop/patch/patch-manifest.json','utf8'));
assert.equal(m.patchVerification,'runtime-injection');
assert.equal(m.expectedPatchedSha256,m.expectedOriginalSha256);
assert(!runtime.includes('botc-app@3.58.0'));
// An untrusted navigation must return before accessing any DOM or storage API.
vm.runInNewContext(runtime,{location:{origin:'https://example.com'}});
console.log('Own-source runtime bundle and origin restriction verified');
