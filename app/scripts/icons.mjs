// Draws the tray icons and the application icon and writes them as PNG and
// ICO into src-tauri/icons/. The drawings are SVG built here, one template
// for all states, so the six tray icons cannot drift apart. Run by
// `pnpm icons` and before every `tauri build`; the output is committed,
// because the Rust side embeds it at compile time.

import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Resvg } from '@resvg/resvg-js';

const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'src-tauri', 'icons');

// Colours of the dark theme: a tray icon sits on the
// taskbar, not in the window, and has to read on both light and dark ones.
const C = { vpn: '#3ddc97', off: '#8b94a3', warn: '#f5b84b', bad: '#f2555a', ink: '#0f1216' };

const SHIELD = 'M16 1.5 28.5 7v8.5c0 7.2-5 12.4-12.500 15-7.500-2.600-12.500-7.800-12.500-15V7z';

// What is drawn inside the shield. The state is never told by colour alone.
const glyphs = {
  // The arrow: the points of a railway switch, and the app's name.
  arrow: `<path d="M9.500 18.500 16 12l6.500 6.500" fill="none" stroke="${C.ink}" stroke-width="3.400" stroke-linecap="round" stroke-linejoin="round"/>`,
  bang: `<path d="M16 8.500v8.500" fill="none" stroke="${C.ink}" stroke-width="3.600" stroke-linecap="round"/><circle cx="16" cy="22.500" r="2.100" fill="${C.ink}"/>`,
  lock: `<rect x="9.500" y="14.500" width="13" height="9.500" rx="2" fill="${C.ink}"/><path d="M12.200 14.500v-2.300a3.800 3.800 0 017.600 0v2.300" fill="none" stroke="${C.ink}" stroke-width="2.600"/>`,
};

const tray = {
  on: { fill: C.vpn, glyph: 'arrow' },
  off: { fill: C.off, glyph: 'arrow' },
  connecting: { fill: C.warn, glyph: 'arrow' },
  // The second frame of the pulse.
  'connecting-dim': { fill: C.warn, glyph: 'arrow', opacity: 0.45 },
  problem: { fill: C.warn, glyph: 'bang' },
  down: { fill: C.bad, glyph: 'arrow' },
  blocked: { fill: C.bad, glyph: 'lock' },
};

function traySvg({ fill, glyph, opacity = 1 }) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><g opacity="${opacity}"><path d="${SHIELD}" fill="${fill}"/>${glyphs[glyph]}</g></svg>`;
}

// The application icon: the same shield on a dark rounded plate.
const appSvg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect width="48" height="48" rx="10" fill="#161a20"/><g transform="translate(8 8)"><path d="${SHIELD}" fill="${C.vpn}"/>${glyphs.arrow}</g></svg>`;

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
