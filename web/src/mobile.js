// web/src/mobile.js
import { $ } from './state.js';
import { layout, render } from './renderer.js';
import { on } from './bus.js';

/* Phone layout: only the open file and the file list. style.css hides the
   inspector, view switches and status bar under the same breakpoint; here the
   sidebar becomes a drawer that starts closed and closes once a file opens. */
export const PHONE_QUERY = '(max-width: 640px)';
const phone = matchMedia(PHONE_QUERY);

export const isPhone = () => phone.matches;

function setSide(open) {
  document.body.classList.toggle('side-hidden', !open);
  $('#btn-side-drawer')?.setAttribute('aria-expanded', String(open));
  layout();
  render();
}

export function collapseSideOnPhone() {
  if (isPhone()) setSide(false);
}

/* Boot: a phone opens on the file alone, or on the file list when no file is open. */
export function settlePhoneSide(hasFile) {
  if (isPhone()) setSide(!hasFile);
}

export function initMobile() {
  if (isPhone()) document.body.classList.add('side-hidden');
  $('#btn-side-drawer')?.addEventListener('click', () => setSide(document.body.classList.contains('side-hidden')));
  $('#side-scrim')?.addEventListener('click', () => setSide(false));
  phone.addEventListener('change', e => { if (e.matches) setSide(false); });
  on('tab:activated', () => collapseSideOnPhone());
}
