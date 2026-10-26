// Chrome throttles hidden-tab timers, so a late tick there is not a wake; reconnecting would hold one of 6 per-host connections.
export function wokeFromSleep(gapMs, visibility) {
  return gapMs > 3000 && visibility === 'visible';
}
