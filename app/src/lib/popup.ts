// Popups (a menu, an explainer) are shown in the top layer with the popover
// attribute. A group of the Servers screen is painted with containment
// (content-visibility), which clips anything absolutely positioned inside it:
// the menu of an empty subscription was cut to the height of its header.
// The top layer is above every such box and every scrolling list.

const EDGE = 8;

/**
 * Shows a [popover] element and puts it next to the element that opened it:
 * below, or above when there is no room below, aligned by the opener's left
 * or right edge, never past the window's edge. Returns true when it went
 * above, for the direction of its entrance. Without popover support the
 * element stays where its own CSS puts it.
 */
export function showBeside(popup: HTMLElement, opener: HTMLElement, align: 'start' | 'end', gap = 4): boolean {
  if (typeof popup.showPopover !== 'function') return false;
  if (!popup.matches(':popover-open')) popup.showPopover();
  const at = opener.getBoundingClientRect();
  const { width, height } = popup.getBoundingClientRect();
  const below = at.bottom + gap;
  const room = innerHeight - EDGE;
  const up = below + height > room && at.top - gap - height >= EDGE;
  const top = up ? at.top - gap - height : Math.max(EDGE, Math.min(below, room - height));
  const x = align === 'end' ? at.right - width : at.left;
  popup.style.top = `${top}px`;
  popup.style.left = `${Math.max(EDGE, Math.min(x, innerWidth - EDGE - width))}px`;
  return up;
}

/**
 * Closes a popup on a click outside it and its opener, on any scroll and on
 * a resize: a popup in the top layer does not move with the list it came
 * from. Returns the cleanup for an $effect.
 */
export function dismissOn(inside: () => (Node | undefined)[], close: () => void): () => void {
  const away = (event: Event) => {
    if (!inside().some((node) => node?.contains(event.target as Node))) close();
  };
  document.addEventListener('pointerdown', away, true);
  document.addEventListener('scroll', away, true);
  window.addEventListener('resize', close);
  return () => {
    document.removeEventListener('pointerdown', away, true);
    document.removeEventListener('scroll', away, true);
    window.removeEventListener('resize', close);
  };
}
