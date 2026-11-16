// What the Rules screen shows: rule lists as groups of plates, and the live
// connections gathered by program. Pure functions, like view.ts.

import { ru } from './i18n/ru';
import type { Connection, RuleKind, RuleList, RuleListName } from './proto';

const t = ru.rules;

export interface RuleRef {
  kind: RuleKind;
  value: string;
}

export interface RuleGroup {
  key: 'processes' | 'sites' | 'keywords' | 'ips';
  title: string;
  rules: RuleRef[];
}

/** A list split the way people think of it: programs, sites, words, addresses. */
export function ruleGroups(list: RuleList, query: string): RuleGroup[] {
  const q = query.trim().toLowerCase();
  const pick = (kind: RuleKind, values: string[]) =>
    values.filter((v) => !q || v.toLowerCase().includes(q)).map((value) => ({ kind, value }));
  const groups: RuleGroup[] = [
    { key: 'processes', title: t.groups.processes, rules: pick('process', list.processes) },
    {
      key: 'sites',
      title: t.groups.sites,
      rules: [...pick('suffix', list.suffixes), ...pick('domain', list.domains)].sort((a, b) => a.value.localeCompare(b.value)),
    },
    { key: 'keywords', title: t.groups.keywords, rules: pick('keyword', list.keywords) },
    { key: 'ips', title: t.groups.ips, rules: pick('ip', list.ips) },
  ];
  return groups.filter((g) => g.rules.length > 0);
}

export function ruleCount(list: RuleList): number {
  return list.processes.length + list.domains.length + list.suffixes.length + list.keywords.length + list.ips.length;
}

export function listName(list: RuleListName): string {
  return list === 'vpn' ? t.vpn : t.direct;
}

export type Route = 'vpn' | 'direct' | 'mixed';

export interface HostLine {
  key: string;
  /** What to show: the site's name, or the address when there is none. */
  label: string;
  /** What a rule for it would be: the site with its subdomains, or the IP. */
  rule: RuleRef | null;
  conns: number;
  bytes: number;
  route: Route;
}

export interface AppLine {
  key: string;
  name: string;
  path: string;
  conns: number;
  /** Bytes per second, from two lists in a row; null until there are two. */
  rate: number | null;
  route: Route;
  hosts: HostLine[];
}

function routeOf(list: Connection[]): Route {
  const vpn = list.filter((c) => c.route === 'vpn').length;
  if (vpn === 0) return 'direct';
  return vpn === list.length ? 'vpn' : 'mixed';
}

/** "cdn.steamstatic.com" → "steamstatic.com", "news.bbc.co.uk" → "bbc.co.uk". */
export function baseDomain(host: string): string | null {
  if (!host || /^[\d.]+$/.test(host) || host.includes(':')) return null;
  const parts = host.toLowerCase().split('.');
  if (parts.length <= 2) return parts.join('.');
  const second = parts[parts.length - 2]!;
  const short = parts[parts.length - 1]!.length === 2 && ['com', 'co', 'net', 'org', 'gov', 'edu', 'ac'].includes(second);
  return parts.slice(short ? -3 : -2).join('.');
}

/**
 * Connections gathered by program, programs by name, so that rows stay
 * where they are while numbers change under them. rates holds bytes per
 * second per connection id, from the previous list.
 */
export function appLines(list: Connection[], rates: Map<string, number> | null, query: string): AppLine[] {
  const q = query.trim().toLowerCase();
  const byApp = new Map<string, Connection[]>();
  for (const c of list) {
    const key = c.process || '';
    let bucket = byApp.get(key);
    if (!bucket) byApp.set(key, (bucket = []));
    bucket.push(c);
  }
  const out: AppLine[] = [];
  for (const [name, conns] of byApp) {
    const appHit = !q || name.toLowerCase().includes(q);
    const hosts = new Map<string, Connection[]>();
    for (const c of conns) {
      const key = c.host || c.dest.replace(/:\d+$/, '');
      if (!appHit && !key.toLowerCase().includes(q)) continue;
      let bucket = hosts.get(key);
      if (!bucket) hosts.set(key, (bucket = []));
      bucket.push(c);
    }
    if (hosts.size === 0) continue;
    const lines: HostLine[] = [...hosts].map(([key, cs]) => {
      const site = baseDomain(cs[0]!.host);
      const ip = cs[0]!.dest.replace(/:\d+$/, '').replace(/^\[|\]$/g, '');
      return {
        key,
        label: key,
        rule: site ? { kind: 'suffix', value: site } : ip ? { kind: 'ip', value: ip } : null,
        conns: cs.length,
        bytes: cs.reduce((n, c) => n + c.up + c.down, 0),
        route: routeOf(cs),
      };
    });
    lines.sort((a, b) => a.label.localeCompare(b.label));
    out.push({
      key: name,
      name,
      path: '',
      conns: conns.length,
      rate: rates ? conns.reduce((n, c) => n + (rates.get(c.id) ?? 0), 0) : null,
      route: routeOf(conns),
      hosts: lines,
    });
  }
  // Named programs by name; connections without one at the end.
  return out.sort((a, b) => (a.name === '') !== (b.name === '') ? (a.name === '' ? 1 : -1) : a.name.localeCompare(b.name, 'ru'));
}

/** Whose route is set by the VPN itself and must not get a button. */
export function pinned(process: string): boolean {
  const p = process.toLowerCase();
  return p === 'sing-box.exe' || p === 'xray.exe' || p === 'sing-box' || p === 'xray';
}
