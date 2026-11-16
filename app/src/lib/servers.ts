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

export interface Part {
  text: string;
  tone: 'none' | 'warn' | 'bad';
}

/** The line under a subscription's name: size, traffic, term, freshness. */
export function subLine(sub: Sub, starred: number, now: number): Part[] {
  const parts: Part[] = [{ text: starred > 0 ? `${t.count(sub.servers)}, ${t.inFavorites(starred)}` : t.count(sub.servers), tone: 'none' }];
  if (sub.totalBytes !== null && sub.totalBytes > 0 && sub.usedBytes !== null) {
    const left = sub.totalBytes - sub.usedBytes;
    if (left <= 0) parts.push({ text: t.trafficOut, tone: 'bad' });
    else parts.push({ text: t.left(bytes(left), bytes(sub.totalBytes)), tone: left / sub.totalBytes < 0.1 ? 'warn' : 'none' });
  }
  if (sub.expireAt) {
    const at = Date.parse(sub.expireAt);
    if (at <= now) parts.push({ text: t.expired(dayMonth(sub.expireAt, now)), tone: 'bad' });
    else parts.push({ text: t.until(dayMonth(sub.expireAt, now)), tone: at - now < 3 * 86_400_000 ? 'warn' : 'none' });
  }
  parts.push({ text: sub.updatedAt ? t.updated(ago(sub.updatedAt, now)) : t.neverUpdated, tone: 'none' });
  return parts;
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
  const ping = chosen && s.server.ping !== null ? s.server.ping : server.ping;
  const at = chosen && s.server.pingAt ? s.server.pingAt : server.pingAt;
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
