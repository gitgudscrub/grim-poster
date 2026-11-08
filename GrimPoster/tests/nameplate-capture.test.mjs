// BOTC's newer cosmetic nameplates must not leak into captured Grimoire images.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const source = fs.readFileSync(fileURLToPath(new URL('../src/grim-poster.js', import.meta.url)), 'utf8');
const start = source.indexOf('  function addNameplateCaptureStyles(');
const end = source.indexOf('  function canvasBlob(', start);
assert(start >= 0 && end > start, 'Nameplate capture helpers are present');

function makeNameplate(originalClasses, inlineStyle = null) {
  const classes = new Set(originalClasses.split(' '));
  let currentStyle = inlineStyle;
  return {
    classList: {
      [Symbol.iterator]: () => classes.values(),
      add: value => classes.add(value),
      remove: value => classes.delete(value),
      contains: value => classes.has(value),
    },
    style: {
      setProperty(name, value) {
        currentStyle = `${currentStyle || ''};${name}:${value}`;
      },
    },
    getAttribute(name) {
      return name === 'class' ? [...classes].join(' ') : currentStyle;
    },
    setAttribute(name, value) {
      if (name === 'class') {
        classes.clear();
        value.split(' ').forEach(cls => classes.add(cls));
      } else {
        currentStyle = value;
      }
    },
    removeAttribute(name) {
      if (name === 'class') classes.clear();
      else currentStyle = null;
    },
  };
}

const modern = makeNameplate('nameplate border-halloween-friends level-42 relation-friend');
const legacy = makeNameplate('nameplate border-townsfolk level-patreon-7');
const untouched = makeNameplate('nameplate normal', 'color: white');
let captureStyle;
const document = {
  createElement(tag) {
    assert.equal(tag, 'style');
    return {remove() {this.removed = true;}};
  },
  head: {appendChild(style) {captureStyle = style;}},
};
const grimoire = {querySelectorAll(selector) {
  assert.equal(selector, '.nameplate');
  return [modern, legacy, untouched];
}};
const context = {document};
vm.runInNewContext(`${source.slice(start, end)}\n`
  + 'globalThis.add = addNameplateCaptureStyles; globalThis.restore = restoreNameplateCaptureStyles;', context);

const snapshot = context.add(grimoire);
assert(!modern.classList.contains('border-halloween-friends'));
assert(!modern.classList.contains('level-42'));
assert(!legacy.classList.contains('border-townsfolk'));
assert(!legacy.classList.contains('level-patreon-7'));
assert(modern.classList.contains('relation-friend'), 'Non-cosmetic state remains');
assert(modern.classList.contains('gp-capture-nameplate'));
assert(captureStyle.textContent.includes('::before'));

context.restore(snapshot);
assert.equal(modern.getAttribute('class'), 'nameplate border-halloween-friends level-42 relation-friend');
assert.equal(legacy.getAttribute('class'), 'nameplate border-townsfolk level-patreon-7');
assert.equal(untouched.getAttribute('style'), 'color: white');
assert.equal(modern.getAttribute('style'), null);
assert(captureStyle.removed, 'Temporary stylesheet must be removed');
console.log('Nameplate capture: new and old cosmetics are removed only for the snapshot, then restored.');
