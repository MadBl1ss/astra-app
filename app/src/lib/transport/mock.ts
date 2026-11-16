import {
  ErrorCode,
  PROTO,
  type Connection,
  type Incident,
  type JournalCat,
  type JournalEntry,
  type MethodName,
  type Methods,
  type NotificationName,
  type Notifications,
  type Server,
  type Settings,
  type Severity,
  type State,
  type Topic,
} from '../proto';
import { mock as text } from '../i18n/ru';
import { LinkError, Listeners, RpcError, type LinkStatus, type Transport, type Unsubscribe } from './types';

// The same files core/proto checks its Go types against.
const fixtures = import.meta.glob<State>('../../../../core/proto/testdata/fixtures/*.json', {
  eager: true,
  import: 'default',
});

/** Scenario names: every fixture plus a daemon that does not answer. */
export const OFFLINE = 'offline';

export function scenarios(): string[] {
  const names = Object.keys(fixtures).map((path) => path.replace(/^.*\/|\.json$/g, ''));
  return [...names.sort(), OFFLINE];
}

function fixture(name: string): State | undefined {
  const hit = Object.entries(fixtures).find(([path]) => path.endsWith(`/${name}.json`));
  return hit ? rebase(structuredClone(hit[1])) : undefined;
}

const ISO = /^\d{4}-\d\d-\d\dT[\d:.]+Z$/;

/**
 * Moves every timestamp of a fixture so that its story ends now: a VPN that
 * "has been on for 2 h 14 min" in the mockup is that old on screen as well,
 * not as old as the file.
 */
function rebase(state: State): State {
  const past = [state.vpn.since, state.health.since, state.server.pingAt, ...state.incidents.map((i) => i.opened)]
    .concat(state.servers.map((x) => x.pingAt))
    .filter((t): t is string => t !== null)
    .map((t) => Date.parse(t));
  if (past.length === 0) return state;
  const healthy = state.vpn.phase === 'on' && state.health.verdict === 'ok' && state.vpn.since !== null;
  const then = healthy ? Date.parse(state.vpn.since!) + 134 * 60_000 : Math.max(...past) + 40_000;
  const shift = Date.now() - then;
  const walk = (value: unknown): unknown => {
    if (typeof value === 'string') return ISO.test(value) ? new Date(Date.parse(value) + shift).toISOString() : value;
    if (Array.isArray(value)) return value.map(walk);
    if (value !== null && typeof value === 'object') {
      return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, walk(v)]));
    }
    return value;
  };
  return walk(state) as State;
}

// How fast the fake daemon does things. Slow enough to see every phase.
const START_MS = 900;
const STOP_MS = 300;
const SETTLE_MS = 700;
const CONFIRM_DOWN_MS = 1800;
const SUBS_MS = 1500;

// What the direct path carries in the fake world, bytes per second.
const DIRECT = { down: 4_400_000, up: 245_000 };

/**
 * A daemon in memory. It starts from a fixture and answers the methods the
 * window uses, changing its state the way the real one would, only faster
 * and without a network. `still` freezes the numbers for screenshots.
 */
export class MockTransport implements Transport {
  #listeners = new Listeners();
  #status: LinkStatus = 'connecting';
  #reachable: boolean;
  #state: State;
  #topics = new Set<Topic>();
  #journal: JournalEntry[] = [];
  #journalId = 9100;
  #incidentSeq = 7;
  #timers = new Set<ReturnType<typeof setTimeout>>();
  #conns: Connection[] = [];
  #still: boolean;
  #ticks = 0;
  // Bytes per second through the VPN while it works; remembered from the
  // fixture so that a restart brings the same picture back.
  #vpnRate: { up: number; down: number };

  constructor(scenario: string, options: { still?: boolean } = {}) {
    this.#reachable = scenario !== OFFLINE;
    this.#still = options.still ?? false;
    this.#state = fixture(scenario) ?? fixture('ok')!;
    const t = this.#state.traffic;
    this.#vpnRate = t.down > 0 ? { up: t.up, down: t.down } : { up: 18_234, down: 912_340 };
    this.#seedJournal();
    this.#seedConns();
    setTimeout(() => this.#setStatus(this.#reachable ? 'connected' : 'disconnected'), 120);
    setInterval(() => this.#tick(), 1000);
  }

  // ---- Transport ----

  call<M extends MethodName>(method: M, params?: Methods[M]['params']): Promise<Methods[M]['result']> {
    return new Promise((resolve, reject) => {
      // Answers are asynchronous in the real thing too.
      setTimeout(() => {
        if (this.#status !== 'connected') return reject(new LinkError());
        try {
          resolve(this.#handle(method, params) as Methods[M]['result']);
        } catch (err) {
          reject(err);
        }
      }, 20);
    });
  }

  on<N extends NotificationName>(name: N, handler: (params: Notifications[N]) => void): Unsubscribe {
    return this.#listeners.on(name, handler);
  }

  status(): LinkStatus {
    return this.#status;
  }

  onStatus(handler: (status: LinkStatus) => void): Unsubscribe {
    return this.#listeners.onStatus(handler);
  }

  retry(): void {
    if (this.#status === 'connected') return;
    this.#setStatus('connecting');
    // In the "offline" scenario the second try finds the daemon: this way the
    // screen can be seen both failing and recovering.
    setTimeout(() => {
      this.#reachable = true;
      this.#setStatus('connected');
    }, 600);
  }

  // ---- methods ----

  #handle(method: MethodName, params: unknown): unknown {
    const p = (params ?? {}) as Record<string, unknown>;
    const s = this.#state;
    switch (method) {
      case 'hello':
        return { version: s.version, proto: PROTO, os: 'windows' };
      case 'state.get':
        return structuredClone(s);
      case 'state.subscribe': {
        for (const topic of p.topics as Topic[]) this.#topics.add(topic);
        // The real daemon sends the current document right after subscribing.
        if ((p.topics as Topic[]).includes('state')) this.#later(0, () => this.#publishState());
        return {};
      }
      case 'state.unsubscribe':
        for (const topic of p.topics as Topic[]) this.#topics.delete(topic);
        return {};
      case 'vpn.start':
        if (s.vpn.phase === 'stopping') throw new RpcError(ErrorCode.busy, text.busy);
        if (s.vpn.phase === 'on' || s.vpn.phase === 'starting') return {};
        this.#start();
        return {};
      case 'vpn.stop':
        this.#stop();
        return {};
      case 'vpn.restart':
        if (s.vpn.phase === 'off') throw new RpcError(ErrorCode.invalid, text.restartWhileOff);
        if (s.vpn.phase === 'starting' || s.vpn.phase === 'stopping') throw new RpcError(ErrorCode.busy, text.busy);
        this.#start();
        return {};
      case 'mode.set':
        s.vpn.mode = p.mode as State['vpn']['mode'];
        this.#log('vpn', 'info', s.vpn.mode === 'global' ? text.modeGlobal : text.modeRule);
        this.#touch();
        return {};
      case 'server.select':
        this.#select(p.id as string);
        return {};
      case 'settings.set':
        this.#applySettings(p.patch as Partial<Settings>);
        return {};
      case 'incident.action':
        this.#action(p.action as string);
        return {};
      case 'journal.list': {
        const limit = Math.min((p.limit as number | undefined) ?? 100, 500);
        const before = p.before as number | undefined;
        const all = before === undefined ? this.#journal : this.#journal.filter((e) => e.id < before);
        return { entries: structuredClone(all.slice(0, limit)), more: all.length > limit };
      }
      default:
        throw new RpcError(ErrorCode.methodNotFound, text.unknownMethod);
    }
  }

  #start(): void {
    const s = this.#state;
    if (s.servers.length === 0) throw new RpcError(ErrorCode.noServers, text.noServers);
    this.#cancelTimers();
    this.#closeIncidents(['server_down', 'all_servers_down', 'engine_failed', 'helper_down']);
    s.vpn = { ...s.vpn, desired: 'on', phase: 'starting', since: null, failReason: null, failText: null };
    s.health = { ...s.health, verdict: 'settling', since: now(), hint: null };
    s.guard.blocking = false;
    s.guard.engaged = s.guard.enabled;
    s.server.ping = null;
    s.busy = 'starting';
    this.#quiet();
    this.#touch();
    this.#later(START_MS, () => {
      s.vpn.phase = 'on';
      s.vpn.since = now();
      s.busy = null;
      this.#judge(true);
    });
  }

  #stop(): void {
    const s = this.#state;
    this.#cancelTimers();
    this.#closeIncidents(['server_down', 'all_servers_down', 'engine_failed', 'helper_down']);
    if (s.vpn.phase === 'off') {
      s.vpn.desired = 'off';
      this.#touch();
      return;
    }
    s.vpn = { ...s.vpn, desired: 'off', phase: 'stopping', since: null, failReason: null, failText: null };
    s.busy = 'stopping';
    this.#touch();
    this.#later(STOP_MS, () => {
      s.vpn.phase = 'off';
      s.busy = null;
      s.health = { ...s.health, verdict: 'settling', since: null, hint: null };
      s.guard.engaged = false;
      s.guard.blocking = false;
      s.server.ping = null;
      s.server.pingAt = null;
      this.#quiet();
      this.#log('vpn', 'info', text.stopped);
      this.#touch();
    });
  }

  #select(id: string): void {
    const s = this.#state;
    const server = s.servers.find((x) => x.id === id);
    if (!server) throw new RpcError(ErrorCode.notFound, text.noSuchServer);
    if (s.server.selected === id) return;
    s.server = { selected: id, ping: null, pingAt: null };
    this.#log('server', 'info', text.selected(server.name));
    if (s.vpn.phase !== 'on') {
      this.#touch();
      return;
    }
    this.#cancelTimers();
    this.#closeIncidents(['server_down']);
    s.health = { ...s.health, verdict: 'settling', since: now(), hint: null };
    s.traffic.up = s.traffic.down = 0;
    s.conns.vpn = 0;
    this.#touch();
    this.#later(SETTLE_MS, () => this.#judge(false));
  }

  /** Decides what the fake health check finds on the selected server. */
  #judge(started: boolean): void {
    const s = this.#state;
    const server = this.#selected();
    if (!server) return;
    if (!s.health.internet) {
      s.health = { ...s.health, verdict: 'no_internet', since: now(), hint: null };
      this.#touch();
      return;
    }
    if (server.alive) {
      s.health = { ...s.health, verdict: 'ok', since: now(), hint: null };
      s.server.ping = server.ping;
      s.server.pingAt = now();
      s.traffic.up = this.#vpnRate.up;
      s.traffic.down = this.#vpnRate.down;
      s.conns = { total: 143, vpn: 37 };
      if (started) this.#log('vpn', 'info', text.connected(server.name, server.ping));
      this.#touch();
      return;
    }
    // A dead server is confirmed a little later than a live one answers.
    this.#touch();
    this.#later(CONFIRM_DOWN_MS, () => {
      const id = `i-20261112-${++this.#incidentSeq}`;
      const incident: Incident = {
        id,
        kind: 'server_down',
        severity: 'problem',
        opened: now(),
        server: server.id,
        title: text.serverDownTitle(server.name),
        body: text.serverDownBody,
        flaps: 0,
        unstable: false,
      };
      s.health = { ...s.health, verdict: 'server_down', since: now(), hint: text.serverDownHint };
      s.incidents = [...s.incidents, incident];
      s.conns = { total: 12, vpn: 0 };
      this.#log('server', 'problem', `${incident.title}. ${incident.body}`, id);
      this.#touch();
    });
  }

  #applySettings(patch: Partial<Settings>): void {
    const s = this.#state;
    for (const key of Object.keys(patch)) {
      if (!(key in s.settings)) throw new RpcError(ErrorCode.invalidParams, text.badSetting);
    }
    s.settings = { ...s.settings, ...patch };
    s.guard.enabled = s.settings.guard;
    s.guard.allowLan = s.settings.allowLan;
    if (!s.guard.enabled) {
      s.guard.engaged = false;
      s.guard.blocking = false;
    } else if (s.vpn.phase !== 'off') {
      s.guard.engaged = true;
    }
    if (patch.guard !== undefined) this.#log('guard', 'info', patch.guard ? text.guardOn : text.guardOff);
    this.#touch();
  }

  #action(action: string): void {
    const s = this.#state;
    if (action.startsWith('switch:')) return this.#select(action.slice('switch:'.length));
    switch (action) {
      case 'retry':
        return this.#start();
      case 'release':
        s.guard.blocking = false;
        s.guard.engaged = false;
        this.#log('guard', 'problem', text.released);
        this.#touch();
        return;
      case 'update_subs':
        s.busy = 'updating_subs';
        this.#touch();
        this.#later(SUBS_MS, () => {
          s.busy = null;
          this.#closeIncidents(['sub_expiring', 'sub_expired', 'sub_traffic']);
          for (const sub of s.subs) sub.updatedAt = now();
          this.#log('subs', 'info', text.subsUpdated(s.servers.length));
          this.#touch();
        });
        return;
      case 'open':
      case 'dismiss':
        return;
      default:
        throw new RpcError(ErrorCode.invalidParams, text.unknownMethod);
    }
  }

  // ---- plumbing ----

  #selected(): Server | undefined {
    return this.#state.servers.find((x) => x.id === this.#state.server.selected);
  }

  #quiet(): void {
    const s = this.#state;
    s.traffic.up = s.traffic.down = 0;
    s.conns = { total: 0, vpn: 0 };
  }

  #closeIncidents(kinds: Incident['kind'][]): void {
    const s = this.#state;
    s.incidents = s.incidents.filter((incident) => !kinds.includes(incident.kind));
  }

  #touch(): void {
    this.#state.rev++;
    this.#publishState();
  }

  #publishState(): void {
    if (this.#topics.has('state')) this.#listeners.emit('state', structuredClone(this.#state));
  }

  #log(cat: JournalCat, level: Severity, entryText: string, incident: string | null = null): void {
    const entry: JournalEntry = { id: ++this.#journalId, t: now(), cat, level, text: entryText, incident };
    this.#journal.unshift(entry);
    if (this.#topics.has('journal')) this.#listeners.emit('journal', structuredClone(entry));
  }

  #later(ms: number, fn: () => void): void {
    const timer = setTimeout(() => {
      this.#timers.delete(timer);
      fn();
    }, ms);
    this.#timers.add(timer);
  }

  #cancelTimers(): void {
    for (const timer of this.#timers) clearTimeout(timer);
    this.#timers.clear();
    if (this.#state.busy !== null) this.#state.busy = null;
  }

  #setStatus(status: LinkStatus): void {
    if (status === this.#status) return;
    this.#status = status;
    this.#listeners.emitStatus(status);
  }

  /** The journal a scenario starts with, newest first, ending in its story. */
  #seedJournal(): void {
    const s = this.#state;
    const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
    const add = (t: string, cat: JournalCat, level: Severity, entryText: string, incident: string | null = null) =>
      this.#journal.unshift({ id: ++this.#journalId, t, cat, level, text: entryText, incident });

    if (s.servers.length === 0) {
      add(ago(4), 'system', 'info', text.daemonStarted);
      return;
    }
    add(ago(336), 'server', 'info', text.recovered);
    add(ago(198), 'subs', 'info', text.subsUpdated(s.servers.length));
    const first = s.servers[0]!;
    add(s.vpn.since ?? ago(134), 'vpn', 'info', text.connected(first.name, first.ping));

    const incident = s.incidents[0];
    if (incident) add(incident.opened, 'server', incident.severity, `${incident.title}. ${incident.body}`, incident.id);
    else if (s.health.verdict === 'no_internet') add(s.health.since ?? ago(1), 'system', 'problem', text.noInternet);
    else if (s.vpn.phase === 'off') add(ago(1), 'vpn', 'info', text.stopped);
  }

  /** A believable list of connections: the scheme derives speeds from it. */
  #seedConns(): void {
    const names = ['chrome.exe', 'Discord.exe', 'Telegram.exe', 'steam.exe', 'code.exe', 'svchost.exe'];
    for (let i = 0; i < 143; i++) {
      this.#conns.push({
        id: `c${i}`,
        process: names[i % names.length]!,
        host: `host${i}.example.com`,
        dest: `203.0.113.${(i % 250) + 1}:443`,
        route: i < 37 ? 'vpn' : 'direct',
        chain: i < 37 ? ['proxy'] : ['direct'],
        up: 0,
        down: 0,
        started: now(),
      });
    }
  }

  /** Once a second: traffic moves, numbers in State follow every other time. */
  #tick(): void {
    const s = this.#state;
    if (this.#status !== 'connected' || s.vpn.phase !== 'on') return;
    const wobble = () => (this.#still ? 1 : 0.8 + Math.random() * 0.4);
    const vpnFlows = s.health.verdict === 'ok';
    const directFlows = s.health.internet && !s.guard.blocking;
    const vpn = vpnFlows ? { up: this.#vpnRate.up * wobble(), down: this.#vpnRate.down * wobble() } : { up: 0, down: 0 };
    const direct = directFlows ? { up: DIRECT.up * wobble(), down: DIRECT.down * wobble() } : { up: 0, down: 0 };

    const live = this.#conns.filter((c, i) => (c.route === 'vpn' ? i < s.conns.vpn : i - 37 < s.conns.total - s.conns.vpn));
    const vpnCount = Math.max(live.filter((c) => c.route === 'vpn').length, 1);
    const directCount = Math.max(live.filter((c) => c.route === 'direct').length, 1);
    for (const c of live) {
      const rate = c.route === 'vpn' ? vpn : direct;
      const share = c.route === 'vpn' ? vpnCount : directCount;
      c.up += Math.round(rate.up / share);
      c.down += Math.round(rate.down / share);
    }
    if (this.#topics.has('connections')) this.#listeners.emit('connections', structuredClone(live));

    // State carries traffic every other second, like the real daemon.
    if (!vpnFlows || this.#still || ++this.#ticks % 2 !== 0) return;
    s.traffic.up = Math.round(vpn.up + direct.up);
    s.traffic.down = Math.round(vpn.down + direct.down);
    s.traffic.upTotal += s.traffic.up * 2;
    s.traffic.downTotal += s.traffic.down * 2;
    this.#touch();
  }
}

function now(): string {
  return new Date().toISOString();
}
