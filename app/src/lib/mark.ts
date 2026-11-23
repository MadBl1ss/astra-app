// The mark of Astra: a rounded triangle, a swoosh cut across it and an arch
// cut under it. One source for the window (Mark.svelte) and the tray and app
// icons (scripts/icons.mjs), so they cannot drift apart.
//
// Drawn in a 612 × 410 box. The cuts run past the outline on purpose - the
// swoosh opens to the left, the arch to the bottom - so they are applied as a
// mask, not as holes of one path.

export const MARK_W = 612;
export const MARK_H = 410;

export const MARK_OUTLINE =
  'M297.6 11.2Q306 0 314.4 11.2L588.1 378Q612 410 572 410L40 410Q0 410 23.9 378Z';

export const MARK_CUTS = [
  // The swoosh: from the left edge up to the inner peak and down to the right.
  'M9 287L83 299C129 264 209 232 247 207C279 185 294 142 307 87C321 147 339 192 379 224' +
    'C419 254 479 274 509 297C529 314 541 332 552 350L507 350C469 350 444 337 414 310' +
    'C384 282 354 252 306 247C259 247 199 267 149 292C99 317 64 337 24 372Z',
  // The arch between the two feet.
  'M144 414C179 414 191 397 209 370L262 294C284 264 329 264 350 294L404 370' +
    'C422 397 434 414 469 414Z',
];

/**
 * The mark as SVG elements in its own 612 × 410 coordinates, filled with
 * `fill`. `id` names the mask and must be unique within one document.
 */
export function markElements(id: string, fill: string): string {
  const cuts = MARK_CUTS.map((d) => `<path d="${d}" fill="#000"/>`).join('');
  return (
    `<defs><mask id="${id}" maskUnits="userSpaceOnUse" x="-20" y="-20" width="660" height="460">` +
    `<path d="${MARK_OUTLINE}" fill="#fff"/>${cuts}</mask></defs>` +
    `<rect x="-20" y="-20" width="660" height="460" fill="${fill}" mask="url(#${id})"/>`
  );
}
