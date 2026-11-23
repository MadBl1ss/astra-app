// pnpm contrast: the contrast of every text colour of src/lib/styles/tokens.css
// on every surface it is drawn on, in both themes. WCAG asks 4.5:1 for body
// text; the window holds every text colour to it.

import fs from 'node:fs';

const css = fs.readFileSync(new URL('../src/lib/styles/tokens.css', import.meta.url), 'utf8');

/** The --name: #rrggbb pairs of one block of the file. */
function tokens(block) {
  return Object.fromEntries([...block.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6})\b/gi)].map((m) => [m[1], m[2]]));
}

// The dark theme is the base block; the light one overrides it.
const blocks = css.split(/\[data-theme=['"]?light['"]?\]/);
const dark = tokens(blocks[0]);
const light = { ...dark, ...tokens(blocks.slice(1).join('')) };

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const lin = (c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function ratio(a, b) {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (l1 + 0.05) / (l2 + 0.05);
}

// Text colours and the surfaces they sit on. --line is not text: borders
// need 3:1 only against what they separate, and they are decoration here.
const texts = ['text', 'muted', 'vpn', 'warn', 'bad', 'direct'];
const grounds = ['bg', 'sky', 'surface', 'surface-2'];
let failed = 0;
for (const [name, theme] of [
  ['тёмная', dark],
  ['светлая', light],
]) {
  console.log(`\n${name}`);
  for (const t of texts) {
    const row = grounds.map((g) => {
      const r = ratio(theme[t], theme[g]);
      if (r < 4.5) failed++;
      return `${g} ${r.toFixed(2)}${r < 4.5 ? ' ✗' : ''}`;
    });
    console.log(`  --${t.padEnd(7)} ${row.join('   ')}`);
  }
}
console.log(failed === 0 ? '\nВсё не ниже 4,5:1.' : `\nНиже 4,5:1: ${failed}.`);
process.exitCode = failed === 0 ? 0 : 1;
