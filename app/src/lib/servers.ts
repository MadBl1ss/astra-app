// What the Servers screen shows for a State: groups, rows, the line under a
// subscription's name. Pure functions, like view.ts.

import { ago, bytes, dayMonth } from './format';
import { ru } from './i18n/ru';
import type { Server, State, Sub } from './proto';
import { countryOf, displayName, pingTone } from './view';

const t = ru.servers;

export interface Group {
  key: string;
  kind: 'favorites' | 'sub' | 'links';
  sub: Sub | null;
  servers: Server[];
  /** How many of the subscription's servers are in the favourites group. */
  starred: number;
}

/** Does a server answer the search: by name, country code or address. */
export function matches(server: Server, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    displayName(server).toLowerCase().includes(q) ||
    server.host.toLowerCase().includes(q) ||
    (countryOf(server) ?? '').toLowerCase() === q ||
    server.id.toLowerCase().includes(q)
  );
}

const byName = (a: Server, b: Server) => displayName(a).localeCompare(displayName(b), 'ru', { numeric: true });

/**
 * Favourites first, in their order (the order of fallback servers), then
 * each subscription with the rest of its servers, then servers added by
 * link. A favourite is listed once, in the favourites: its subscription's
 * header says how many of its servers went there.
 */
export function groupServers(s: State, favorites: Server[], query: string): Group[] {
  const groups: Group[] = [];
  const shown = (x: Server) => matches(x, query);
  const starred = new Set(favorites.map((x) => x.id));
  const fav = favorites.filter(shown);
  if (fav.length > 0 || (!query && favorites.length > 0)) {
    groups.push({ key: 'favorites', kind: 'favorites', sub: null, servers: fav, starred: 0 });
  }
  for (const sub of s.subs) {
    const own = s.servers.filter((x) => x.sub === sub.id);
    const rest = own.filter((x) => !starred.has(x.id) && shown(x)).sort(byName);
    // A subscription stays on screen while it is not searched through: it
    // may have no servers at all and still need its menu.
    if (query && rest.length === 0) continue;
    groups.push({ key: sub.id, kind: 'sub', sub, servers: rest, starred: own.length - own.filter((x) => !starred.has(x.id)).length });
  }
  const links = s.servers.filter((x) => x.sub === '' && !starred.has(x.id) && shown(x)).sort(byName);
  if (links.length > 0) groups.push({ key: 'links', kind: 'links', sub: null, servers: links, starred: 0 });
  return groups;
}

/** Favourites in their order. */
export function favoritesOf(s: State): Server[] {
  return s.servers.filter((x) => x.favorite).sort((a, b) => a.order - b.order);
}

export type Tone = 'none' | 'warn' | 'bad';

/** What the header of a subscription shows, piece by piece. */
export interface SubFacts {
  /** "12 серверов"; "2 в избранном" when some went to the favourites. */
  count: string;
  starred: string | null;
  /** Traffic left: "122 ГБ" of "из 150 ГБ", and the share left for the bar. */
  traffic: { value: string; of: string; share: number; tone: Tone; title: string } | null;
  /** The term: "до 25 мая 2027" and how far that is. */
  term: { value: string; rest: string | null; tone: Tone } | null;
  updated: string;
}

const DAY = 86_400_000;

/** How far a date is, in the unit a person would say. */
function rest(ms: number): string {
  const days = Math.floor(ms / DAY);
  if (days < 1) return t.lessThanDay;
  if (days < 100) return t.daysLeft(days);
  return t.monthsLeft(Math.round(days / 30.4));
}

/** Size, traffic, term and freshness of a subscription. */
export function subFacts(sub: Sub, starred: number, now: number): SubFacts {
  let traffic: SubFacts['traffic'] = null;
  if (sub.totalBytes !== null && sub.totalBytes > 0 && sub.usedBytes !== null) {
    const left = Math.max(0, sub.totalBytes - sub.usedBytes);
    const share = left / sub.totalBytes;
    traffic =
      left === 0
        ? { value: t.trafficOut, of: '', share: 0, tone: 'bad', title: t.trafficOutTitle(bytes(sub.totalBytes)) }
        : {
            value: bytes(left),
            of: t.of(bytes(sub.totalBytes)),
            share,
            tone: share < 0.1 ? 'warn' : 'none',
            title: t.trafficTitle(bytes(left), bytes(sub.totalBytes), bytes(sub.usedBytes)),
          };
  }
  let term: SubFacts['term'] = null;
  if (sub.expireAt) {
    const at = Date.parse(sub.expireAt);
    term =
      at <= now
        ? { value: t.expired(dayMonth(sub.expireAt, now)), rest: null, tone: 'bad' }
        : { value: t.until(dayMonth(sub.expireAt, now)), rest: rest(at - now), tone: at - now < 3 * DAY ? 'warn' : 'none' };
  }
  return {
    count: t.count(sub.servers),
    starred: starred > 0 ? t.inFavorites(starred) : null,
    traffic,
    term,
    updated: sub.updatedAt ? t.updated(ago(sub.updatedAt, now)) : t.neverUpdated,
  };
}

export interface PingView {
  text: string;
  tone: 'ok' | 'warn' | 'bad' | 'none';
  title: string;
}

/**
 * A server's ping: the colour of its range, a dash when there is no
 * answer, and in the tooltip how old the number is. The selected server is
 * probed all the time, so its number is the fresher one while the VPN works.
 */
export function pingOf(server: Server, s: State, now: number): PingView {
  const chosen = server.id === s.server.selected && s.vpn.phase === 'on';
  if (chosen && (s.health.verdict === 'server_down' || s.health.verdict === 'all_servers_down')) {
    return { text: t.noAnswer, tone: 'warn', title: t.noAnswer };
  }
  // Whichever answer is fresher: the constant probe of the selected server
  // or the last test of all servers.
  const probe = chosen && s.server.ping !== null && s.server.pingAt !== null && (!server.pingAt || Date.parse(s.server.pingAt) > Date.parse(server.pingAt));
  const ping = probe ? s.server.ping : server.ping;
  const at = probe ? s.server.pingAt : server.pingAt;
  if (!at) return { text: '—', tone: 'none', title: t.pingNever };
  if (ping === null || (!chosen && !server.alive)) return { text: '—', tone: 'none', title: t.pingNone(ago(at, now)) };
  return { text: ru.units.ms(ping), tone: pingTone(ping), title: t.pingAt(ping, ago(at, now)) };
}

/** Moves one id within a list; used by dragging and by Alt+arrows. */
export function moved(ids: string[], from: number, to: number): string[] {
  const out = [...ids];
  const [id] = out.splice(from, 1);
  out.splice(Math.max(0, Math.min(to, out.length)), 0, id!);
  return out;
}

export function protocolLabel(server: Server): string {
  return { vless: 'VLESS', trojan: 'Trojan', shadowsocks: 'SS', hysteria2: 'Hy2' }[server.protocol];
}
