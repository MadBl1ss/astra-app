// What the toasts on screen are, as pure functions of what came from the
// daemon, what the user did and the time: like view.ts, nothing here draws
// or waits. The main page keeps the state (store of toasts.svelte.ts) and
// the toast window only draws what it is sent.
//
// Only type imports, so that `node --test` runs the tests of this file
// without a build step.

import type { Severity, Toast } from './proto';

/** How long an ordinary toast stays; critical ones stay until acted on. */
export const SHOW_MS = 6000;
/** Cards on screen at once; the rest fold into "ещё N". */
export const MAX_CARDS = 3;
/** Buttons on one card. */
export const MAX_ACTIONS = 2;

export interface Card {
  id: string;
  severity: Severity;
  title: string;
  body: string;
  actions: { id: string; label: string }[];
  /** Does not hide by itself. */
  sticky: boolean;
  /** When it came or was last replaced: its time counts from here. */
  at: number;
  /** Grows with every replacement, so the card can show its text changed. */
  rev: number;
}

export interface Toasts {
  /** On screen, in the order they came. */
  shown: Card[];
  /** Held back while a full-screen program is in front, in the order they came. */
  waiting: Card[];
  /** Set while the pointer is over the toasts: no time runs out meanwhile. */
  pausedAt: number | null;
}

export const EMPTY: Toasts = { shown: [], waiting: [], pausedAt: null };

/** What the toast window draws. */
export interface ToastView {
  /** At most MAX_CARDS, top to bottom: the newest is nearest the corner. */
  cards: Card[];
  /** How many more there are behind them. */
  more: number;
}

function card(t: Toast, now: number, rev: number): Card {
  return {
    id: t.id,
    severity: t.severity,
    title: t.title,
    body: t.body,
    actions: t.actions.slice(0, MAX_ACTIONS),
    sticky: t.sticky || t.severity === 'critical',
    at: now,
    rev,
  };
}

/**
 * A toast from the daemon. The same id replaces the card in its place, on
 * screen or in the queue; `close` takes it away. While the screen is busy
 * with a full-screen program, a new card that is not critical waits.
 */
export function receive(s: Toasts, t: Toast, now: number, busy: boolean): Toasts {
  if (t.close) return dismiss(s, t.id);
  const onScreen = s.shown.findIndex((c) => c.id === t.id);
  if (onScreen >= 0) {
    const shown = s.shown.slice();
    shown[onScreen] = card(t, now, shown[onScreen]!.rev + 1);
    return { ...s, shown };
  }
  const queued = s.waiting.findIndex((c) => c.id === t.id);
  if (queued >= 0) {
    const waiting = s.waiting.slice();
    waiting[queued] = card(t, now, waiting[queued]!.rev + 1);
    return { ...s, waiting };
  }
  if (busy && t.severity !== 'critical') {
    return { ...s, waiting: [...s.waiting, card(t, now, 0)] };
  }
  return { ...s, shown: [...s.shown, card(t, now, 0)] };
}

/** Takes a card away, from the screen or from the queue. */
export function dismiss(s: Toasts, id: string): Toasts {
  const shown = s.shown.filter((c) => c.id !== id);
  const waiting = s.waiting.filter((c) => c.id !== id);
  if (shown.length === s.shown.length && waiting.length === s.waiting.length) return s;
  return { ...s, shown, waiting };
}

/**
 * The full-screen program is gone: only the newest of the waiting cards is
 * shown, the rest are in the journal. Its time starts now.
 */
export function release(s: Toasts, now: number): Toasts {
  const last = s.waiting[s.waiting.length - 1];
  if (!last) return s;
  return { ...s, shown: [...s.shown, { ...last, at: now }], waiting: [] };
}

/** The pointer came over the toasts or left them. */
export function hover(s: Toasts, on: boolean, now: number): Toasts {
  if (on) return s.pausedAt === null ? { ...s, pausedAt: now } : s;
  if (s.pausedAt === null) return s;
  // The time under the pointer does not count.
  const paused = now - s.pausedAt;
  return { ...s, pausedAt: null, shown: s.shown.map((c) => ({ ...c, at: c.at + paused })) };
}

/** Takes away the cards whose time is up. */
export function expire(s: Toasts, now: number): Toasts {
  if (s.pausedAt !== null) return s;
  const shown = s.shown.filter((c) => c.sticky || now - c.at < SHOW_MS);
  return shown.length === s.shown.length ? s : { ...s, shown };
}

/** In how many milliseconds the next card's time is up; null if none will be. */
export function nextExpiry(s: Toasts, now: number): number | null {
  if (s.pausedAt !== null) return null;
  let soonest: number | null = null;
  for (const c of s.shown) {
    if (c.sticky) continue;
    const left = Math.max(0, c.at + SHOW_MS - now);
    soonest = soonest === null ? left : Math.min(soonest, left);
  }
  return soonest;
}

/**
 * The cards to draw. Critical ones go first when there is no room for all,
 * then the newest; on screen they keep the order they came in.
 */
export function view(s: Toasts): ToastView {
  const rank = s.shown
    .map((c, i) => ({ c, i }))
    .sort((a, b) => Number(b.c.severity === 'critical') - Number(a.c.severity === 'critical') || b.i - a.i)
    .slice(0, MAX_CARDS)
    .sort((a, b) => a.i - b.i);
  return { cards: rank.map((x) => x.c), more: s.shown.length - rank.length };
}
