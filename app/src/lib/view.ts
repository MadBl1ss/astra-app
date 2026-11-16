// What the window shows for a given State. Pure functions: Home, the tray and
// (later) the toasts all read the same picture, and it can be checked without
// a DOM.

import { duration, since } from './format';
import { ru } from './i18n/ru';
import type { Incident, IncidentAction, Server, State } from './proto';

const t = ru;

/** The colour family of a state; a word or an icon always goes with it. */
export type Tone = 'ok' | 'off' | 'busy' | 'warn' | 'bad';

/** Names of the tray icons, drawn by app/scripts/icons.mjs. */
export type TrayIcon = 'on' | 'off' | 'connecting' | 'problem' | 'down' | 'blocked';

/** How the two paths of the scheme are drawn. */
export type RailLook = 'flow' | 'idle' | 'waiting' | 'down' | 'closed';

export interface Picture {
  tone: Tone;
  word: string;
  sub: string;
  tray: TrayIcon;
  /** The switch shows what the user asked for, not what the engine does. */
  wantOn: boolean;
  vpnRail: RailLook;
  directRail: RailLook;
  /** Both paths are crossed out at the start: the lock holds the internet. */
  gate: boolean;
}

export function selectedServer(s: State): Server | undefined {
  return s.servers.find((x) => x.id === s.server.selected);
}

export function describe(s: State, now: number): Picture {
  const server = selectedServer(s);
  const name = server ? displayName(server) : t.state.noServer;
  const wantOn = s.vpn.desired === 'on';
  const base = { wantOn, gate: false } as const;

  if (s.guard.blocking) {
    return {
      ...base,
      tone: 'bad',
      word: t.state.blocked,
      sub: s.vpn.failText ? t.state.blockedSub(lowerFirst(trimDot(s.vpn.failText))) : t.state.blockedSubPlain,
      tray: 'blocked',
      vpnRail: 'closed',
      directRail: 'closed',
      gate: true,
    };
  }

  switch (s.vpn.phase) {
    case 'off':
      return {
        ...base,
        tone: 'off',
        word: t.state.off,
        sub: s.servers.length === 0 ? t.state.offEmptySub : t.state.offSub,
        tray: 'off',
        vpnRail: 'idle',
        directRail: 'flow',
      };
    case 'starting':
      return { ...base, tone: 'busy', word: t.state.starting, sub: name, tray: 'connecting', vpnRail: 'waiting', directRail: 'flow' };
    case 'stopping':
      return { ...base, tone: 'busy', word: t.state.stopping, sub: t.state.stoppingSub, tray: 'connecting', vpnRail: 'idle', directRail: 'flow' };
    case 'recovering':
      return { ...base, tone: 'busy', word: t.state.recovering, sub: t.state.recoveringSub, tray: 'connecting', vpnRail: 'waiting', directRail: 'flow' };
    case 'failed':
      return {
        ...base,
        tone: 'bad',
        word: t.state.failed,
        sub: s.vpn.failText ?? t.state.failedSub,
        tray: 'down',
        vpnRail: 'down',
        directRail: 'flow',
      };
    case 'on':
      break;
  }

  switch (s.health.verdict) {
    case 'ok': {
      const parts = [name];
      if (s.server.ping !== null) parts.push(t.units.ms(s.server.ping));
      if (s.vpn.since) parts.push(t.state.uptime(duration(since(s.vpn.since, now))));
      return { ...base, tone: 'ok', word: t.state.ok, sub: parts.join(' · '), tray: 'on', vpnRail: 'flow', directRail: 'flow' };
    }
    case 'settling':
      return { ...base, tone: 'busy', word: t.state.starting, sub: t.state.settlingSub(name), tray: 'connecting', vpnRail: 'waiting', directRail: 'flow' };
    case 'degraded':
      return { ...base, tone: 'warn', word: t.state.degraded, sub: s.health.hint ?? t.state.degradedSub(name), tray: 'problem', vpnRail: 'flow', directRail: 'flow' };
    case 'server_down':
      return {
        ...base,
        tone: 'warn',
        word: t.state.serverDown,
        sub: t.state.serverDownSub(name, duration(since(s.health.since, now))),
        tray: 'problem',
        vpnRail: 'down',
        directRail: 'flow',
      };
    case 'all_servers_down':
      return { ...base, tone: 'bad', word: t.state.allDown, sub: t.state.allDownSub, tray: 'down', vpnRail: 'down', directRail: 'flow' };
    case 'no_internet':
      return { ...base, tone: 'warn', word: t.state.noInternet, sub: t.state.noInternetSub, tray: 'problem', vpnRail: 'idle', directRail: 'waiting' };
    case 'no_network':
      return { ...base, tone: 'warn', word: t.state.noNetwork, sub: t.state.noNetworkSub, tray: 'problem', vpnRail: 'idle', directRail: 'waiting' };
    case 'engine_recovering':
      return { ...base, tone: 'busy', word: t.state.recovering, sub: t.state.recoveringSub, tray: 'connecting', vpnRail: 'waiting', directRail: 'flow' };
    case 'engine_failed':
      return { ...base, tone: 'bad', word: t.state.failed, sub: s.vpn.failText ?? t.state.failedSub, tray: 'down', vpnRail: 'down', directRail: 'flow' };
    case 'helper_down':
      return { ...base, tone: 'warn', word: t.state.helperDown, sub: s.health.hint ?? t.state.helperDownSub, tray: 'problem', vpnRail: 'down', directRail: 'flow' };
  }
}

export interface BannerAction {
  label: string;
  /** Sent to the daemon as incident.action; "journal" opens the journal. */
  action: IncidentAction | 'journal';
}

export interface BannerView {
  /** Incident id, or null for a strip the window made up itself. */
  id: string | null;
  tone: 'warn' | 'bad';
  title: string;
  body: string;
  actions: BannerAction[];
}

/**
 * The strip above the scheme: the most serious open incident and what can be
 * done about it. State carries no actions (those travel with toasts), so the
 * window derives them from the incident kind.
 */
export function banner(s: State): BannerView | null {
  const incident = worst(s.incidents);
  if (!incident) {
    if (!s.guard.blocking) return null;
    return {
      id: null,
      tone: 'bad',
      title: t.banner.blocked,
      body: '',
      actions: [
        { label: t.banner.retry, action: 'retry' },
        { label: t.banner.release, action: 'release' },
      ],
    };
  }

  const actions: BannerAction[] = [];
  switch (incident.kind) {
    case 'server_down': {
      const spare = spareServer(s);
      if (spare) actions.push({ label: t.banner.switchTo(displayName(spare)), action: `switch:${spare.id}` });
      actions.push({ label: t.banner.retry, action: 'retry' });
      break;
    }
    case 'all_servers_down':
      actions.push({ label: t.banner.retry, action: 'retry' }, { label: t.banner.updateSubs, action: 'update_subs' });
      break;
    case 'engine_failed':
      actions.push({ label: t.banner.retry, action: 'retry' });
      if (s.guard.blocking) actions.push({ label: t.banner.release, action: 'release' });
      break;
    case 'helper_down':
      actions.push({ label: t.banner.retry, action: 'retry' });
      break;
    case 'sub_expiring':
    case 'sub_expired':
    case 'sub_traffic':
      actions.push({ label: t.banner.updateSubs, action: 'update_subs' });
      break;
  }

  const body = [incident.body, incident.unstable ? t.banner.unstable : '', s.health.hint ?? '']
    .filter((part, i, all) => part && all.indexOf(part) === i)
    .join(' ');
  return {
    id: incident.id,
    tone: incident.severity === 'critical' ? 'bad' : 'warn',
    title: incident.title,
    body,
    actions: actions.slice(0, 2),
  };
}

function worst(incidents: Incident[]): Incident | undefined {
  const rank = { critical: 0, problem: 1, info: 2 } as const;
  return [...incidents].sort((a, b) => rank[a.severity] - rank[b.severity] || b.opened.localeCompare(a.opened))[0];
}

/**
 * The server to offer instead of a dead one: favourites in their order, then
 * whoever answered fastest. Only servers whose last probe succeeded.
 */
export function spareServer(s: State): Server | undefined {
  const alive = s.servers.filter((x) => x.alive && x.id !== s.server.selected);
  const favorites = alive.filter((x) => x.favorite).sort((a, b) => a.order - b.order);
  if (favorites[0]) return favorites[0];
  return alive.sort((a, b) => (a.ping ?? Infinity) - (b.ping ?? Infinity))[0];
}

/** Servers the way pickers list them: favourites first, then by name. */
export function sortedServers(s: State): Server[] {
  return [...s.servers].sort((a, b) => {
    if (a.favorite !== b.favorite) return a.favorite ? -1 : 1;
    if (a.favorite && a.order !== b.order) return a.order - b.order;
    return displayName(a).localeCompare(displayName(b), 'ru');
  });
}

// A flag emoji is two "regional indicator" letters. Subscriptions put flags
// into server names; the window shows the flag once, in front of the name,
// built from the country the daemon worked out.
const FLAG = /[\u{1F1E6}-\u{1F1FF}]{2}/u;
const FLAGS = /[\u{1F1E6}-\u{1F1FF}]{2}\s*/gu;

/** Any text without flag emoji: the daemon's lines quote server names. */
export function noFlags(text: string): string {
  return text.replace(FLAGS, '');
}

/** A server's name without the flag emoji subscriptions like to put in. */
export function displayName(server: Server): string {
  return noFlags(server.name).trim() || server.name;
}

/** The country code for the badge: from the daemon, else from the flag. */
export function countryOf(server: Server): string | null {
  if (server.country) return server.country.toUpperCase();
  const flag = FLAG.exec(server.name)?.[0];
  if (!flag) return null;
  return [...flag].map((ch) => String.fromCharCode((ch.codePointAt(0) ?? 0) - 0x1f1e6 + 65)).join('');
}

/** The flag emoji of a server's country, or null when it is not known. */
export function flagOf(server: Server): string | null {
  const code = countryOf(server);
  if (!code || !/^[A-Z]{2}$/.test(code)) return null;
  return [...code].map((ch) => String.fromCodePoint(0x1f1e6 + ch.charCodeAt(0) - 65)).join('');
}

/** Ping colour by its thresholds. */
export function pingTone(ping: number | null): 'ok' | 'warn' | 'bad' | 'none' {
  if (ping === null) return 'none';
  if (ping <= 150) return 'ok';
  if (ping <= 400) return 'warn';
  return 'bad';
}

export type GuardLook = 'on' | 'off' | 'idle' | 'blocking' | 'not_ready';

/**
 * What to say about the lock. "enabled" is only the setting; whether traffic
 * is really held is "engaged", and the window must not call an unarmed lock
 * "on".
 */
export function guardLook(s: State): GuardLook {
  if (!s.guard.enabled) return 'off';
  if (s.guard.blocking) return 'blocking';
  if (s.guard.engaged) return 'on';
  return s.vpn.phase === 'off' ? 'idle' : 'not_ready';
}

function trimDot(text: string): string {
  return text.replace(/\.\s*$/, '');
}

function lowerFirst(text: string): string {
  // "sing-box падает…" and "Не удалось…" both read well after a colon.
  const first = text.charAt(0);
  const second = text.charAt(1);
  // Leave abbreviations and names alone: lower only a plain capitalised word.
  if (first === first.toLowerCase() || second !== second.toLowerCase()) return text;
  return first.toLowerCase() + text.slice(1);
}
