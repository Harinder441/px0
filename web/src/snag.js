// web/src/snag.js
// Mark Snag: logs the open file into the hos snag store (the same S-refs hos
// lists). Inert unless the server reports an hos daemon (S.meta.hos), so a
// plain px0 never shows it. The Go server forwards the POST (hos.go).
import { $, S, doc_, apiPostJson } from './state.js';
import { SEL_MENU_ITEMS, setSnagHandler } from './selbar.js';
import { COMMANDS } from './palette.js';
import { showToast } from './ui.js';
import { snagText } from './snag-text.js';

const KINDS = [
  { kind: 'task', label: 'Task', hint: 'A bug, slowness or something to change' },
  { kind: 'question', label: 'Question', hint: 'Something to ask or understand' },
];

// The same limits hos's snag store enforces.
const FILES_MAX = 10;
const BYTES_MAX = 25 * 1024 * 1024;

let dialog = null;
let kind = 'task';
let files = [];

function build() {
  const el = document.createElement('div');
  el.id = 'snag-dialog';
  el.className = 'modal-overlay';
  el.hidden = true;
  el.innerHTML =
    '<form class="snag-box" role="dialog" aria-modal="true" aria-labelledby="snag-title">' +
      '<header class="snag-head"><h2 id="snag-title">Mark Snag</h2><button type="button" class="mini" data-snag="close" title="Close (Esc)" aria-label="Close">&#10005;</button></header>' +
      '<div class="snag-kinds" role="radiogroup" aria-label="Kind">' +
        KINDS.map(k => '<button type="button" role="radio" data-kind="' + k.kind + '" title="' + k.hint + '">' + k.label + '</button>').join('') +
      '</div>' +
      '<textarea id="snag-note" rows="5" spellcheck="true" placeholder="What is wrong, or what do you want to know?" aria-label="Snag note"></textarea>' +
      '<div class="snag-files"><button type="button" class="settings-btn-secondary" data-snag="attach" title="Attach files (or paste or drop them here)">Attach files</button><input type="file" id="snag-file" multiple hidden><ul id="snag-list" aria-label="Attached files"></ul></div>' +
      '<div class="snag-where" id="snag-where"></div>' +
      '<div class="snag-err" id="snag-err" role="alert" hidden></div>' +
      '<footer class="snag-foot"><button type="button" class="settings-btn-secondary" data-snag="close">Cancel</button><button type="submit" class="settings-btn-primary" id="snag-send">Mark Snag</button></footer>' +
    '</form>';
  document.body.append(el);
  el.addEventListener('click', e => {
    if (e.target === el || e.target.closest('[data-snag="close"]')) closeSnag();
    const k = e.target.closest('[data-kind]');
    if (k) setKind(k.dataset.kind);
    if (e.target.closest('[data-snag="attach"]')) $('#snag-file').click();
    const rm = e.target.closest('[data-rm]');
    if (rm) { files.splice(Number(rm.dataset.rm), 1); renderFiles(); }
  });
  el.querySelector('#snag-file').addEventListener('change', e => { addFiles(e.target.files); e.target.value = ''; });
  el.addEventListener('paste', e => {
    if (e.clipboardData?.files.length) { e.preventDefault(); addFiles(e.clipboardData.files); }
  });
  el.addEventListener('dragover', e => { if (e.dataTransfer?.types.includes('Files')) e.preventDefault(); });
  el.addEventListener('drop', e => {
    if (!e.dataTransfer?.files.length) return;
    e.preventDefault();
    addFiles(e.dataTransfer.files);
  });
  el.addEventListener('keydown', e => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closeSnag(); }
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(); }
  });
  el.querySelector('form').addEventListener('submit', e => { e.preventDefault(); send(); });
  return el;
}

function showErr(msg) {
  const err = $('#snag-err');
  err.textContent = msg;
  err.hidden = !msg;
}

function addFiles(list) {
  const next = [...files, ...list];
  if (next.length > FILES_MAX) return showErr('A snag holds at most ' + FILES_MAX + ' files.');
  if (next.reduce((sum, f) => sum + f.size, 0) > BYTES_MAX) return showErr('Snag files are at most 25 MB in total.');
  files = next;
  showErr('');
  renderFiles();
}

function renderFiles() {
  const ul = $('#snag-list');
  ul.replaceChildren(...files.map((f, i) => {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = (f.name || 'pasted file') + ' · ' + Math.max(1, Math.round(f.size / 1024)) + ' KB';
    const rm = document.createElement('button');
    rm.type = 'button';
    rm.className = 'mini';
    rm.dataset.rm = String(i);
    rm.setAttribute('aria-label', 'Remove ' + (f.name || 'file'));
    rm.innerHTML = '&#10005;';
    li.append(name, rm);
    return li;
  }));
}

function toBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).slice(String(reader.result).indexOf(',') + 1));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

function setKind(next) {
  kind = next;
  for (const b of dialog.querySelectorAll('[data-kind]')) {
    const on = b.dataset.kind === kind;
    b.classList.toggle('active', on);
    b.setAttribute('aria-checked', String(on));
  }
}

export function openSnag(startKind = 'task') {
  dialog ??= build();
  const d = doc_();
  dialog.dataset.path = d?.path || '';
  $('#snag-where').textContent = snagText('', S.meta, d?.path || '').trim();
  showErr('');
  $('#snag-note').value = '';
  files = [];
  renderFiles();
  setKind(startKind);
  dialog.hidden = false;
  $('#snag-note').focus();
}

function closeSnag() {
  if (dialog) dialog.hidden = true;
}

async function send() {
  const note = $('#snag-note').value;
  if (!note.trim() && !files.length) return showErr('Write what the snag is about, or attach a file.');
  const btn = $('#snag-send');
  btn.disabled = true;
  try {
    const sent = await Promise.all(files.map(async (f, i) => ({ name: f.name || 'pasted-' + (i + 1), data: await toBase64(f) })));
    const { ref } = await apiPostJson('/api/hos/snag', { kind, text: snagText(note, S.meta, dialog.dataset.path), files: sent });
    closeSnag();
    showToast('✓', 'Marked ' + ref + ' in hos');
  } catch (e) {
    showErr(e.message);
  } finally {
    btn.disabled = false;
  }
}

function addSidebarButton() {
  const actions = $('#side .panel-actions');
  if (!actions || $('#btn-mark-snag')) return;
  const b = document.createElement('button');
  b.id = 'btn-mark-snag';
  b.className = 'mini';
  b.title = 'Mark Snag in hos';
  b.setAttribute('aria-label', 'Mark Snag');
  b.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 22V4"/><path d="M4 4h12l-2 4 2 4H4"/></svg>';
  b.addEventListener('click', () => openSnag());
  actions.prepend(b);
}

export function initSnag() {
  if (!S.meta?.hos) return;
  if (!SEL_MENU_ITEMS.some(item => item.sel === 'mark-snag')) SEL_MENU_ITEMS.push({ sel: 'mark-snag', label: 'Mark Snag' });
  setSnagHandler(() => openSnag());
  COMMANDS.push(
    { name: 'hos: Mark Snag (task)', run: () => openSnag('task') },
    { name: 'hos: Mark Snag (question)', run: () => openSnag('question') },
  );
  addSidebarButton();
}
