// The journal as the screen shows it: by day, newest first, every incident
// as one item however many lines it wrote. Pure functions, like view.ts.

import { dayKey, dayTitle, duration } from './format';
import { ru } from './i18n/ru';
import type { JournalCat, JournalEntry, JournalFilter, Severity } from './proto';

const t = ru.journal;

export type FilterName = 'all' | 'problems' | 'servers' | 'system';

/** The four filters of the journal; "servers" and "system" split all. */
export const FILTERS: Record<FilterName, JournalFilter> = {
  all: {},
  problems: { levels: ['problem', 'critical'] },
  servers: { cats: ['server', 'subs'] },
  system: { cats: ['vpn', 'system', 'guard', 'rules'] },
};

/** The same test the daemon applies, for entries that arrive live. */
export function fits(e: JournalEntry, f: JournalFilter): boolean {
  return (!f.cats?.length || f.cats.includes(e.cat as JournalCat)) && (!f.levels?.length || f.levels.includes(e.level));
}

export type Item =
  | { kind: 'entry'; key: string; entry: JournalEntry }
  | { kind: 'incident'; key: string; id: string; entries: JournalEntry[] };

export interface Day {
  key: string;
  title: string;
  items: Item[];
}

/**
 * Entries (newest first) into days of items. An incident stands where its
 * newest line is: that is where the eye looks for what just happened.
 */
export function feed(entries: JournalEntry[], now: number): Day[] {
  const items: Item[] = [];
  const groups = new Map<string, Extract<Item, { kind: 'incident' }>>();
  for (const e of entries) {
    if (e.incident) {
      const known = groups.get(e.incident);
      if (known) {
        known.entries.push(e);
        continue;
      }
      const group = { kind: 'incident' as const, key: `i:${e.incident}`, id: e.incident, entries: [e] };
      groups.set(e.incident, group);
      items.push(group);
    } else {
      items.push({ kind: 'entry', key: `e:${e.id}`, entry: e });
    }
  }
  const days: Day[] = [];
  for (const item of items) {
    const t0 = item.kind === 'entry' ? item.entry.t : item.entries[0]!.t;
    const key = dayKey(t0);
    let day = days[days.length - 1];
    if (!day || day.key !== key) days.push((day = { key, title: dayTitle(t0, now), items: [] }));
    day.items.push(item);
  }
  return days;
}

export interface IncidentView {
  /** The line that opened it. */
  start: JournalEntry;
  level: Severity;
  ended: boolean;
  /** "не работало 3 мин", "продолжается, 40 с". */
  meta: string;
}

const rank: Record<Severity, number> = { info: 0, problem: 1, critical: 2 };

/**
 * An incident's headline. entries are newest first; an incident is over
 * when its newest line is the calm one the daemon writes at the end, and
 * it is not among the open ones of State.
 */
export function incidentView(entries: JournalEntry[], open: boolean, now: number): IncidentView {
  const newest = entries[0]!;
  const start = [...entries].reverse().find((e) => e.level !== 'info') ?? entries[entries.length - 1]!;
  const level = entries.reduce<Severity>((worst, e) => (rank[e.level] > rank[worst] ? e.level : worst), 'info');
  const ended = !open && newest.level === 'info' && newest !== start;
  const lasted = duration((ended ? Date.parse(newest.t) : now) - Date.parse(start.t));
  const meta = ended ? (start.cat === 'subs' ? t.lastedSoft(lasted) : t.lasted(lasted)) : t.ongoing(lasted);
  return { start, level, ended, meta };
}

/**
 * Whether the loaded lines are enough to tell an incident's story: its
 * opening line and, unless it is still open, its end. A page boundary or
 * the "problems" filter can cut either off.
 */
export function complete(entries: JournalEntry[], open: boolean): boolean {
  const hasStart = entries.some((e) => e.level !== 'info');
  const hasEnd = entries[0]?.level === 'info';
  return hasStart && (hasEnd || open);
}
