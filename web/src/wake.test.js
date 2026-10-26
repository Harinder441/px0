import test from 'node:test';
import assert from 'node:assert/strict';

import { wokeFromSleep } from './wake.js';

test('a late tick in a visible tab is a wake; in a hidden tab, or on time, it is not', () => {
  assert.equal(wokeFromSleep(3500, 'visible'), true);
  assert.equal(wokeFromSleep(60_000, 'hidden'), false);
  assert.equal(wokeFromSleep(1000, 'visible'), false);
});
