// The made-up world of the fake daemon: a crowded profile for the "many"
// scenario, rule lists, running programs and the connections they open. It
// is deterministic, so that a scenario looks the same on every load and two
// screenshots can be compared.

import { mock as text } from '../i18n/ru';
import type {
  Connection,
  Incident,
  JournalCat,
  JournalEntry,
  ProcessInfo,
  RuleKind,
  RuleList,
  RuleListName,
  Rules,
  Server,
  Severity,
  State,
  Sub,
} from '../proto';

/** The scenario with 200 servers, 300 connections and a long journal. */
export const MANY = 'many';

/** A small seeded generator (mulberry32). */
export function random(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const iso = (ms: number) => new Date(ms).toISOString();

function flag(cc: string): string {
  return [...cc].map((ch) => String.fromCodePoint(0x1f1e6 + ch.charCodeAt(0) - 65)).join('');
}

const PLACES: readonly [cc: string, ru: string, city: string, base: number][] = [
  ['NL', 'Нидерланды', 'Amsterdam', 55],
  ['DE', 'Германия', 'Frankfurt', 48],
  ['FI', 'Финляндия', 'Helsinki', 38],
  ['SE', 'Швеция', 'Stockholm', 52],
  ['PL', 'Польша', 'Warsaw', 44],
  ['FR', 'Франция', 'Paris', 70],
  ['GB', 'Великобритания', 'London', 78],
  ['US', 'США', 'New York', 165],
  ['TR', 'Турция', 'Istanbul', 95],
  ['KZ', 'Казахстан', 'Almaty', 85],
  ['AT', 'Австрия', 'Vienna', 60],
  ['CH', 'Швейцария', 'Zurich', 66],
  ['LV', 'Латвия', 'Riga', 34],
  ['EE', 'Эстония', 'Tallinn', 31],
  ['JP', 'Япония', 'Tokyo', 290],
  ['SG', 'Сингапур', 'Singapore', 240],
  ['AE', 'ОАЭ', 'Dubai', 150],
  ['RS', 'Сербия', 'Belgrade', 72],
];

const PROTOCOLS: readonly Server['protocol'][] = ['vless', 'vless', 'trojan', 'shadowsocks', 'hysteria2'];

interface SubPlan {
  sub: Sub;
  prefix: string;
  name: (cc: string, ru: string, city: string, k: number) => string;
}

/**
 * Turns a fixture into a crowded profile: five subscriptions (one that did
 * not update, one with no servers at all, one about to run out, one run
 * out), two servers added by link,
 * 200 servers in all, some with very long names.
 */
export function crowd(state: State, now: number): State {
  const rnd = random(20261116);
  const day = 86_400_000;
  const gb = 1024 ** 3;
  const own = state.subs[0]!;
  const plans: SubPlan[] = [
    {
      sub: { ...own, servers: 50 },
      prefix: 'my',
      name: (_cc, ru, _city, k) => `${ru} ${k}`,
    },
    {
      sub: {
        id: 's2',
        name: 'KLEX VPN — премиум-доступ без ограничений по скорости и трафику',
        url: 'https://panel.example.org/…77c1',
        servers: 70,
        updatedAt: iso(now - 40 * 60_000),
        error: null,
        expireAt: iso(now + 2 * day),
        usedBytes: 96 * gb,
        totalBytes: 100 * gb,
      },
      prefix: 'klex',
      name: (cc, _ru, city, k) => `${flag(cc)} [${cc}]⚡ KLEX ${city} — Premium 10 Gbit/s Ultra Low Latency #${k}`,
    },
    {
      sub: {
        id: 's3',
        name: 'Резерв',
        url: 'https://reserve.example.net/…0b2e',
        servers: 60,
        updatedAt: iso(now - 3 * day),
        error: 'сервер подписки ответил 502. Остались серверы с прошлого раза.',
        expireAt: null,
        usedBytes: null,
        totalBytes: null,
      },
      prefix: 'res',
      name: (cc, _ru, city, k) => `${cc}-${k} ${city}`,
    },
    {
      // Carried over from an old install and never alive since: its menu is
      // the only way to remove it.
      sub: {
        id: 's5',
        name: 'port',
        url: 'https://port.example.net/…3c1d',
        servers: 0,
        updatedAt: null,
        error: 'в подписке нет ни одного подходящего сервера',
        expireAt: null,
        usedBytes: null,
        totalBytes: null,
      },
      prefix: 'port',
      name: (cc, _ru, _city, k) => `port-${cc}${k}`,
    },
    {
      sub: {
        id: 's4',
        name: 'lider',
        url: 'https://lider.example.com/…9f00',
        servers: 18,
        updatedAt: iso(now - 6 * 3_600_000),
        error: null,
        expireAt: iso(now - day),
        usedBytes: 12 * gb,
        totalBytes: 50 * gb,
      },
      prefix: 'lider',
      name: (cc, _ru, _city, k) => `lider-${cc}${k}`,
    },
  ];

  const servers: Server[] = [...state.servers];
  for (const plan of plans) {
    const have = servers.filter((x) => x.sub === plan.sub.id).length;
    const seen = new Map<string, number>();
    for (let i = have; i < plan.sub.servers; i++) {
      const place = PLACES[Math.floor(rnd() * PLACES.length)]!;
      const [cc, ru, city, base] = place;
      const k = (seen.get(cc) ?? 0) + 1;
      seen.set(cc, k);
      servers.push(makeServer(rnd, now, plan.sub.id, `${plan.prefix}-${cc}${k}`, plan.name(cc, ru, city, k), cc, base, i));
    }
  }
  servers.push(
    makeServer(rnd, now, '', 'link-1', 'Мой VPS в Хельсинки', 'FI', 36, 1),
    makeServer(rnd, now, '', 'link-2', 'Домашний роутер у родителей (WireGuard через Shadowsocks)', null, 18, 2),
  );

  // Five favourites: the fixture's two, then three from the crowd.
  const extra = ['klex-SE1', 'res-FI1', 'link-1'];
  let order = servers.filter((x) => x.favorite).length;
  for (const id of extra) {
    const x = servers.find((s) => s.id === id);
    if (x && !x.favorite) {
      x.favorite = true;
      x.order = order++;
    }
  }

  const incident: Incident = {
    id: 'i-20261116-2',
    kind: 'sub_expiring',
    severity: 'problem',
    opened: iso(now - 50 * 60_000),
    server: null,
    title: `Подписка «${plans[1]!.sub.name}» скоро закончится`,
    body: 'Осталось 2 дня. Продли её у провайдера, иначе серверы перестанут работать.',
    flaps: 0,
    unstable: false,
  };
  return { ...state, servers, subs: plans.map((p) => p.sub), incidents: [...state.incidents, incident] };
}

/** Servers of a subscription added in the mock, or of a single link. */
export function freshServers(sub: string, prefix: string, n: number, now: number, seed: number): Server[] {
  const rnd = random(seed);
  const out: Server[] = [];
  const seen = new Map<string, number>();
  for (let i = 0; i < n; i++) {
    const [cc, ru, , base] = PLACES[Math.floor(rnd() * PLACES.length)]!;
    const k = (seen.get(cc) ?? 0) + 1;
    seen.set(cc, k);
    out.push(makeServer(rnd, now, sub, `${prefix}-${cc}${k}`, `${ru} ${k}`, cc, base, i));
  }
  return out;
}

function makeServer(
  rnd: () => number,
  now: number,
  sub: string,
  id: string,
  name: string,
  country: string | null,
  base: number,
  i: number,
): Server {
  const protocol = PROTOCOLS[i % PROTOCOLS.length]!;
  const tested = rnd() > 0.12;
  const alive = tested && rnd() > 0.1;
  const slow = rnd() > 0.93 ? 300 : 0;
  const ping = alive ? Math.round(base + rnd() * base * 0.6 + slow) : null;
  const samples = tested ? Math.floor(rnd() * 420) : 0;
  const loss = rnd() > 0.8 ? 0.08 + rnd() * 0.35 : rnd() * 0.04;
  const host = rnd() > 0.7 ? `203.0.113.${(i * 7) % 250}` : `${id.toLowerCase()}.edge.example.net`;
  return {
    id,
    name,
    country,
    sub,
    protocol,
    via: protocol === 'vless' && i % 3 === 0 ? 'xray' : 'sing-box',
    host,
    favorite: false,
    order: 0,
    ping,
    pingAt: tested ? iso(now - Math.floor(rnd() * 30) * 60_000) : null,
    alive,
    stats: samples > 0 ? { avg: Math.round((ping ?? base * 1.4) * 1.08), loss: Math.round(loss * 100) / 100, samples } : null,
  };
}

// ---- rules ---------------------------------------------------------------

const emptyList = (): RuleList => ({ processes: [], domains: [], suffixes: [], keywords: [], ips: [] });

/** The rules a scenario starts with. An empty profile has none. */
export function seedRules(scenario: string, now: number, bare: boolean): Rules {
  const auto = [
    { tag: 'itdog-inside', count: 18_432, updatedAt: iso(now - 2 * 86_400_000) },
    { tag: 'itdog-google-ai', count: 112, updatedAt: iso(now - 2 * 86_400_000) },
  ];
  if (bare) return { vpn: emptyList(), direct: emptyList(), auto };
  const vpn: RuleList = {
    processes: ['Discord.exe', 'Telegram.exe'],
    domains: [],
    suffixes: ['youtube.com', 'instagram.com', 'chatgpt.com', 'x.com'],
    keywords: ['openai'],
    ips: ['91.108.4.0/22'],
  };
  const direct: RuleList = {
    processes: ['steam.exe'],
    domains: ['update.example.org'],
    suffixes: ['sberbank.ru', 'gosuslugi.ru', 'ya.ru'],
    keywords: [],
    ips: [],
  };
  if (scenario === MANY) {
    const rnd = random(7);
    const words = ['stream', 'cloud', 'media', 'news', 'shop', 'play', 'data', 'mail', 'photo', 'chat', 'code', 'docs'];
    // A list holds every value once, as the daemon's lists do.
    const sites = new Set<string>();
    while (sites.size < 110) sites.add(`${words[sites.size % words.length]}${Math.floor(rnd() * 900 + 100)}.example.com`);
    vpn.suffixes.push(...sites);
    vpn.suffixes.push('very-long-subdomain-name.of-some-cdn-provider.example-content-delivery.net');
    vpn.processes.push('Some Very Long Application Name Helper (Renderer).exe', 'obs64.exe', 'Spotify.exe');
    direct.processes.push('EpicGamesLauncher.exe', 'Battle.net.exe');
  }
  return { vpn, direct, auto };
}

/** How the daemon reads a value typed without a kind, and checks it. */
export function normalize(kind: RuleKind | undefined, value: string): { kind: RuleKind; value: string } | string {
  let v = value.trim();
  if (v === '') return text.emptyValue;
  if (!kind) {
    const lv = v.toLowerCase();
    if (lv.endsWith('.exe') || v.includes('\\')) kind = 'process';
    else if (isIp(v)) kind = 'ip';
    else kind = 'suffix';
  }
  switch (kind) {
    case 'process': {
      v = v.slice(Math.max(v.lastIndexOf('\\'), v.lastIndexOf('/')) + 1);
      if (!v.includes('.')) v += '.exe';
      if (/[:*?"<>|]/.test(v)) return text.notProcess(value);
      break;
    }
    case 'domain':
    case 'suffix': {
      v = v.toLowerCase().replace(/^[a-z]+:\/\//, '').replace(/[/?#].*$/, '').replace(/^\*\./, '').replace(/:\d+$/, '');
      if (!v || /[\s/\\:*]/.test(v) || !v.includes('.')) return text.notSite(value);
      break;
    }
    case 'keyword':
      v = v.toLowerCase();
      if (/[\s/\\]/.test(v)) return text.badKeyword;
      break;
    case 'ip':
      if (!isIp(v)) return text.notIp(v);
      if (!v.includes('/')) v += v.includes(':') ? '/128' : '/32';
      break;
  }
  return { kind, value: v };
}

function isIp(v: string): boolean {
  return /^\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?$/.test(v) || /^[0-9a-f:]+:[0-9a-f:]*(\/\d{1,3})?$/i.test(v);
}

export const KEY: Record<RuleKind, keyof RuleList> = {
  process: 'processes',
  domain: 'domains',
  suffix: 'suffixes',
  keyword: 'keywords',
  ip: 'ips',
};

/** The rule of one list that catches a program, a host or an address. */
export function match(list: RuleList, process: string, host: string, ip: string): { kind: RuleKind; value: string } | null {
  const p = process.toLowerCase();
  const hit = list.processes.find((x) => x.toLowerCase() === p);
  if (process && hit) return { kind: 'process', value: hit };
  const h = host.toLowerCase();
  if (h) {
    const domain = list.domains.find((d) => d === h);
    if (domain) return { kind: 'domain', value: domain };
    const suffix = list.suffixes.find((d) => h === d || h.endsWith(`.${d}`));
    if (suffix) return { kind: 'suffix', value: suffix };
    const word = list.keywords.find((k) => h.includes(k));
    if (word) return { kind: 'keyword', value: word };
  }
  const net = ip && list.ips.find((cidr) => inCidr(ip, cidr));
  if (net) return { kind: 'ip', value: net };
  return null;
}

function inCidr(ip: string, cidr: string): boolean {
  const [base, bits] = cidr.split('/');
  const a = ip4(ip);
  const b = ip4(base ?? '');
  if (a === null || b === null) return ip === base;
  const size = 2 ** (32 - Number(bits ?? 32));
  return Math.floor(a / size) === Math.floor(b / size);
}

function ip4(s: string): number | null {
  const parts = s.split('.');
  if (parts.length !== 4) return null;
  let n = 0;
  for (const part of parts) {
    const v = Number(part);
    if (!/^\d+$/.test(part) || v > 255) return null;
    n = n * 256 + v;
  }
  return n;
}

/** Sites the fake auto list holds: what the real one would route itself. */
const AUTO = ['youtube.com', 'googlevideo.com', 'instagram.com', 'cdninstagram.com', 'facebook.com', 'x.com', 'twimg.com', 'discord.com', 'discord.gg', 'discordapp.com', 'discordapp.net', 'linkedin.com', 'chatgpt.com', 'openai.com'];

function inAuto(host: string): boolean {
  const h = host.toLowerCase();
  return AUTO.some((d) => h === d || h.endsWith(`.${d}`));
}

/** Where a connection goes, in the order the engine applies the rules. */
export function routeOf(rules: Rules, global: boolean, process: string, host: string, ip: string): 'vpn' | 'direct' {
  if (match(rules.direct, process, host, ip)) return 'direct';
  if (global || match(rules.vpn, process, host, ip)) return 'vpn';
  return inAuto(host) ? 'vpn' : 'direct';
}

/** The answer of rules.check, worded as the daemon words it. */
export function check(rules: Rules, global: boolean, target: string): { route: 'vpn' | 'direct'; because: string } | string {
  const n = normalize(undefined, target);
  if (typeof n === 'string') return n;
  const { kind, value } = n;
  const process = kind === 'process' ? value : '';
  const host = kind === 'suffix' ? value : '';
  const ip = kind === 'ip' ? value.replace(/\/\d+$/, '') : '';
  if (kind === 'ip' && /^(10\.|192\.168\.|127\.|172\.(1[6-9]|2\d|3[01])\.)/.test(ip)) {
    return { route: 'direct', because: text.checkLocal };
  }
  const d = match(rules.direct, process, host, ip);
  if (d) return { route: 'direct', because: text.checkDirectList(text.kindTitle[d.kind], d.value) };
  if (global) return { route: 'vpn', because: text.checkGlobal };
  const v = match(rules.vpn, process, host, ip);
  if (v) return { route: 'vpn', because: text.checkVpnList(text.kindTitle[v.kind], v.value) };
  if (kind === 'process') return { route: 'direct', because: text.checkProcess };
  if (host && inAuto(host)) return { route: 'vpn', because: text.checkAuto('itdog-inside') };
  return { route: 'direct', because: text.checkNone };
}

export function listTitle(list: RuleListName): string {
  return list === 'vpn' ? text.listVpn : text.listDirect;
}

// ---- programs and connections ---------------------------------------------

interface App {
  name: string;
  path: string;
  hosts: string[];
  weight: number;
}

const APPS: App[] = [
  {
    name: 'chrome.exe',
    path: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    hosts: ['www.youtube.com', 'rr3---sn-4g5e6nsz.googlevideo.com', 'mail.google.com', 'github.com', 'habr.com', 'yandex.ru', 'online.sberbank.ru', 'www.instagram.com', 'chatgpt.com', 'news.example.org'],
    weight: 9,
  },
  { name: 'Discord.exe', path: 'C:\\Users\\user\\AppData\\Local\\Discord\\app-1.0.9170\\Discord.exe', hosts: ['gateway.discord.gg', 'cdn.discordapp.com', 'media.discordapp.net'], weight: 3 },
  { name: 'Telegram.exe', path: 'C:\\Users\\user\\AppData\\Roaming\\Telegram Desktop\\Telegram.exe', hosts: ['149.154.167.51', '91.108.56.130'], weight: 2 },
  { name: 'steam.exe', path: 'C:\\Program Files (x86)\\Steam\\steam.exe', hosts: ['store.steampowered.com', 'cdn.akamai.steamstatic.com', 'api.steampowered.com'], weight: 2 },
  { name: 'Code.exe', path: 'C:\\Users\\user\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe', hosts: ['marketplace.visualstudio.com', 'api.github.com', 'update.code.visualstudio.com'], weight: 2 },
  { name: 'svchost.exe', path: 'C:\\Windows\\System32\\svchost.exe', hosts: ['settings-win.data.microsoft.com', 'ctldl.windowsupdate.com'], weight: 2 },
  { name: 'Spotify.exe', path: 'C:\\Users\\user\\AppData\\Roaming\\Spotify\\Spotify.exe', hosts: ['spclient.wg.spotify.com', 'audio-ak-spotify-com.akamaized.net'], weight: 1 },
  { name: 'firefox.exe', path: 'C:\\Program Files\\Mozilla Firefox\\firefox.exe', hosts: ['www.linkedin.com', 'x.com', 'pbs.twimg.com', 'ru.wikipedia.org'], weight: 3 },
  {
    name: 'Some Very Long Application Name Helper (Renderer).exe',
    path: 'C:\\Program Files\\Some Vendor With A Long Name\\Application\\Some Very Long Application Name Helper (Renderer).exe',
    hosts: ['telemetry.some-vendor-with-a-very-long-domain-name.example.com'],
    weight: 1,
  },
  { name: '', path: '', hosts: ['time.windows.com'], weight: 1 },
];

/** Programs with windows, for "from running". */
export const PROCESSES: ProcessInfo[] = [
  ...APPS.filter((a) => a.name && a.name !== 'svchost.exe').map((a) => ({ name: a.name, path: a.path })),
  { name: 'obs64.exe', path: 'C:\\Program Files\\obs-studio\\bin\\64bit\\obs64.exe' },
  { name: 'EpicGamesLauncher.exe', path: 'C:\\Program Files (x86)\\Epic Games\\Launcher\\Portal\\Binaries\\Win64\\EpicGamesLauncher.exe' },
  { name: 'Battle.net.exe', path: 'C:\\Program Files (x86)\\Battle.net\\Battle.net.exe' },
  { name: 'qbittorrent.exe', path: 'C:\\Program Files\\qBittorrent\\qbittorrent.exe' },
  { name: 'Zoom.exe', path: 'C:\\Users\\user\\AppData\\Roaming\\Zoom\\bin\\Zoom.exe' },
  { name: 'WhatsApp.exe', path: 'C:\\Program Files\\WindowsApps\\WhatsApp\\WhatsApp.exe' },
  { name: 'notepad++.exe', path: 'C:\\Program Files\\Notepad++\\notepad++.exe' },
  { name: 'Figma.exe', path: 'C:\\Users\\user\\AppData\\Local\\Figma\\Figma.exe' },
  { name: 'explorer.exe', path: 'C:\\Windows\\explorer.exe' },
].sort((a, b) => a.name.localeCompare(b.name));

/** Makes up connections: who opens them and where. Ids never repeat. */
export class ConnectionMaker {
  #rnd = random(4242);
  #seq = 0;
  #weights = APPS.reduce((n, a) => n + a.weight, 0);

  make(rules: Rules, global: boolean): Connection {
    let pick = this.#rnd() * this.#weights;
    const app = APPS.find((a) => (pick -= a.weight) < 0) ?? APPS[0]!;
    const host = app.hosts[Math.floor(this.#rnd() * app.hosts.length)]!;
    const numeric = ip4(host) !== null;
    const ip = numeric ? host : `198.51.100.${(hash(host) % 250) + 1}`;
    const route = routeOf(rules, global, app.name, numeric ? '' : host, ip);
    return {
      id: `c${++this.#seq}`,
      process: app.name,
      host: numeric ? '' : host,
      dest: `${ip}:443`,
      route,
      chain: route === 'vpn' ? ['proxy'] : ['direct'],
      up: 0,
      down: 0,
      started: new Date().toISOString(),
    };
  }
}

function hash(s: string): number {
  let h = 0;
  for (const ch of s) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return h;
}

// ---- journal ------------------------------------------------------------

type Add = (t: number, cat: JournalCat, level: Severity, entry: string, incident?: string | null) => void;

/** Six days of a busy life, oldest first, for the "many" scenario. */
export function seedLongJournal(add: Add, servers: Server[], now: number): void {
  const rnd = random(99);
  const name = () => servers[Math.floor(rnd() * servers.length)]!.name.replace(/[\u{1F1E6}-\u{1F1FF}]{2}\s*/gu, '');
  let t = now - 6 * 86_400_000;
  let incident = 0;
  while (t < now - 20 * 60_000) {
    t += Math.floor(rnd() * 25 * 60_000) + 60_000;
    const roll = rnd();
    if (roll < 0.06) {
      // Ids of their own: the scenario's other incidents are i-20261112-* and i-20261116-*.
      const id = `i-20261110-${++incident}`;
      const server = name();
      const minutes = 1 + Math.floor(rnd() * 9);
      add(t, 'server', 'problem', `Сервер ${server} не отвечает. Интернет есть, другие серверы работают.`, id);
      if (rnd() > 0.5) add(t + 40_000, 'server', 'problem', `Сервер ${server} не отвечает. Похоже, сервер выключен или его порт закрыт.`, id);
      add(t + minutes * 60_000, 'server', 'info', `Сервер ${server} снова отвечает. Не работало ${minutes} мин.`, id);
      t += minutes * 60_000;
    } else if (roll < 0.09) {
      // Ids of their own: the scenario's other incidents are i-20261112-* and i-20261116-*.
      const id = `i-20261110-${++incident}`;
      const minutes = 1 + Math.floor(rnd() * 4);
      add(t, 'system', 'problem', 'Нет интернета. VPN заработает, когда сеть вернётся.', id);
      add(t + minutes * 60_000, 'system', 'info', `Интернет вернулся. Его не было ${minutes} мин.`, id);
      t += minutes * 60_000;
    } else if (roll < 0.3) {
      add(t, 'server', 'info', `Выбран сервер ${name()}.`);
    } else if (roll < 0.45) {
      add(t, 'vpn', 'info', 'VPN выключен.');
      add(t + 120_000, 'vpn', 'info', 'VPN включён.');
    } else if (roll < 0.55) {
      add(t, 'rules', 'info', `В список «через VPN» добавлено: site${Math.floor(rnd() * 900)}.example.com.`);
    } else if (roll < 0.62) {
      add(t, 'subs', 'info', `Подписка «Мой провайдер» обновлена: ${40 + Math.floor(rnd() * 20)} серверов.`);
    } else if (roll < 0.66) {
      add(t, 'vpn', 'problem', 'VPN неожиданно остановился. Перезапускаю через 1 с.');
      add(t + 1500, 'vpn', 'info', 'VPN снова работает.');
    } else if (roll < 0.68) {
      add(
        t,
        'subs',
        'problem',
        'Подписка «Резерв» не обновилась: сервер подписки ответил 502 Bad Gateway, а в прошлый раз — превысил время ожидания в 30 секунд. Серверы остались с прошлого раза, они могут не работать, если провайдер их сменил.',
      );
    } else {
      add(t, 'server', 'info', `Проверка серверов: ответили ${150 + Math.floor(rnd() * 40)} из 200.`);
    }
  }
}
