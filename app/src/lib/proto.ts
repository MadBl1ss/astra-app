// Mirror of core/proto, the Go side of the protocol: a change goes to
// both at once.

export const PROTO = 1;

export type Desired = 'on' | 'off';
export type Phase = 'off' | 'starting' | 'on' | 'recovering' | 'stopping' | 'failed';
export type Mode = 'rule' | 'global';
export type FailReason = 'config' | 'tun' | 'port' | 'crashloop' | 'missing';

export type Verdict =
  | 'ok'
  | 'settling'
  | 'degraded'
  | 'server_down'
  | 'all_servers_down'
  | 'no_internet'
  | 'no_network'
  | 'engine_recovering'
  | 'engine_failed'
  | 'helper_down';

export type Busy = 'starting' | 'stopping' | 'updating_subs' | 'testing' | 'measuring';
export type Severity = 'critical' | 'problem' | 'info';

export type IncidentKind =
  | 'server_down'
  | 'all_servers_down'
  | 'engine_failed'
  | 'helper_down'
  | 'blocked'
  | 'sub_expiring'
  | 'sub_expired'
  | 'sub_traffic';

export interface State {
  proto: number;
  version: string;
  rev: number;
  vpn: {
    desired: Desired;
    phase: Phase;
    /** When phase became "on". */
    since: string | null;
    mode: Mode;
    failReason: FailReason | null;
    failText: string | null;
  };
  health: {
    verdict: Verdict;
    since: string | null;
    hint: string | null;
    internet: boolean;
    network: boolean;
  };
  guard: {
    /** The setting. */
    enabled: boolean;
    /** The filters are in place. */
    engaged: boolean;
    /** The filters are in place and the engine has been gone for over 10 s. */
    blocking: boolean;
    allowLan: boolean;
  };
  server: {
    selected: string | null;
    ping: number | null;
    pingAt: string | null;
  };
  servers: Server[];
  subs: Sub[];
  traffic: { up: number; down: number; upTotal: number; downTotal: number };
  conns: { total: number; vpn: number };
  /** Open incidents only. */
  incidents: Incident[];
  settings: Settings;
  busy: Busy | null;
  /** The server test under way; null when there is none. */
  test: TestRun | null;
  /** The speed test under way; null when there is none. */
  speed: SpeedRun | null;
  /** Where the computer is, for the globe on Home; null while not known. */
  place: Place | null;
}

/** Progress of a server test. */
export interface TestRun {
  started: string;
  /** "preparing": the test's own engine is coming up (the VPN is off). */
  stage: 'preparing' | 'testing';
  /** The test runs on an engine of its own, since the VPN is off. */
  own: boolean;
  total: number;
  /** Answered or timed out. */
  done: number;
  /** Answered. */
  alive: number;
}

/** Where a server is in the test under way. */
export type Check = 'queued' | 'running' | 'done';

/** A speed test: one server after another, pinged and made to download. */
export interface SpeedRun {
  started: string;
  /** "preparing": the test's own engine is coming up (the VPN is off). */
  stage: 'preparing' | 'testing';
  own: boolean;
  /** In the order they are measured. */
  ids: string[];
  /** The server being measured and what is done to it; null between servers. */
  current: string | null;
  phase: SpeedPhase | null;
  /** Measured so far, in order. */
  results: SpeedResult[];
  /** The bounds of one download. */
  maxBytes: number;
  maxSeconds: number;
}

export type SpeedPhase = 'ping' | 'download';

export interface SpeedResult {
  id: string;
  /** The middle of its pings; null: it did not answer. */
  ping: number | null;
  jitter: number | null;
  /** Mbit/s; null: the download failed. */
  mbps: number | null;
  /** Traffic the download took. */
  bytes: number;
  /** Why a number is missing, ready for the user. */
  error: string | null;
}

/** The "speed" notification: the server being measured, a few times a second. */
export interface SpeedSample {
  id: string;
  phase: SpeedPhase;
  /** The ping that has just come, in the ping phase; null: no answer. */
  ping: number | null;
  /** In the download phase: Mbit/s over the last moments, bytes so far, ms since the first byte. */
  mbps: number;
  bytes: number;
  elapsed: number;
}

/** What the daemon has learnt about a server over time. */
export interface ServerStats {
  /** Typical delay, ms; 0 when it never answered. */
  avg: number;
  /** How much the delay wobbles around that, ms. */
  jitter: number;
  /** Share of checks without an answer, 0..1. */
  loss: number;
  samples: number;
  /** Delay, wobble, answers and speed together, 0..100. */
  score: number;
  /** 0..1: how much of the score is evidence; little evidence scores near 50. */
  trust: number;
  /** Days with answers in the last two weeks. */
  days: number;
  /** Answers nearly always, evenly, on several days. */
  stable: boolean;
  /** Typical download speed, Mbit/s; null until measured. */
  speed: number | null;
  speedAt: string | null;
}

export interface Place {
  lat: number;
  lon: number;
  /** What to show; may be empty. */
  city: string;
  country: string | null;
  /** "ip": found by the address; "manual": chosen in Settings. */
  source: 'ip' | 'manual';
  /** When it was found or chosen. */
  at: string;
}

export interface Server {
  id: string;
  name: string;
  country: string | null;
  sub: string;
  protocol: 'vless' | 'trojan' | 'shadowsocks' | 'hysteria2';
  via: 'sing-box' | 'xray';
  host: string;
  favorite: boolean;
  order: number;
  ping: number | null;
  pingAt: string | null;
  alive: boolean;
  stats: ServerStats | null;
  /** Only while a test runs and the server is part of it. */
  check: Check | null;
}

export interface Sub {
  id: string;
  name: string;
  /** Always masked. */
  url: string;
  servers: number;
  updatedAt: string | null;
  error: string | null;
  expireAt: string | null;
  usedBytes: number | null;
  totalBytes: number | null;
}

export interface Incident {
  id: string;
  kind: IncidentKind;
  severity: Severity;
  opened: string;
  server: string | null;
  title: string;
  body: string;
  flaps: number;
  unstable: boolean;
}

export interface Settings {
  notify: 'all' | 'important' | 'silent';
  quietFullscreen: boolean;
  guard: boolean;
  allowLan: boolean;
  ipv6: 'tunnel' | 'block';
  failover: boolean;
  autoUpdateSubs: 'off' | '6h' | '12h' | '24h';
  localProxyPort: number;
  startOnBoot: boolean;
  /** While the VPN works, a few servers are pinged now and then to keep their rating fresh. */
  rating: boolean;
}

export type JournalCat = 'vpn' | 'server' | 'subs' | 'rules' | 'guard' | 'system';

export interface JournalEntry {
  id: number;
  t: string;
  cat: JournalCat;
  level: Severity;
  text: string;
  incident: string | null;
}

export interface JournalFilter {
  cats?: JournalCat[];
  levels?: Severity[];
  incident?: string;
}

export interface Toast {
  id: string;
  severity: Severity;
  title: string;
  body: string;
  actions: { id: string; label: string }[];
  sticky: boolean;
  close: boolean;
}

export interface Connection {
  id: string;
  process: string;
  host: string;
  dest: string;
  route: 'vpn' | 'direct';
  chain: string[];
  /** Bytes since the connection opened. */
  up: number;
  down: number;
  started: string;
}

export type Topic = 'state' | 'journal' | 'traffic' | 'connections' | 'toast' | 'log' | 'diag' | 'speed';

export type DiagStepName = 'network' | 'internet' | 'dns' | 'engine' | 'server' | 'site';

/** One step of the connection check, as it goes and as it ended. */
export interface DiagStep {
  step: DiagStepName;
  status: 'running' | 'ok' | 'fail' | 'skip';
  text: string;
  advice: string | null;
}

export interface LegacyImportResult {
  subs: number;
  servers: number;
  rules: number;
  selected: string | null;
  mode: Mode;
  /** Names of subscriptions that did not download. */
  failed: string[];
}

/** An old Strelka found on the machine (legacy.detect). */
export interface LegacyFolder {
  dir: string;
  /** Its own VPN is up: the new one must not be started next to it. */
  running: boolean;
  /** The enabled logon task starts it. */
  autostart: boolean;
  /** Names of its subscriptions, never the links; [''] for a lone unnamed one. */
  subs: string[];
}

export interface LegacyRetireResult {
  /** How many of its programs were stopped. */
  stopped: number;
  task: 'disabled' | 'absent';
}

export type RuleListName = 'vpn' | 'direct';
export type RuleKind = 'process' | 'suffix' | 'domain' | 'keyword' | 'ip';

/** One direction's rules; every array is present. */
export interface RuleList {
  processes: string[];
  domains: string[];
  suffixes: string[];
  keywords: string[];
  ips: string[];
}

/** A list the engine keeps up to date by itself (blocked sites). */
export interface AutoList {
  tag: string;
  count: number;
  updatedAt: string | null;
}

export interface Rules {
  vpn: RuleList;
  direct: RuleList;
  auto: AutoList[];
}

export interface ProcessInfo {
  name: string;
  path: string;
}

export interface EngineVersion {
  name: 'sing-box' | 'xray';
  version: string | null;
}

/** What the application may send as incident.action. */
export type IncidentAction = `switch:${string}` | 'retry' | 'update_subs' | 'release';

export const ErrorCode = {
  methodNotFound: -32601,
  invalidParams: -32602,
  busy: 1001,
  noServers: 1002,
  notFound: 1003,
  invalid: 1004,
  fetchFailed: 1005,
  engine: 1006,
  unsupported: 1007,
} as const;

type Empty = Record<string, never>;

/** Methods of the daemon the application calls. */
export interface Methods {
  hello: {
    params: { client: 'app' | 'ctl'; version: string; proto: number };
    result: { version: string; proto: number; os: string };
  };
  'state.get': { params: undefined; result: State };
  'state.subscribe': { params: { topics: Topic[] }; result: Empty };
  'state.unsubscribe': { params: { topics: Topic[] }; result: Empty };
  'vpn.start': { params: undefined; result: Empty };
  'vpn.stop': { params: undefined; result: Empty };
  'vpn.restart': { params: undefined; result: Empty };
  'mode.set': { params: { mode: Mode }; result: Empty };
  'server.select': { params: { id: string }; result: Empty };
  'settings.set': { params: { patch: Partial<Settings> }; result: Empty };
  'incident.action': { params: { id: string; action: IncidentAction }; result: Empty };
  'journal.list': {
    params: { before?: number; limit?: number; filter?: JournalFilter };
    result: { entries: JournalEntry[]; more: boolean };
  };
  /** ids are tested in this order; without them every server, in the list's order. */
  'server.test': { params: { ids?: string[] }; result: Empty };
  /** Stops a speed test too. */
  'server.testStop': { params: undefined; result: Empty };
  /** One server at a time, at most 12; without ids the selected one. */
  'server.speedTest': { params: { ids?: string[] }; result: Empty };
  'server.favorite': { params: { id: string; favorite: boolean }; result: Empty };
  'server.reorder': { params: { ids: string[] }; result: Empty };
  'server.remove': { params: { id: string }; result: Empty };
  'subs.add': { params: { url: string; name?: string }; result: { id: string; servers: number } };
  'subs.addLink': { params: { link: string }; result: { id: string } };
  'subs.rename': { params: { id: string; name: string }; result: Empty };
  'subs.remove': { params: { id: string }; result: Empty };
  'subs.update': { params: { id?: string }; result: Empty };
  'rules.get': { params: undefined; result: Rules };
  'rules.add': {
    params: { list: RuleListName; value: string; kind?: RuleKind };
    result: { kind: RuleKind; value: string };
  };
  'rules.remove': { params: { list: RuleListName; kind: RuleKind; value: string }; result: Empty };
  'rules.check': { params: { target: string }; result: { route: 'vpn' | 'direct'; because: string } };
  'rules.processes': { params: undefined; result: { processes: ProcessInfo[] } };
  'diag.export': { params: undefined; result: { path: string } };
  'engine.versions': { params: undefined; result: { engines: EngineVersion[] } };
  'place.detect': { params: undefined; result: Place };
  'place.set': { params: { lat: number; lon: number; city: string; country?: string }; result: Empty };
  'diag.run': { params: undefined; result: { steps: DiagStep[] } };
  /** found: every old Strelka, the preferred first; dir and running repeat the first. */
  'legacy.detect': { params: undefined; result: { dir: string | null; running: boolean; found: LegacyFolder[] } };
  'legacy.import': { params: { dir: string }; result: LegacyImportResult };
  'legacy.retire': { params: undefined; result: LegacyRetireResult };
}

export type MethodName = keyof Methods;

/** Notifications of the daemon, by topic. */
export interface Notifications {
  state: State;
  journal: JournalEntry;
  traffic: { t: string; up: number; down: number };
  connections: Connection[];
  toast: Toast;
  log: { t: string; source: 'sing-box' | 'xray' | 'strelkad'; level: string; line: string };
  diag: DiagStep;
  speed: SpeedSample;
}

export type NotificationName = keyof Notifications;
