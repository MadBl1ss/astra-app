// What the window makes of the daemon's rating: tiers, the best servers, the
// fast ones, and the scale of the speedometer. Pure functions with no
// imports but types, so `pnpm test` (node --test) checks them as they are.

import type { Server, ServerStats, SpeedResult } from './proto';

/** How a server's score reads. "new" is a score with little evidence behind it. */
export type Tier = 'top' | 'good' | 'fair' | 'poor' | 'new' | 'none';

/** Below this much trust the score is mostly the prior: "мало данных". */
export const TRUST_FLOOR = 0.3;

export function tierOf(stats: ServerStats | null): Tier {
  if (!stats) return 'none';
  if (stats.avg === 0 && stats.loss >= 0.99) return 'poor'; // never answered
  if (stats.trust < TRUST_FLOOR) return 'new';
  if (stats.score >= 78) return 'top';
  if (stats.score >= 62) return 'good';
  if (stats.score >= 45) return 'fair';
  return 'poor';
}

/** The colour of a tier: colours mean state, so only three of them. */
export function toneOf(tier: Tier): 'ok' | 'warn' | 'bad' | 'none' {
  switch (tier) {
    case 'top':
    case 'good':
      return 'ok';
    case 'fair':
      return 'warn';
    case 'poor':
      return 'bad';
    default:
      return 'none';
  }
}

/** Steady and good: what the user asked to see at a glance. */
export function isReliable(stats: ServerStats | null): boolean {
  return stats !== null && stats.stable && stats.score >= 62;
}

/**
 * The best servers, best first: a score with enough evidence behind it, a
 * server that answers. Ties go to the faster download, then the shorter
 * ping, then the name, so that the order does not jump between renders.
 */
export function bestOf(servers: readonly Server[], n: number): Server[] {
  return servers
    .filter((x) => x.stats !== null && x.stats.trust >= 0.4 && x.stats.avg > 0 && x.stats.loss < 0.5)
    .sort((a, b) => {
      const sa = a.stats!;
      const sb = b.stats!;
      return sb.score - sa.score || (sb.speed ?? 0) - (sa.speed ?? 0) || sa.avg - sb.avg || a.id.localeCompare(b.id);
    })
    .slice(0, n);
}

/**
 * The servers that download fast: within 85% of the best speed measured,
 * and fast in itself. Speeds are bounded by the user's own line, so "fast"
 * means "as fast as anything here", not a number from an advert.
 */
export function fastIds(servers: readonly Server[]): Set<string> {
  const speeds = servers.filter((x) => x.stats?.speed != null).map((x) => [x.id, x.stats!.speed!] as const);
  const top = Math.max(0, ...speeds.map(([, v]) => v));
  const floor = speeds.length > 1 ? Math.max(10, top * 0.85) : 30;
  return new Set(speeds.filter(([, v]) => v >= floor).map(([id]) => id));
}

/** The result with the fastest download, if any server downloaded at all. */
export function winner(results: readonly SpeedResult[]): SpeedResult | null {
  let best: SpeedResult | null = null;
  for (const r of results) {
    if (r.mbps !== null && (best === null || r.mbps > best.mbps!)) best = r;
  }
  return best;
}

// ---- the speedometer -------------------------------------------------------

/** The marks of the dial, Mbit/s. Speeds differ by factors, so the scale does. */
export const TICKS = [0, 5, 10, 25, 50, 100, 250, 500, 1000] as const;

/** Where a speed sits on the dial, 0..1, evenly between the marks. */
export function dialAt(mbps: number): number {
  const v = Math.max(0, mbps);
  const last = TICKS.length - 1;
  if (v >= TICKS[last]!) return 1;
  for (let i = 1; i <= last; i++) {
    const hi = TICKS[i]!;
    if (v <= hi) {
      const lo = TICKS[i - 1]!;
      return (i - 1 + (v - lo) / (hi - lo)) / last;
    }
  }
  return 1;
}

/** "84,3", "118", "9,6": a speed for reading, Russian decimal comma. */
export function mbpsText(mbps: number, decimal = ','): string {
  const v = Math.max(0, mbps);
  const text = v >= 100 ? String(Math.round(v)) : v.toFixed(1);
  return text.replace('.', decimal);
}

/**
 * One step of a needle that follows a target: it closes a share of the gap
 * that depends on the time passed, so it moves the same at 30 and 144 Hz.
 */
export function follow(current: number, target: number, dtMs: number, halfLifeMs = 120): number {
  if (dtMs <= 0) return current;
  const keep = Math.pow(0.5, dtMs / halfLifeMs);
  const next = target + (current - target) * keep;
  return Math.abs(next - target) < 1e-3 ? target : next;
}
