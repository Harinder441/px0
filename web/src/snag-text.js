// web/src/snag-text.js
// The hos snag model has no path field, so where a snag was marked rides in its text.

function joinPath(root, rel) {
  const base = root.replace(/\/+$/, '');
  return rel ? base + '/' + rel : base;
}

export function snagText(note, meta, path) {
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  const lines = [note.trim(), '', 'px0 · ' + meta.name];
  if (path) lines.push('File: ' + joinPath(meta.root, path));
  lines.push('Folder: ' + joinPath(meta.root, dir));
  return lines.join('\n');
}
