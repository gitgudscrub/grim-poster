import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
const dir = new URL('../desktop/', import.meta.url);
const manager = fs.readFileSync(new URL('Start-GrimPosterManager.ps1',dir),'utf8');
const update = fs.readFileSync(new URL('Check-GrimPosterUpdate.ps1',dir),'utf8');
test('GUI remains readable in Windows PowerShell 5.1 and avoids mojibake',()=>{
  assert.ok(manager.startsWith('\uFEFF'), 'manager has UTF8 BOM');
  assert.ok(update.startsWith('\uFEFF'), 'checker has UTF8 BOM');
  assert.doesNotMatch(manager,/â€|â€“|â€”/);
  assert.doesNotMatch(manager,/[^\x00-\x7f\uFEFF]/);
});
test('launches directly only when both official game and signed patch are up to date',()=>{
  assert.match(manager,/function Test-ReadyForAutoLaunch/);
  assert.match(manager,/\$script:botc.Version -lt \$script:release.Version/);
  assert.match(manager,/\$script:remoteGrim\.updateAvailable/);
  assert.match(manager,/\$script:remoteGrim\.patchInstalled/);
  assert.match(manager,/if\(Test-ReadyForAutoLaunch\)/);
  assert.match(manager,/if\(Do-Launch\) \{\$form.Close\(\)\}/);
});
test('installer runs in current user, captures errors and surfaces the underlying log',()=>{
  assert.match(update,/-RedirectStandardOutput \$logOutput -RedirectStandardError \$logErrors/);
  assert.doesNotMatch(update, /Start-Process[^\n]+-Verb RunAs/);
  assert.match(update,/Get-Content -LiteralPath \$logOutput -Tail 12/);
  assert.match(update,/Full logs: \$logDirectory/);
});
