// Bakes the continents of the Home globe into src/lib/globe/land.ts.
//
// The globe is a lattice of dots: rows of equal latitude, each with as many
// dots as its circle is long. This script asks Natural Earth (the 1:110m land
// of the world-atlas package) which dots are on land and writes the answers
// as one bit per dot. The window then needs neither the map nor d3 at run
// time, and loads nothing from the network. Run by `pnpm globe`; the output
// is committed.

import { readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { geoContains } from 'd3-geo';
import { feature } from 'topojson-client';

// Must match the lattice of src/lib/globe/lattice.ts.
const ROWS = 100;

const require = createRequire(import.meta.url);
const world = JSON.parse(readFileSync(require.resolve('world-atlas/land-110m.json'), 'utf8'));
const land = feature(world, world.objects.land);

const bits = [];
for (let i = 0; i < ROWS; i++) {
  const lat = 90 - ((i + 0.5) * 180) / ROWS;
  const count = Math.max(1, Math.round(2 * ROWS * Math.cos((lat * Math.PI) / 180)));
  for (let j = 0; j < count; j++) {
    const lon = -180 + ((j + 0.5) * 360) / count;
    bits.push(geoContains(land, [lon, lat]) ? 1 : 0);
  }
}

const bytes = new Uint8Array(Math.ceil(bits.length / 8));
bits.forEach((bit, i) => {
  if (bit) bytes[i >> 3] |= 1 << (i & 7);
});

const out = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'lib', 'globe', 'land.ts');
writeFileSync(
  out,
  `// Written by scripts/globe.mjs (\`pnpm globe\`) from Natural Earth 1:110m land.
// One bit per dot of the lattice in lattice.ts, row by row from the north
// pole, least significant bit first; base64.
export const LAND_ROWS = ${ROWS};
export const LAND_DOTS = ${bits.length};
export const LAND =
  '${Buffer.from(bytes).toString('base64')}';
`,
);

const on = bits.reduce((sum, bit) => sum + bit, 0);
console.log(`${bits.length} dots, ${on} on land (${Math.round((100 * on) / bits.length)}%) -> ${out}`);
