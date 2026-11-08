// Native capture must not block End Game on server staging or IndexedDB.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const source = fs.readFileSync(fileURLToPath(new URL('../src/grim-poster.js', import.meta.url)), 'utf8');
const part = (a, b) => {
  const start = source.indexOf(a), end = source.indexOf(b, start + a.length);
  assert(start >= 0 && end > start);
  return source.slice(start, end);
};
function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return {promise, resolve};
}
const storage = deferred();
let hidden = false, snapshotCount = 0, toastCount = 0, postCount = 0;
const classList = {add(){},remove(){}};
const style = {setProperty(){}};
const grimoire = {classList};
const app = {classList};
const state = {
  capturePromise:null,revealReadyPromise:null,capturing:false,
  revealCycle:1,suppressObserver:false,config:{enabled:true,autoPost:true},
  pending:null,
};
let clickHandler;
const document = {
  body:{classList},
  querySelector(sel){return sel === '#grimoire'?grimoire:sel === '#app'?app:null;},
  querySelectorAll(){return [];},
  addEventListener(type,fn){if(type==='click') clickHandler = fn;},
};
const ctx = {
  state,document,console,window:{devicePixelRatio:1},
  isDesktopApp:()=>true,
  setCaptureScreen(value){hidden=!value;},
  saveClassState:()=>null,restoreClassState(){},addCaptureDefinitions:()=>null,
  addNameplateCaptureStyles:()=>null,addVideoRedactions:()=>[],
  findRevealPrompt:()=>null, nextFrame:async()=>{},
  captureWithWindowsCompanion:async()=>{snapshotCount++;return 'immutable-native-png';},
  prepareStage:async()=>null,
  detectWinner:()=> 'Good',detectScriptName:()=> 'Trouble Brewing',
  persistPending:async capture=>{await storage.promise;state.pending=capture;},
  toast:()=>{toastCount++;},updateStatusButton(){},
  restoreNameplateCaptureStyles(){},clearPending:async()=>{},
  // For the Close handler:
  revealHasStarted:()=>true,showGame2Prompt:()=>{},
  uploadPending:async()=>{postCount++;},
  setTimeout(fn){queueMicrotask(fn);},
};
vm.runInNewContext(`${part('  async function captureFinalGrimoire() {','  function captureToDataUrl(')}\n${part('  function installCloseHandler() {','  function checkRevealState()')}\nglobalThis.captureFinalGrimoire=captureFinalGrimoire;globalThis.installCloseHandler=installCloseHandler;`, ctx);
ctx.captureFinalGrimoire();
await state.revealReadyPromise;
assert.equal(snapshotCount,1,'Native screenshot completed before reveal unblocked');
assert(hidden, 'Grim capture styles were restored before reveal unblocked');
assert.equal(state.pending,null,'Storage is still in progress, so must not be posted yet');
assert(state.capturing,'Processing is still in progress');
// Closing the reveal in this window must defer posting until persistence finishes.
ctx.installCloseHandler();
const button = {};
clickHandler({target:{closest(sel){return sel.startsWith('#reveal ')?button:null;}}});
await Promise.resolve();
assert.equal(postCount,0);
storage.resolve();
await state.capturePromise;
await new Promise(resolve => setImmediate(resolve));

assert.equal(postCount,1,'Quickly closed reveal successfully posted after save');
assert.equal(state.pending.completedReveal,true);
assert.equal(state.pending.notifyGame2,undefined);
assert.equal(state.capturing,false);
assert(toastCount>0);
console.log('Desktop capture timing: native snapshot releases reveal early; post waits for storage.');
