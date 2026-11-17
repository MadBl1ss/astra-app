// The dots of the globe: rows of equal latitude, each with as many dots as
// its circle is long, so the spacing is the same everywhere on the sphere.
// scripts/globe.mjs walks the same lattice to decide which dots are land.

import { LAND, LAND_DOTS, LAND_ROWS } from './land';

/** A point on the unit sphere: x to the east, y to the north pole, z to the viewer at 0°E 0°N. */
export type Vec3 = [number, number, number];

export interface Lattice {
  count: number;
  /** x, y, z of every dot, one after another. */
  xyz: Float32Array;
  /** 1 where the dot is on land. */
  land: Uint8Array;
}

const RAD = Math.PI / 180;

export function unit(lat: number, lon: number): Vec3 {
  const phi = lat * RAD;
  const lambda = lon * RAD;
  return [Math.cos(phi) * Math.sin(lambda), Math.sin(phi), Math.cos(phi) * Math.cos(lambda)];
}

let cached: Lattice | null = null;

export function lattice(): Lattice {
  if (cached) return cached;
  const packed = Uint8Array.from(atob(LAND), (ch) => ch.charCodeAt(0));
  const xyz = new Float32Array(LAND_DOTS * 3);
  const land = new Uint8Array(LAND_DOTS);
  let k = 0;
  for (let i = 0; i < LAND_ROWS; i++) {
    const lat = 90 - ((i + 0.5) * 180) / LAND_ROWS;
    const count = Math.max(1, Math.round(2 * LAND_ROWS * Math.cos(lat * RAD)));
    for (let j = 0; j < count; j++, k++) {
      const [x, y, z] = unit(lat, -180 + ((j + 0.5) * 360) / count);
      xyz[k * 3] = x;
      xyz[k * 3 + 1] = y;
      xyz[k * 3 + 2] = z;
      land[k] = (packed[k >> 3]! >> (k & 7)) & 1;
    }
  }
  cached = { count: LAND_DOTS, xyz, land };
  return cached;
}
