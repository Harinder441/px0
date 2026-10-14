import test from 'node:test';
import assert from 'node:assert/strict';

import { snagText } from './snag-text.js';

const meta = { name: 'hos', root: '/Users/h/Repos/hos/' };

test('a snag carries the note, then the checkout, the open file and its folder', () => {
  assert.equal(
    snagText('  Pill overlaps the tabs \n', meta, 'web/src/shell/Layout.tsx'),
    'Pill overlaps the tabs\n\npx0 · hos\nFile: /Users/h/Repos/hos/web/src/shell/Layout.tsx\nFolder: /Users/h/Repos/hos/web/src/shell',
  );
});

test('a file at the root has the checkout as its folder, and no open file leaves only the folder', () => {
  assert.equal(snagText('x', meta, 'README.md'), 'x\n\npx0 · hos\nFile: /Users/h/Repos/hos/README.md\nFolder: /Users/h/Repos/hos');
  assert.equal(snagText('x', meta, ''), 'x\n\npx0 · hos\nFolder: /Users/h/Repos/hos');
});
