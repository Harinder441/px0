// web/src/fold.js
import { doc_ } from './state.js';
import { vp } from './ui.js';
import { layout, render } from './renderer.js';
import { getDocRaw } from './markdown.js';

const MAX_FOLD_BYTES = 8 << 20;

// Start line -> line of its closing bracket, for brackets that span lines.
export function jsonFolds(text) {
  const ends = new Map(), stack = [];
  let line = 1, inStr = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '\n') { line++; continue; }
    if (inStr) {
      if (c === '\\') i++;
      else if (c === '"') inStr = false;
      continue;
    }
    if (c === '"') inStr = true;
    else if (c === '{' || c === '[') stack.push(line);
    else if ((c === '}' || c === ']') && stack.length) {
      const open = stack.pop();
      if (line > open + 1 && line > (ends.get(open) || 0)) ends.set(open, line);
    }
  }
  return ends;
}

export function loadFolds(d) {
  if (!/\.json$/i.test(d.path) || d.diffRef || d.size > MAX_FOLD_BYTES) return;
  getDocRaw(d).then(t => {
    d.foldEnd = jsonFolds(t);
    d.folded = new Set();
    if (doc_() === d) render();
  }).catch(() => {});
}

function rebuild(d) {
  if (!d.folded.size) { d.vis = null; return; }
  const vis = [];
  for (let n = 1; n <= d.total; n++) {
    vis.push(n);
    if (d.folded.has(n)) n = d.foldEnd.get(n) - 1;
  }
  d.vis = vis;
}

export function toggleFold(d, n) {
  if (!d.foldEnd?.has(n)) return;
  if (d.folded.has(n)) d.folded.delete(n);
  else {
    d.folded.add(n);
    if (d.cur > n && d.cur < d.foldEnd.get(n)) d.cur = n;
  }
  rebuild(d);
  layout(); render();
}

export function unfoldAt(d, n) {
  if (!d?.folded?.size) return;
  let changed = false;
  for (const s of [...d.folded]) {
    if (n > s && n < d.foldEnd.get(s)) { d.folded.delete(s); changed = true; }
  }
  if (changed) { rebuild(d); layout(); }
}

export function initFold() {
  vp.addEventListener('click', e => {
    const el = e.target.closest('.fold');
    if (!el) return;
    const d = doc_();
    if (d) toggleFold(d, +el.dataset.l);
  });
}
