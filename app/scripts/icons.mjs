// Draws the tray icons and the application icon and writes them as PNG and
// ICO into src-tauri/icons/. The drawings are SVG built here, one template
// for all states, so the six tray icons cannot drift apart. Run by
// `pnpm icons` and before every `tauri build`; the output is committed,
// because the Rust side embeds it at compile time.

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Resvg } from '@resvg/resvg-js';
import { MARK_W, markElements } from '../src/lib/mark.ts';

const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'src-tauri', 'icons');

// Colours of the dark theme: a tray icon sits on the
// taskbar, not in the window, and has to read on both light and dark ones.
const C = { vpn: '#3ddc97', off: '#8b94a3', warn: '#f5b84b', bad: '#f2555a', ink: '#0f1216' };

// The mark sits on a rounded plate: a plate in the state's colour reads at
// 16 px where the mark's own cuts no longer do, and the colour is what the
// tray icon is looked at for.
const PLATE = '<rect x="1" y="1" width="30" height="30" rx="7"';

// The mark in a box of `w` px whose top-left corner is at (x, y).
function mark(id, fill, x, y, w) {
  return `<g transform="translate(${x} ${y}) scale(${w / MARK_W})">${markElements(id, fill)}</g>`;
}

// What is drawn on the plate. The state is never told by colour alone.
const glyphs = {
  // Raised a little: a triangle's weight is in its base.
  mark: mark('m', C.ink, 4, 7, 24),
  bang: `<path d="M16 8.500v8.500" fill="none" stroke="${C.ink}" stroke-width="3.600" stroke-linecap="round"/><circle cx="16" cy="22.500" r="2.100" fill="${C.ink}"/>`,
  lock: `<rect x="9.500" y="14.500" width="13" height="9.500" rx="2" fill="${C.ink}"/><path d="M12.200 14.500v-2.300a3.800 3.800 0 017.600 0v2.300" fill="none" stroke="${C.ink}" stroke-width="2.600"/>`,
};

const tray = {
  on: { fill: C.vpn, glyph: 'mark' },
  off: { fill: C.off, glyph: 'mark' },
  connecting: { fill: C.warn, glyph: 'mark' },
  // The second frame of the pulse.
  'connecting-dim': { fill: C.warn, glyph: 'mark', opacity: 0.45 },
  problem: { fill: C.warn, glyph: 'bang' },
  down: { fill: C.bad, glyph: 'mark' },
  blocked: { fill: C.bad, glyph: 'lock' },
};

function traySvg({ fill, glyph, opacity = 1 }) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g opacity="${opacity}">${PLATE} fill="${fill}"/>${glyphs[glyph]}</g></svg>`;
}

// The application icon: the white mark on a dark plate with a green glow,
// the look of the brand's pictures.
const appSvg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><defs><radialGradient id="glow" cx="0.85" cy="0.95" r="0.9"><stop offset="0" stop-color="${C.vpn}" stop-opacity="0.45"/><stop offset="1" stop-color="${C.vpn}" stop-opacity="0"/></radialGradient></defs><rect width="48" height="48" rx="10" fill="#10161a"/><rect width="48" height="48" rx="10" fill="url(#glow)"/>${mark('a', '#fff', 7, 12, 34)}</svg>`;

function png(svg, size) {
  return new Resvg(svg, { fitTo: { mode: 'width', value: size } }).render().asPng();
}

/** An .ico is a directory of images; since Vista each may be a whole PNG. */
function ico(images) {
  const header = Buffer.alloc(6 + 16 * images.length);
  header.writeUInt16LE(1, 2); // type: icon
  header.writeUInt16LE(images.length, 4);
  let offset = header.length;
  images.forEach(({ size, data }, i) => {
    const at = 6 + 16 * i;
    header.writeUInt8(size === 256 ? 0 : size, at); // width, 0 means 256
    header.writeUInt8(size === 256 ? 0 : size, at + 1);
    header.writeUInt16LE(1, at + 4); // colour planes
    header.writeUInt16LE(32, at + 6); // bits per pixel
    header.writeUInt32LE(data.length, at + 8);
    header.writeUInt32LE(offset, at + 12);
    offset += data.length;
  });
  return Buffer.concat([header, ...images.map((image) => image.data)]);
}

mkdirSync(join(out, 'tray'), { recursive: true });

// 32 px is what Windows asks for at 200 % scaling and shrinks for less.
for (const [name, spec] of Object.entries(tray)) {
  writeFileSync(join(out, 'tray', `${name}.png`), png(traySvg(spec), 32));
}

writeFileSync(join(out, '32x32.png'), png(appSvg, 32));
writeFileSync(join(out, '128x128.png'), png(appSvg, 128));
writeFileSync(join(out, 'icon.png'), png(appSvg, 512));
writeFileSync(
  join(out, 'icon.ico'),
  ico([16, 24, 32, 48, 64, 256].map((size) => ({ size, data: png(appSvg, size) }))),
);

console.log(`icons: ${Object.keys(tray).length} tray states and the app icon -> ${out}`);
