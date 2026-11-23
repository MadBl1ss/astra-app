import {
  ErrorCode,
  PROTO,
  type Connection,
  type DiagStep,
  type LegacyFolder,
  type LegacyImportResult,
  type Incident,
  type JournalCat,
  type JournalEntry,
  type JournalFilter,
  type MethodName,
  type Methods,
  type NotificationName,
  type Notifications,
  type Place,
  type RuleKind,
  type RuleListName,
  type Rules,
  type Server,
  type Settings,
  type SpeedResult,
  type SpeedSample,
  type Severity,
  type State,
  type Toast,
  type Topic,
} from '../proto';
import { mock as text } from '../i18n/ru';
import {
  answered,
  ConnectionMaker,
  KEY,
  MANY,
  PROCESSES,
  check,
  crowd,
  freshServers,
  listTitle,
  normalize,
  seedLongJournal,
  seedRules,
} from './mockworld';
import { unwrapSubLink } from './mocklinks';
import { LinkError, Listeners, RpcError, type LinkStatus, type Transport, type Unsubscribe } from './types';

// The same files core/proto checks its Go types against.
const fixtures = import.meta.glob<State>('../../../../core/proto/testdata/fixtures/*.json', {
  eager: true,
  import: 'default',
});

/** Scenario names: every fixture, the crowded profile, a daemon that does not answer. */
export const OFFLINE = 'offline';
/** A working VPN that says something of every kind, see #demoToasts. */
export const TOASTS = 'toasts';

export function scenarios(): string[] {
  const names = Object.keys(fixtures).map((path) => path.replace(/^.*\/|\.json$/g, ''));
  return [...names.sort(), MANY, OFFLINE, TOASTS];
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
// A server test: the own engine's start, then each server on its own, as
// many at a time as the daemon does. A dead server holds its slot the
// longest, as the real one holds it for the whole timeout.
const PREPARE_MS = 800;
const TEST_WORKERS = 16;
const TEST_ALIVE_MS = [250, 1300] as const;
const TEST_DEAD_MS = 3000;
// A speed test: pings, then a download of a few seconds, one server at a
// time; shorter than the daemon's eight seconds, enough to watch.
const SPEED_MAX = 12;
const SPEED_DEAD_MS = 1600;
const SPEED_DOWNLOAD_S = 3.6;
const SPEED_GAP_MS = 450;

// What the direct path carries in the fake world, bytes per second.
const DIRECT = { down: 4_400_000, up: 245_000 };

// A share of connections that close each second and are replaced by new
// ones: new connections take the routes of the rules as they are now.
const CHURN = 0.06;

const SUB_INCIDENTS: Incident['kind'][] = ['sub_expiring', 'sub_expired', 'sub_traffic'];
const VPN_INCIDENTS: Incident['kind'][] = ['server_down', 'all_servers_down', 'engine_failed', 'helper_down', 'blocked'];

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
  #rules: Rules;
  #topics = new Set<Topic>();
  #journal: JournalEntry[] = [];
  #journalId = 9100;
  #incidentSeq = 7;
  #timers = new Set<ReturnType<typeof setTimeout>>();
  #conns: Connection[] = [];
  #connCount: number;
  #maker = new ConnectionMaker();
  #still: boolean;
  #ticks = 0;
  #subSeq = 10;
  #linkSeq = 10;
  #urls = new Set<string>();
  // Bytes per second through the VPN while it works; remembered from the
  // fixture so that a restart brings the same picture back.
  #vpnRate: { up: number; down: number };
  // Toasts nobody listened to yet, as the daemon keeps them.
  #held: Toast[] = [];
  #diagBusy = false;
  #legacy: LegacyFolder[] = [];
  #legacyStuck = false;

  constructor(scenario: string, options: { still?: boolean; legacy?: string | null } = {}) {
    this.#reachable = scenario !== OFFLINE;
    this.#still = options.still ?? false;
    let state = fixture(scenario) ?? fixture('ok')!;
    if (scenario === MANY) state = crowd(state, Date.now());
    this.#state = state;
    this.#rules = seedRules(scenario, Date.now(), state.servers.length === 0);
    this.#connCount = scenario === MANY ? 300 : 143;
    const t = this.#state.traffic;
    this.#vpnRate = t.down > 0 ? { up: t.up, down: t.down } : { up: 18_234, down: 912_340 };
    this.#seedJournal(scenario === MANY);
    this.#seedConns();
    // Like the daemon on its first run: the place is asked for at once.
    if (state.place === null && this.#reachable) setTimeout(() => this.#located(), 1500);
    if (scenario === TOASTS) this.#demoToasts();
    if (state.servers.length === 0) this.#seedLegacy(options.legacy ?? null);
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
          Promise.resolve(this.#handle(method, params)).then((v) => resolve(v as Methods[M]['result']), reject);
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
        // The real daemon sends the current document right after subscribing,
        // and the toasts that waited for a listener.
        if ((p.topics as Topic[]).includes('state')) this.#later(0, () => this.#publishState());
        if ((p.topics as Topic[]).includes('toast')) {
          const held = this.#held;
          this.#held = [];
          setTimeout(() => held.forEach((t) => this.#toast(t)), 0);
        }
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
        this.#log('vpn', 'info', text.restarting);
        this.#start();
        return {};
      case 'mode.set':
        s.vpn.mode = p.mode as State['vpn']['mode'];
        this.#log('vpn', 'info', s.vpn.mode === 'global' ? text.modeGlobal : text.modeRule);
        // The engine closes what was opened under the old mode.
        this.#seedConns();
        this.#touch();
        return {};
      case 'server.select':
        this.#select(p.id as string);
        return {};
      case 'server.test':
        return this.#test(p.ids as string[] | undefined);
      case 'server.testStop':
        this.#endTest(text.testStopped);
        this.#endSpeed(text.speedStopped);
        return {};
      case 'server.speedTest':
        return this.#speedTest(p.ids as string[] | undefined);
      case 'server.favorite':
        this.#favorite(p.id as string, p.favorite as boolean);
        return {};
      case 'server.reorder':
        this.#reorder(p.ids as string[]);
        return {};
      case 'server.remove':
        this.#removeServer(p.id as string);
        return {};
      case 'subs.add':
        return this.#addSub(p.url as string, p.name as string | undefined);
      case 'subs.addLink':
        return this.#addLink(p.link as string);
      case 'subs.rename':
        this.#renameSub(p.id as string, p.name as string);
        return {};
      case 'subs.remove':
        this.#removeSub(p.id as string);
        return {};
      case 'subs.update':
        return this.#updateSubs(p.id as string | undefined);
      case 'rules.get':
        return this.#soon(120, () => structuredClone(this.#rules));
      case 'rules.add':
        return this.#addRule(p.list as RuleListName, p.value as string, p.kind as RuleKind | undefined);
      case 'rules.remove':
        this.#removeRule(p.list as RuleListName, p.kind as RuleKind, p.value as string);
        return {};
      case 'rules.check':
        return this.#soon(150, () => {
          const answer = check(this.#rules, s.vpn.mode === 'global', p.target as string);
          if (typeof answer === 'string') throw new RpcError(ErrorCode.invalid, answer);
          return answer;
        });
      case 'rules.processes':
        return this.#soon(400, () => ({ processes: structuredClone(PROCESSES) }));
      case 'settings.set':
        this.#applySettings(p.patch as Partial<Settings>);
        return {};
      case 'incident.action':
        this.#action(p.action as string);
        return {};
      case 'journal.list': {
        const limit = Math.min((p.limit as number | undefined) ?? 100, 500);
        const before = p.before as number | undefined;
        const f = p.filter as JournalFilter | undefined;
        const fits = (e: JournalEntry) =>
          (before === undefined || e.id < before) &&
          (!f?.cats?.length || f.cats.includes(e.cat)) &&
          (!f?.levels?.length || f.levels.includes(e.level)) &&
          (!f?.incident || e.incident === f.incident);
        const all = this.#journal.filter(fits);
        return { entries: structuredClone(all.slice(0, limit)), more: all.length > limit };
      }
      case 'diag.export':
        return this.#soon(900, () => {
          const stamp = new Date().toISOString().replace(/[-:]/g, '').replace('T', '-').slice(0, 15);
          const file = `strelka-report-${stamp}.zip`;
          this.#log('system', 'info', text.reportMade(file));
          return { path: `C:\\ProgramData\\Strelka\\reports\\${file}` };
        });
      case 'engine.versions':
        return this.#soon(250, () => ({
          engines: [
            { name: 'sing-box', version: '1.14.0' },
            { name: 'xray', version: '26.3.27' },
          ],
        }));
      case 'diag.run':
        return this.#diagRun();
      case 'legacy.detect':
        // Only a fresh profile has an old Strelka next to it in the mock:
        // that is where the first run offers to bring it over.
        return this.#soon(300, () => {
          const found = structuredClone(this.#legacy);
          return { dir: found[0]?.dir ?? null, running: found[0]?.running ?? false, found };
        });
      case 'legacy.import':
        return this.#legacyImport(p.dir as string);
      case 'legacy.retire':
        return this.#legacyRetire();
      case 'place.detect':
        return this.#soon(700, () => this.#located());
      case 'place.set': {
        const lat = p.lat as number;
        const lon = p.lon as number;
        const city = String(p.city ?? '').trim();
        if (!(lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180) || city === '') {
          throw new RpcError(ErrorCode.invalidParams, text.placeBad);
        }
        s.place = { lat, lon, city, country: (p.country as string | undefined) ?? null, source: 'manual', at: now() };
        this.#log('system', 'info', text.placeChosen);
        this.#touch();
        return {};
      }
      default:
        throw new RpcError(ErrorCode.methodNotFound, text.unknownMethod);
    }
  }

  /** What the daemon gets from the geo service; the mock always "lives" in Moscow. */
  #located(): Place {
    const place: Place = { lat: 55.7558, lon: 37.6173, city: 'Москва', country: 'RU', source: 'ip', at: now() };
    this.#state.place = place;
    this.#log('system', 'info', text.placeDetected);
    this.#touch();
    return structuredClone(place);
  }

  #start(): void {
    const s = this.#state;
    if (s.servers.length === 0) throw new RpcError(ErrorCode.noServers, text.noServers);
    // A test on its own engine gives way to the tunnel; one through the VPN
    // loses its engine to the restart.
    if (s.test) this.#endTest(text.testInterrupted(s.test.own ? text.testVpnStarting : text.testVpnRestarting));
    this.#cancelTimers();
    this.#closeIncidents(VPN_INCIDENTS, 'close');
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
      this.#seedConns();
      this.#judge(true);
    });
  }

  #stop(): void {
    const s = this.#state;
    if (s.test && !s.test.own) this.#endTest(text.testInterrupted(text.testVpnStopping));
    this.#cancelTimers();
    this.#closeIncidents(VPN_INCIDENTS, 'close');
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
    this.#closeIncidents(['server_down'], 'back');
    s.health = { ...s.health, verdict: 'settling', since: now(), hint: null };
    s.traffic.up = s.traffic.down = 0;
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
      this.#count();
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
      this.#log('server', 'problem', `${incident.title}. ${incident.body}`, id);
      this.#touch();
      // The way out the real daemon offers: the first favourite that answers.
      const alive = s.servers
        .filter((x) => x.alive && x.id !== server.id)
        .sort((a, b) => Number(b.favorite) - Number(a.favorite) || a.order - b.order)[0];
      const actions = alive ? [{ id: `switch:${alive.id}`, label: text.switchTo(alive.name) }] : [];
      this.#toast({
        id,
        severity: 'problem',
        title: incident.title,
        body: incident.body,
        actions: [...actions, { id: 'open', label: text.open }],
        sticky: false,
        close: false,
      });
    });
  }

  // ---- servers and subscriptions ----

  #jobBusy(): void {
    if (this.#state.busy === 'updating_subs') throw new RpcError(ErrorCode.busy, text.subsBusy);
    if (this.#state.busy === 'testing') throw new RpcError(ErrorCode.busy, text.testBusy);
    if (this.#state.busy === 'measuring') throw new RpcError(ErrorCode.busy, text.speedBusy);
  }

  // The run under way; a stop or a newer run replaces it, and its timers go
  // with it.
  #testRun: { timers: Set<ReturnType<typeof setTimeout>> } | null = null;

  #test(ids?: string[]): object {
    const s = this.#state;
    this.#jobBusy();
    if (s.servers.length === 0) throw new RpcError(ErrorCode.noServers, text.noServers);
    if (s.vpn.phase === 'starting' || s.vpn.phase === 'recovering') throw new RpcError(ErrorCode.busy, text.testWhileStarting);
    if (s.vpn.phase === 'stopping') throw new RpcError(ErrorCode.busy, text.testWhileStopping);
    const pool = ids && ids.length > 0 ? [...new Set(ids)] : this.#listOrder();
    for (const id of pool) {
      if (!s.servers.some((x) => x.id === id)) throw new RpcError(ErrorCode.notFound, text.noSuchServer);
    }
    const own = s.vpn.phase !== 'on';
    const run = { timers: new Set<ReturnType<typeof setTimeout>>() };
    this.#testRun = run;
    s.busy = 'testing';
    const test = { started: now(), stage: own ? 'preparing' : 'testing', own, total: pool.length, done: 0, alive: 0 } as const;
    s.test = { ...test };
    const inRound = new Set(pool);
    for (const x of s.servers) x.check = inRound.has(x.id) ? 'queued' : null;
    this.#touch();

    const later = (ms: number, fn: () => void) => {
      const timer = setTimeout(() => {
        run.timers.delete(timer);
        if (this.#testRun === run) fn();
      }, ms);
      run.timers.add(timer);
    };
    const queue = [...pool];
    let running = 0;
    const next = (): void => {
      const id = queue.shift();
      if (id === undefined) {
        if (running === 0) this.#endTest(null);
        return;
      }
      const x = s.servers.find((y) => y.id === id);
      if (!x) return next();
      running++;
      x.check = 'running';
      this.#touch();
      // The fake world: a server that answered before answers again.
      const answers = x.alive && x.ping !== null;
      const [lo, hi] = TEST_ALIVE_MS;
      later(answers ? lo + Math.random() * (hi - lo) : TEST_DEAD_MS, () => {
        running--;
        x.check = 'done';
        x.pingAt = now();
        s.test!.done++;
        if (answers) {
          s.test!.alive++;
          if (!this.#still) x.ping = Math.max(8, Math.round(x.ping! * (0.9 + Math.random() * 0.2)));
          x.stats = answered(x.stats, x.ping!);
        } else {
          x.stats = answered(x.stats, null);
        }
        this.#touch();
        next();
      });
    };
    const begin = () => {
      for (let i = 0; i < Math.min(TEST_WORKERS, pool.length); i++) next();
    };
    if (own) {
      later(PREPARE_MS, () => {
        s.test!.stage = 'testing';
        this.#touch();
        begin();
      });
    } else {
      begin();
    }
    return {};
  }

  // ---- the speed test ----

  #speedRun: { timers: Set<ReturnType<typeof setTimeout>>; interval: ReturnType<typeof setInterval> | null } | null = null;

  /** What a server downloads at in the fake world: steady per server, none for a silent one. */
  #speedOf(x: Server): number | null {
    if (!x.alive || x.ping === null) return null;
    let h = 0;
    for (const c of x.id) h = (h * 31 + c.charCodeAt(0)) >>> 0;
    return Math.round((14 + (h % 1000) / 1000 * 150) * 10) / 10;
  }

  #speedTest(ids?: string[]): object {
    const s = this.#state;
    this.#jobBusy();
    if (s.vpn.phase === 'starting' || s.vpn.phase === 'recovering') throw new RpcError(ErrorCode.busy, text.testWhileStarting);
    if (s.vpn.phase === 'stopping') throw new RpcError(ErrorCode.busy, text.testWhileStopping);
    const pool = ids && ids.length > 0 ? [...new Set(ids)] : s.server.selected ? [s.server.selected] : [];
    if (pool.length === 0) throw new RpcError(ErrorCode.noServers, text.noServers);
    if (pool.length > SPEED_MAX) throw new RpcError(ErrorCode.invalid, text.speedTooMany(SPEED_MAX));
    for (const id of pool) {
      if (!s.servers.some((x) => x.id === id)) throw new RpcError(ErrorCode.notFound, text.noSuchServer);
    }
    const own = s.vpn.phase !== 'on';
    const run = { timers: new Set<ReturnType<typeof setTimeout>>(), interval: null as ReturnType<typeof setInterval> | null };
    this.#speedRun = run;
    s.busy = 'measuring';
    s.speed = {
      started: now(),
      stage: own ? 'preparing' : 'testing',
      own,
      ids: pool,
      current: null,
      phase: null,
      results: [],
      maxBytes: 25 * 1024 * 1024,
      maxSeconds: 8,
    };
    this.#touch();

    const later = (ms: number, fn: () => void) => {
      const timer = setTimeout(() => {
        run.timers.delete(timer);
        if (this.#speedRun === run) fn();
      }, ms);
      run.timers.add(timer);
    };
    const emit = (sample: SpeedSample) => {
      if (this.#topics.has('speed')) this.#listeners.emit('speed', sample);
    };

    const measure = (i: number): void => {
      const id = pool[i];
      if (id === undefined) return this.#endSpeed(null);
      const x = s.servers.find((y) => y.id === id)!;
      const target = this.#speedOf(x);
      s.speed!.current = id;
      s.speed!.phase = 'ping';
      this.#touch();
      const pings: number[] = [];
      const ping = (k: number): void => {
        if (k === 3 || (k === 1 && pings.length === 0)) return afterPings();
        later(target === null ? SPEED_DEAD_MS : 220 + Math.random() * 120, () => {
          const ms = target === null ? null : Math.max(8, Math.round((x.ping ?? 80) * (0.92 + Math.random() * 0.16)));
          if (ms !== null) pings.push(ms);
          emit({ id, phase: 'ping', ping: ms, mbps: 0, bytes: 0, elapsed: 0 });
          ping(k + 1);
        });
      };
      const afterPings = (): void => {
        if (pings.length === 0 || target === null) {
          return done({ id, ping: null, jitter: null, mbps: null, bytes: 0, error: text.speedSilent }, null);
        }
        const sorted = [...pings].sort((a, b) => a - b);
        const mid = sorted[1] ?? sorted[0]!;
        const jitter = Math.round(sorted.reduce((sum, v) => sum + Math.abs(v - mid), 0) / sorted.length);
        s.speed!.phase = 'download';
        this.#touch();
        const started = Date.now();
        let bytes = 0;
        let last = started;
        let settled = 0;
        let settledFrom = 0;
        run.interval = setInterval(() => {
          if (this.#speedRun !== run) return;
          const t = Date.now();
          const el = (t - started) / 1000;
          // A connection that speeds up and wobbles, the way a real one does.
          const rate = target * (1 - Math.exp(-el / 0.55)) * (1 + 0.07 * Math.sin(el * 7.3) + (Math.random() - 0.5) * 0.08);
          bytes += ((rate * 1e6) / 8) * ((t - last) / 1000);
          last = t;
          if (el > 0.8) {
            settled += rate;
            settledFrom++;
          }
          emit({ id, phase: 'download', ping: null, mbps: Math.round(rate * 10) / 10, bytes: Math.round(bytes), elapsed: t - started });
          if (el >= SPEED_DOWNLOAD_S) {
            clearInterval(run.interval!);
            run.interval = null;
            const mbps = Math.round((settled / Math.max(1, settledFrom)) * 10) / 10;
            done({ id, ping: mid, jitter, mbps, bytes: Math.round(bytes), error: null }, pings);
          }
        }, 120);
      };
      const done = (result: SpeedResult, got: number[] | null): void => {
        s.speed!.results = [...s.speed!.results, result];
        s.speed!.current = null;
        s.speed!.phase = null;
        x.pingAt = now();
        if (got && result.ping !== null) {
          x.alive = true;
          x.ping = result.ping;
          for (const ms of got) x.stats = answered(x.stats, ms);
        } else {
          x.alive = false;
          x.stats = answered(x.stats, null);
        }
        if (result.mbps !== null && x.stats) {
          const prev = x.stats.speed;
          const speed = prev === null ? result.mbps : Math.round(Math.sqrt(prev * result.mbps) * 10) / 10;
          x.stats = { ...answered({ ...x.stats, samples: x.stats.samples - 1, speed, speedAt: now() }, x.ping) };
        }
        this.#touch();
        later(SPEED_GAP_MS, () => measure(i + 1));
      };
      ping(0);
    };

    if (own) {
      later(PREPARE_MS, () => {
        s.speed!.stage = 'testing';
        this.#touch();
        measure(0);
      });
    } else {
      later(200, () => measure(0));
    }
    return {};
  }

  /** Ends the speed test; lead is null for one that got through every server. */
  #endSpeed(lead: string | null): void {
    const s = this.#state;
    const run = this.#speedRun;
    if (!run || !s.speed) return;
    for (const timer of run.timers) clearTimeout(timer);
    if (run.interval) clearInterval(run.interval);
    this.#speedRun = null;
    let best: SpeedResult | null = null;
    for (const r of s.speed.results) if (r.mbps !== null && (best === null || r.mbps > best.mbps!)) best = r;
    const name = best ? (s.servers.find((x) => x.id === best!.id)?.name ?? best.id) : '';
    const summary = best ? text.speedBest(name, best.mbps!) : lead === null ? text.speedNone : '';
    const line = [lead, summary].filter(Boolean).join(' ');
    if (line) this.#log('server', 'info', line);
    s.speed = null;
    if (s.busy === 'measuring') s.busy = null;
    this.#touch();
  }

  /** The window's order: favourites, each subscription, servers by link. */
  #listOrder(): string[] {
    const s = this.#state;
    const out = s.servers
      .filter((x) => x.favorite)
      .sort((a, b) => a.order - b.order)
      .map((x) => x.id);
    for (const sub of s.subs) out.push(...s.servers.filter((x) => x.sub === sub.id && !x.favorite).map((x) => x.id));
    out.push(...s.servers.filter((x) => !out.includes(x.id)).map((x) => x.id));
    return out;
  }

  /** Ends the run; lead is null for one that got through every server. */
  #endTest(lead: string | null): void {
    const s = this.#state;
    const run = this.#testRun;
    if (!run || !s.test) return;
    for (const timer of run.timers) clearTimeout(timer);
    this.#testRun = null;
    const { done, total, alive } = s.test;
    if (lead === null) this.#log('server', 'info', text.testDone(alive, total));
    else this.#log('server', 'info', done > 0 ? text.testCounted(lead, done, total, alive) : lead);
    s.test = null;
    if (s.busy === 'testing') s.busy = null;
    for (const x of s.servers) x.check = null;
    this.#touch();
  }

  #favorite(id: string, favorite: boolean): void {
    const s = this.#state;
    const server = s.servers.find((x) => x.id === id);
    if (!server) throw new RpcError(ErrorCode.notFound, text.noSuchServer);
    if (server.favorite === favorite) return;
    if (favorite) {
      server.order = Math.max(-1, ...s.servers.filter((x) => x.favorite).map((x) => x.order)) + 1;
      server.favorite = true;
    } else {
      server.favorite = false;
      server.order = 0;
      this.#renumber(s.servers.filter((x) => x.favorite).sort((a, b) => a.order - b.order).map((x) => x.id));
    }
    this.#touch();
  }

  #reorder(ids: string[]): void {
    const favorites = this.#state.servers.filter((x) => x.favorite).map((x) => x.id);
    if (ids.length !== favorites.length || !ids.every((id) => favorites.includes(id))) {
      throw new RpcError(ErrorCode.invalid, text.reorderBad);
    }
    this.#renumber(ids);
    this.#touch();
  }

  #renumber(ids: string[]): void {
    for (const [i, id] of ids.entries()) {
      const x = this.#state.servers.find((s) => s.id === id);
      if (x) x.order = i;
    }
  }

  #removeServer(id: string): void {
    const s = this.#state;
    this.#jobBusy();
    const server = s.servers.find((x) => x.id === id);
    if (!server) throw new RpcError(ErrorCode.notFound, text.noSuchServer);
    if (server.sub !== '') throw new RpcError(ErrorCode.invalid, text.serverFromSub);
    s.servers = s.servers.filter((x) => x.id !== id);
    this.#log('server', 'info', text.serverRemoved(server.name));
    this.#afterServersChanged();
  }

  async #addSub(url: string, name: string | undefined): Promise<{ id: string; servers: number }> {
    const s = this.#state;
    this.#jobBusy();
    const link = unwrapSubLink(url ?? '');
    if (!link.ok) {
      const why = {
        notLink: text.notUrl,
        noSub: text.noSubInside,
        server: text.serverInside,
        encrypted: 'app' in link ? text.encrypted(link.app) : '',
        routing: 'app' in link ? text.appRouting(link.app) : '',
      }[link.why];
      throw new RpcError(ErrorCode.invalid, why);
    }
    const clean = link.url;
    if (this.#urls.has(clean)) throw new RpcError(ErrorCode.invalid, text.subExists);
    s.busy = 'updating_subs';
    this.#touch();
    await this.#wait(SUBS_MS);
    s.busy = null;
    if (/fail|404/i.test(clean)) {
      this.#touch();
      throw new RpcError(ErrorCode.fetchFailed, text.subFailed);
    }
    this.#urls.add(clean);
    const id = `s${++this.#subSeq}`;
    const title = name?.trim() || link.name || text.newSubName;
    const servers = freshServers(id, `n${this.#subSeq}`, 6, Date.now(), this.#subSeq);
    s.servers = [...s.servers, ...servers];
    s.subs = [
      ...s.subs,
      {
        id,
        name: title,
        url: `${clean.replace(/^(https?:\/\/[^/]+).*$/, '$1')}/…${clean.slice(-4)}`,
        servers: servers.length,
        updatedAt: now(),
        error: null,
        expireAt: null,
        usedBytes: null,
        totalBytes: null,
      },
    ];
    this.#log('subs', 'info', text.subAdded(title, servers.length));
    this.#afterServersChanged();
    return { id, servers: servers.length };
  }

  #addLink(link: string): { id: string } {
    const s = this.#state;
    this.#jobBusy();
    const clean = (link ?? '').trim();
    const scheme = /^(vless|trojan|ss|hysteria2|hy2):\/\/(?:[^@\s]*@)?([^:/?#\s]+)/i.exec(clean);
    if (!scheme) throw new RpcError(ErrorCode.invalid, text.notLink);
    const protocol = ({ vless: 'vless', trojan: 'trojan', ss: 'shadowsocks', hysteria2: 'hysteria2', hy2: 'hysteria2' } as const)[
      scheme[1]!.toLowerCase() as 'vless'
    ];
    let label = text.linkName;
    const hash = clean.indexOf('#');
    if (hash >= 0) {
      try {
        label = decodeURIComponent(clean.slice(hash + 1)) || label;
      } catch {
        // A broken fragment keeps the default name.
      }
    }
    const id = `link-${++this.#linkSeq}`;
    const server: Server = {
      id,
      name: label,
      country: null,
      sub: '',
      protocol,
      via: 'sing-box',
      host: scheme[2]!,
      favorite: false,
      order: 0,
      ping: null,
      pingAt: null,
      alive: false,
      stats: null,
      check: null,
    };
    s.servers = [...s.servers, server];
    this.#log('server', 'info', text.linkAdded(label));
    this.#afterServersChanged();
    return { id };
  }

  #renameSub(id: string, name: string): void {
    this.#jobBusy();
    const sub = this.#state.subs.find((x) => x.id === id);
    if (!sub) throw new RpcError(ErrorCode.notFound, text.noSuchSub);
    if (!name?.trim()) throw new RpcError(ErrorCode.invalid, text.nameEmpty);
    sub.name = name.trim();
    this.#touch();
  }

  #removeSub(id: string): void {
    const s = this.#state;
    this.#jobBusy();
    const sub = s.subs.find((x) => x.id === id);
    if (!sub) throw new RpcError(ErrorCode.notFound, text.noSuchSub);
    s.subs = s.subs.filter((x) => x.id !== id);
    s.servers = s.servers.filter((x) => x.sub !== id);
    this.#log('subs', 'info', text.subRemoved(sub.name));
    this.#afterServersChanged();
  }

  async #updateSubs(id: string | undefined): Promise<object> {
    const s = this.#state;
    this.#jobBusy();
    const asked = id === undefined ? s.subs : s.subs.filter((x) => x.id === id);
    if (id !== undefined && asked.length === 0) throw new RpcError(ErrorCode.notFound, text.noSuchSub);
    s.busy = 'updating_subs';
    this.#touch();
    await this.#wait(SUBS_MS);
    s.busy = null;
    let fresh = 0;
    for (const sub of asked) {
      // The broken one of the crowded scenario stays broken.
      if (sub.error) continue;
      sub.updatedAt = now();
      fresh++;
    }
    if (fresh > 0) {
      this.#closeIncidents(SUB_INCIDENTS);
      this.#log('subs', 'info', text.subsUpdated(s.servers.length));
    }
    this.#touch();
    if (fresh === 0 && asked.length > 0) throw new RpcError(ErrorCode.fetchFailed, text.subStillBroken);
    return {};
  }

  /** What the daemon does after servers came or went: fix the choice. */
  #afterServersChanged(): void {
    const s = this.#state;
    for (const sub of s.subs) sub.servers = s.servers.filter((x) => x.sub === sub.id).length;
    if (!s.servers.some((x) => x.id === s.server.selected)) {
      const next = s.servers.filter((x) => x.favorite).sort((a, b) => a.order - b.order)[0] ?? s.servers[0];
      s.server = { selected: next?.id ?? null, ping: null, pingAt: null };
    }
    if (s.servers.length === 0 && s.vpn.desired === 'on') this.#stop();
    this.#touch();
  }

  // ---- rules ----

  #addRule(list: RuleListName, value: string, kind: RuleKind | undefined): { kind: RuleKind; value: string } {
    if (list !== 'vpn' && list !== 'direct') throw new RpcError(ErrorCode.invalidParams, text.unknownMethod);
    const n = normalize(kind, value);
    if (typeof n === 'string') throw new RpcError(ErrorCode.invalid, n);
    const key = KEY[n.kind];
    const other = list === 'vpn' ? 'direct' : 'vpn';
    this.#rules[other][key] = this.#rules[other][key].filter((x) => x !== n.value);
    if (!this.#rules[list][key].includes(n.value)) this.#rules[list][key] = [...this.#rules[list][key], n.value];
    this.#log('rules', 'info', text.ruleAdded(listTitle(list), n.value));
    return n;
  }

  #removeRule(list: RuleListName, kind: RuleKind, value: string): void {
    const key = KEY[kind];
    if (!key || !this.#rules[list]?.[key].includes(value)) throw new RpcError(ErrorCode.notFound, text.noSuchRule);
    this.#rules[list][key] = this.#rules[list][key].filter((x) => x !== value);
    this.#log('rules', 'info', text.ruleRemoved(listTitle(list), value));
  }

  // ---- settings and incidents ----

  #applySettings(patch: Partial<Settings>): void {
    const s = this.#state;
    for (const key of Object.keys(patch)) {
      if (!(key in s.settings)) throw new RpcError(ErrorCode.invalidParams, text.badSetting);
    }
    const port = patch.localProxyPort;
    if (port !== undefined && port !== 0 && !(Number.isInteger(port) && port >= 1024 && port <= 65535)) {
      throw new RpcError(ErrorCode.invalidParams, text.badPort);
    }
    const before = s.settings;
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
    // These two are baked into the engine's config.
    const rebuild = before.ipv6 !== s.settings.ipv6 || before.localProxyPort !== s.settings.localProxyPort;
    if (rebuild && s.vpn.phase === 'on') this.#start();
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
        this.#closeIncidents(['blocked', 'engine_failed'], 'close');
        this.#log('guard', 'problem', text.released);
        this.#touch();
        return;
      case 'update_subs':
        void this.#updateSubs(undefined).catch(() => {});
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

  #count(): void {
    const vpn = this.#conns.filter((c) => c.route === 'vpn').length;
    this.#state.conns = { total: this.#conns.length, vpn };
  }

  /**
   * Ends incidents. "back" says it works again, with how long it did not;
   * "close" takes the toast away without a word: the user turned the VPN
   * off or on again, and the old problem means nothing now.
   */
  #closeIncidents(kinds: Incident['kind'][], how: 'back' | 'close' | 'quiet' = 'quiet'): void {
    const s = this.#state;
    const ending = s.incidents.filter((incident) => kinds.includes(incident.kind));
    s.incidents = s.incidents.filter((incident) => !kinds.includes(incident.kind));
    for (const incident of ending) {
      if (how === 'close') {
        this.#toast({ id: incident.id, severity: 'info', title: '', body: '', actions: [], sticky: false, close: true });
      } else if (how === 'back') {
        const name = s.servers.find((x) => x.id === incident.server)?.name ?? '';
        const seconds = Math.max(1, Math.round((Date.now() - Date.parse(incident.opened)) / 1000));
        this.#log('server', 'info', text.backBody(name, seconds), incident.id);
        const body = text.backBody(name, seconds);
        this.#toast({ id: incident.id, severity: 'info', title: text.backTitle, body, actions: [], sticky: false, close: false });
      }
    }
  }

  /** The connection check, a step every half second, as the daemon reports it. */
  async #diagRun(): Promise<{ steps: DiagStep[] }> {
    if (this.#diagBusy) throw new RpcError(ErrorCode.busy, text.diagBusy);
    this.#diagBusy = true;
    const s = this.#state;
    const server = this.#selected();
    const steps: DiagStep[] = [];
    const say = (step: DiagStep) => {
      if (this.#topics.has('diag')) this.#listeners.emit('diag', structuredClone(step));
    };
    const order: DiagStep['step'][] = ['network', 'internet', 'dns', 'engine', 'server', 'site'];
    let stop: string | null = null;
    let skipAdvice: string | null = null;
    try {
      for (const name of order) {
        if (stop !== null) {
          const step: DiagStep = { step: name, status: 'skip', text: stop, advice: skipAdvice };
          skipAdvice = null;
          steps.push(step);
          say(step);
          continue;
        }
        say({ step: name, status: 'running', text: text.diagRunning[name], advice: null });
        await this.#wait(this.#still ? 0 : 450);
        let step: DiagStep;
        switch (name) {
          case 'network':
            step = s.health.network
              ? { step: name, status: 'ok', text: text.diagOk.network, advice: null }
              : { step: name, status: 'fail', text: text.diagFail.network, advice: text.diagAdvice.network };
            if (step.status === 'fail') stop = text.diagSkipNoNetwork;
            break;
          case 'internet':
            step = s.health.internet
              ? { step: name, status: 'ok', text: text.diagOk.internet, advice: null }
              : { step: name, status: 'fail', text: text.diagFail.internet, advice: text.diagAdvice.internet };
            if (step.status === 'fail') stop = text.diagSkipNoInternet;
            break;
          case 'dns':
            step = { step: name, status: 'ok', text: text.diagOk.dns, advice: null };
            break;
          case 'engine':
            if (s.vpn.phase === 'off' || s.vpn.phase === 'stopping') {
              step = { step: name, status: 'skip', text: text.diagVpnOff, advice: text.diagAdvice.vpnOff };
              stop = text.diagSkipVpnOff;
            } else if (s.vpn.phase !== 'on') {
              step = { step: name, status: 'fail', text: text.diagFail.engine, advice: text.diagAdvice.engine };
              stop = text.diagSkipVpnBroken;
            } else {
              step = { step: name, status: 'ok', text: text.diagOk.engine, advice: null };
            }
            break;
          case 'server':
            step = server?.alive
              ? { step: name, status: 'ok', text: text.diagOkServer(server.name, server.ping ?? 0), advice: null }
              : { step: name, status: 'fail', text: text.diagFailServer(server?.name ?? ''), advice: text.diagAdvice.server };
            if (step.status === 'fail') stop = text.diagSkipServer;
            break;
          case 'site':
            step = { step: name, status: 'ok', text: text.diagOkSite((server?.ping ?? 80) * 3), advice: null };
            break;
        }
        steps.push(step);
        say(step);
      }
      const broken = steps.find((x) => x.status === 'fail');
      this.#log('system', 'info', broken ? text.diagJournalBroken(broken.text) : text.diagJournalOk);
      return { steps };
    } finally {
      this.#diagBusy = false;
    }
  }

  /**
   * Old Strelkas next to a fresh profile, `?legacy=` picks which: two (the
   * default, as on the development machine: one running, one started at
   * logon), one, none, or "stuck" - two that refuse to be turned off.
   */
  #seedLegacy(kind: string | null): void {
    const running: LegacyFolder = { dir: text.legacyDirRunning, running: true, autostart: false, subs: ['klex'] };
    const atLogin: LegacyFolder = { dir: text.legacyDir, running: false, autostart: true, subs: ['klex', 'port', 'lider'] };
    switch (kind) {
      case 'none':
        return;
      case 'one':
        this.#legacy = [{ ...atLogin, running: false, autostart: false }];
        return;
      case 'stuck':
        this.#legacyStuck = true;
    }
    this.#legacy = [running, atLogin];
  }

  /** What an old Strelka in the mock brings: its subscriptions, its rules. */
  async #legacyImport(dir: string): Promise<LegacyImportResult> {
    const s = this.#state;
    this.#jobBusy();
    const from = this.#legacy.find((f) => f.dir === dir);
    if (!from) throw new RpcError(ErrorCode.invalid, text.legacyNotHere);
    s.busy = 'updating_subs';
    this.#touch();
    await this.#wait(SUBS_MS);
    s.busy = null;
    const subs = from.subs.map((name, i) => ({ id: `s${++this.#subSeq}`, name: name || 'sub', n: 8 - i * 3 }));
    for (const sub of subs) {
      const servers = freshServers(sub.id, sub.name, sub.n, Date.now(), this.#subSeq + sub.n);
      s.servers = [...s.servers, ...servers];
      s.subs = [
        ...s.subs,
        { id: sub.id, name: sub.name, url: 'https://panel.example.com/…a1b2', servers: servers.length, updatedAt: now(), error: null, expireAt: null, usedBytes: null, totalBytes: null },
      ];
    }
    s.server = { selected: s.servers.find((x) => x.alive)?.id ?? null, ping: null, pingAt: null };
    this.#log('system', 'info', text.legacyImported(subs.length));
    this.#touch();
    return { subs: subs.length, servers: s.servers.length, rules: 24, selected: s.server.selected, mode: 'rule', failed: [] };
  }

  /** Turns the mock's old Strelkas off, as the daemon would. */
  async #legacyRetire(): Promise<{ stopped: number; task: 'disabled' | 'absent' }> {
    await this.#wait(900);
    if (this.#legacyStuck) {
      this.#log('system', 'problem', text.legacyStuck);
      throw new RpcError(ErrorCode.invalid, text.legacyStuck);
    }
    const stopped = this.#legacy.filter((f) => f.running).length * 2;
    const task = this.#legacy.some((f) => f.autostart) ? 'disabled' : 'absent';
    this.#legacy = this.#legacy.map((f) => ({ ...f, running: false, autostart: false }));
    this.#log('system', 'info', text.legacyRetired(stopped));
    return { stopped, task };
  }

  /** Sends a toast, or keeps it for the first listener, as the daemon does. */
  #toast(t: Toast): void {
    if (this.#topics.has('toast')) {
      this.#listeners.emit('toast', structuredClone(t));
      return;
    }
    this.#held = this.#held.filter((x) => x.id !== t.id);
    if (!t.close && !t.id.startsWith('e-')) this.#held.push(t);
  }

  /**
   * The "toasts" scenario: one of every kind, a replacement by id, a
   * critical one with two buttons and four in a row, so the stack folds.
   */
  #demoToasts(): void {
    const s = this.#state;
    const first = s.servers[0];
    const name = (i: number) => s.servers[i]?.name ?? '';
    const toSwitch = { id: `switch:${first?.id ?? ''}`, label: text.switchTo(name(0)) };
    const open = { id: 'open', label: text.open };
    const toast = (id: string, severity: Toast['severity'], title: string, body: string, actions: Toast['actions'] = []): Toast => ({
      id,
      severity,
      title,
      body,
      actions,
      sticky: severity === 'critical',
      close: false,
    });
    const plan: [number, Toast][] = [
      [1500, toast('e-connected', 'info', text.connectedTitle, text.connected(name(0), first?.ping ?? null))],
      [3000, toast('i-demo-1', 'problem', text.serverDownTitle(name(1)), text.serverDownBody, [toSwitch, open])],
      [6000, toast('i-demo-1', 'problem', text.serverDownTitle(name(1)), `${text.hintPort} ${text.serverDownBody}`, [toSwitch, open])],
      [8000, toast('i-demo-2', 'critical', text.blockedTitle, text.blockedBody, [{ id: 'retry', label: text.retry }, { id: 'release', label: text.release }])],
      [10000, toast('i-demo-3', 'problem', text.subExpiringTitle(s.subs[0]?.name ?? ''), text.subExpiringBody, [{ id: 'update_subs', label: text.updateSubs }])],
      [10300, toast('i-demo-4', 'problem', text.helperTitle, text.helperBody, [open])],
      [10600, toast('e-subs', 'info', text.subsAutoTitle, text.subsAutoBody)],
      [10900, toast('i-demo-1', 'info', text.backTitle, text.backBody(name(1), 8))],
    ];
    for (const [ms, t] of plan) setTimeout(() => this.#toast(t), ms);
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

  /** A pause that a stop or a start does not cancel: a download runs on. */
  #wait(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
  }

  /** An answer that takes a moment, like the real daemon's work off its loop. */
  async #soon<T>(ms: number, fn: () => T): Promise<T> {
    await this.#wait(ms);
    return fn();
  }

  #cancelTimers(): void {
    for (const timer of this.#timers) clearTimeout(timer);
    this.#timers.clear();
    if (this.#state.busy === 'starting' || this.#state.busy === 'stopping') this.#state.busy = null;
  }

  #setStatus(status: LinkStatus): void {
    if (status === this.#status) return;
    this.#status = status;
    this.#listeners.emitStatus(status);
  }

  /** The journal a scenario starts with, newest first, ending in its story. */
  #seedJournal(long: boolean): void {
    const s = this.#state;
    const seeds: Omit<JournalEntry, 'id'>[] = [];
    const add = (t: number, cat: JournalCat, level: Severity, entryText: string, incident: string | null = null) =>
      seeds.push({ t: new Date(t).toISOString(), cat, level, text: entryText, incident });
    const ago = (minutes: number) => Date.now() - minutes * 60_000;

    if (s.servers.length === 0) {
      add(ago(4), 'system', 'info', text.daemonStarted);
    } else {
      if (long) seedLongJournal(add, s.servers, Date.now());
      // An incident that is over: three lines, one entry on the journal screen.
      add(ago(339), 'server', 'problem', text.downAgain, 'i-20261112-5');
      add(ago(338), 'server', 'problem', `${text.downAgain.replace(/\..*$/, '.')} ${text.serverDownHint}`, 'i-20261112-5');
      add(ago(336), 'server', 'info', text.backAgain, 'i-20261112-5');
      add(ago(198), 'subs', 'info', text.subsUpdated(s.servers.length));
      const first = s.servers[0]!;
      add(Date.parse(s.vpn.since ?? '') || ago(134), 'vpn', 'info', text.connected(first.name, first.ping));

      const incident = s.incidents.find((i) => VPN_INCIDENTS.includes(i.kind));
      if (incident) add(Date.parse(incident.opened), 'server', incident.severity, `${incident.title}. ${incident.body}`, incident.id);
      else if (s.health.verdict === 'no_internet') add(Date.parse(s.health.since ?? '') || ago(1), 'system', 'problem', text.noInternet);
      else if (s.vpn.phase === 'off') add(ago(1), 'vpn', 'info', text.stopped);
    }
    // Ids follow time, as in the real journal.
    seeds.sort((a, b) => a.t.localeCompare(b.t));
    for (const seed of seeds) this.#journal.unshift({ id: ++this.#journalId, ...seed });
  }

  /** A believable list of connections: the scheme and the rules screen use it. */
  #seedConns(): void {
    const global = this.#state.vpn.mode === 'global';
    this.#conns = Array.from({ length: this.#connCount }, () => this.#maker.make(this.#rules, global));
  }

  /** Once a second: traffic moves, numbers in State follow every other time. */
  #tick(): void {
    const s = this.#state;
    if (this.#status !== 'connected' || s.vpn.phase !== 'on') return;
    const global = s.vpn.mode === 'global';
    if (!this.#still) {
      for (let i = 0; i < this.#conns.length; i++) {
        if (Math.random() < CHURN) this.#conns[i] = this.#maker.make(this.#rules, global);
      }
    }
    const wobble = () => (this.#still ? 1 : 0.8 + Math.random() * 0.4);
    const vpnFlows = s.health.verdict === 'ok';
    const directFlows = s.health.internet && !s.guard.blocking;
    const vpn = vpnFlows ? { up: this.#vpnRate.up * wobble(), down: this.#vpnRate.down * wobble() } : { up: 0, down: 0 };
    const direct = directFlows ? { up: DIRECT.up * wobble(), down: DIRECT.down * wobble() } : { up: 0, down: 0 };

    const vpnCount = Math.max(this.#conns.filter((c) => c.route === 'vpn').length, 1);
    const directCount = Math.max(this.#conns.filter((c) => c.route === 'direct').length, 1);
    for (const [i, c] of this.#conns.entries()) {
      const rate = c.route === 'vpn' ? vpn : direct;
      const share = c.route === 'vpn' ? vpnCount : directCount;
      // Uneven, as in life: a few connections carry most of the bytes.
      const weight = i % 9 === 0 ? 5 : 0.5;
      c.up += Math.round((rate.up / share) * weight);
      c.down += Math.round((rate.down / share) * weight);
    }
    if (this.#topics.has('connections')) this.#listeners.emit('connections', structuredClone(this.#conns));

    // State carries traffic every other second, like the real daemon.
    if (this.#still || ++this.#ticks % 2 !== 0) return;
    this.#count();
    if (!vpnFlows) return;
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
