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

export type Busy = 'starting' | 'stopping' | 'updating_subs' | 'testing';
export type Severity = 'critical' | 'problem' | 'info';

export type IncidentKind =
  | 'server_down'
  | 'all_servers_down'
  | 'engine_failed'
  | 'helper_down'
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
  stats: { avg: number; loss: number; samples: number } | null;
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

export type Topic = 'state' | 'journal' | 'traffic' | 'connections' | 'toast' | 'log';

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

/** Methods of the daemon the application calls at this stage. */
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
}

export type NotificationName = keyof Notifications;
