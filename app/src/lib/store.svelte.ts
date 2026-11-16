import { version as APP_VERSION } from '../../package.json';
import { ru } from './i18n/ru';
import {
  PROTO,
  type Connection,
  type IncidentAction,
  type JournalEntry,
  type MethodName,
  type Methods,
  type Mode,
  type State,
} from './proto';
import { RpcError, type LinkStatus, type Transport } from './transport';

/** Speed and connection count of one path of the scheme. */
export interface PathRate {
  conns: number;
  up: number;
  down: number;
}

export interface Rates {
  vpn: PathRate;
  direct: PathRate;
}

// Enough for Home's three lines with room for entries arriving meanwhile.
const JOURNAL_KEEP = 50;
const NOTICE_MS = 6000;

/**
 * Everything the window knows about the daemon. One instance; components
 * read its fields and call its methods, nothing else talks to the transport.
 */
class Daemon {
  /** The last document from the daemon; null until the first one arrives. */
  state = $state.raw<State | null>(null);
  link = $state<LinkStatus>('connecting');
  /** Set when the daemon speaks another protocol version. */
  outdated = $state.raw<{ app: string; daemon: string } | null>(null);
  /** Newest first. */
  journal = $state.raw<JournalEntry[]>([]);
  /** Per-path speeds, known only while somebody watches connections. */
  rates = $state.raw<Rates | null>(null);
  /** The open connections, once a second while somebody watches them. */
  connections = $state.raw<Connection[] | null>(null);
  /** A short message about a failed action; clears itself. */
  notice = $state<string | null>(null);
  /** A clock for "has been on for…" lines; frozen by `?still`. */
  now = $state(Date.now());

  #transport: Transport | null = null;
  #noticeTimer: ReturnType<typeof setTimeout> | undefined;
  #watchers = 0;
  #seen: Map<string, { up: number; down: number }> | null = null;
  #seenAt = 0;

  attach(transport: Transport): void {
    this.#transport = transport;
    transport.on('state', (state) => {
      this.state = state;
    });
    transport.on('journal', (entry) => {
      this.journal = [entry, ...this.journal.filter((e) => e.id !== entry.id)].slice(0, JOURNAL_KEEP);
    });
    transport.on('connections', (list) => this.#measure(list));
    transport.onStatus((status) => this.#onLink(status));
    this.#onLink(transport.status());

    if (!new URLSearchParams(location.search).has('still')) {
      setInterval(() => {
        this.now = Date.now();
      }, 1000);
    }
  }

  /** The daemon is reachable, speaks our protocol and has sent its state. */
  get ready(): boolean {
    return this.link === 'connected' && this.outdated === null && this.state !== null;
  }

  #onLink(status: LinkStatus): void {
    this.link = status;
    if (status === 'connected') void this.#open();
    else this.#forgetRates();
  }

  /** Runs on every (re)connection: subscriptions do not survive the link. */
  async #open(): Promise<void> {
    const transport = this.#transport!;
    try {
      const hello = await transport.call('hello', { client: 'app', version: APP_VERSION, proto: PROTO });
      if (hello.proto !== PROTO) {
        this.outdated = { app: APP_VERSION, daemon: hello.version };
        return;
      }
      this.outdated = null;
      // Subscribing to "state" delivers the current document right away.
      await transport.call('state.subscribe', { topics: ['state', 'journal'] });
      if (this.#watchers > 0) await transport.call('state.subscribe', { topics: ['connections'] });
      const page = await transport.call('journal.list', { limit: JOURNAL_KEEP });
      const known = new Set(page.entries.map((e) => e.id));
      this.journal = [...this.journal.filter((e) => !known.has(e.id)), ...page.entries]
        .sort((a, b) => b.id - a.id)
        .slice(0, JOURNAL_KEEP);
    } catch {
      // The link dropped half-way: the status handler shows that.
    }
  }

  /**
   * Calls the daemon and leaves a refusal to the caller: a dialog shows it
   * under its field, a setting next to its switch. Whatever the failure, it
   * comes out as an RpcError with a text fit for the user.
   */
  async call<M extends MethodName>(method: M, params?: Methods[M]['params']): Promise<Methods[M]['result']> {
    try {
      return await this.#transport!.call(method, params);
    } catch (err) {
      if (err instanceof RpcError) throw err;
      throw new RpcError(0, ru.errors.link);
    }
  }

  /**
   * Calls the daemon. A refusal becomes a notice on screen and `undefined`
   * here, so that callers need no error handling of their own.
   */
  async rpc<M extends MethodName>(method: M, params?: Methods[M]['params']): Promise<Methods[M]['result'] | undefined> {
    try {
      return await this.#transport!.call(method, params);
    } catch (err) {
      this.#say(err instanceof RpcError ? err.message : ru.errors.link);
      return undefined;
    }
  }

  #say(text: string): void {
    this.notice = text;
    clearTimeout(this.#noticeTimer);
    this.#noticeTimer = setTimeout(() => {
      this.notice = null;
    }, NOTICE_MS);
  }

  dismissNotice(): void {
    clearTimeout(this.#noticeTimer);
    this.notice = null;
  }

  retry(): void {
    this.#transport?.retry();
  }

  // ---- what the user can do on Home ----

  toggleVpn(): Promise<unknown> {
    return this.rpc(this.state?.vpn.desired === 'on' ? 'vpn.stop' : 'vpn.start');
  }

  selectServer(id: string): Promise<unknown> {
    return this.rpc('server.select', { id });
  }

  setMode(mode: Mode): Promise<unknown> {
    return this.rpc('mode.set', { mode });
  }

  setGuard(on: boolean): Promise<unknown> {
    return this.rpc('settings.set', { patch: { guard: on } });
  }

  act(incident: string | null, action: IncidentAction): Promise<unknown> {
    // A strip the window made up has no incident; the daemon only needs the
    // id to tell which toast the action came from.
    return this.rpc('incident.action', { id: incident ?? '', action });
  }

  // ---- per-path speeds ----

  /** Says something to the user in the strip at the bottom of the window. */
  say(text: string): void {
    this.#say(text);
  }

  /**
   * Asks for the list of connections while somebody is looking at speeds.
   * The list is the only place where traffic is split by path, and it is a
   * few dozen kilobytes a second, so nobody watching means no subscription.
   */
  watchConnections(): () => void {
    if (++this.#watchers === 1 && this.link === 'connected') {
      void this.#transport?.call('state.subscribe', { topics: ['connections'] }).catch(() => {});
    }
    let done = false;
    return () => {
      if (done) return;
      done = true;
      if (--this.#watchers > 0) return;
      this.#forgetRates();
      if (this.link === 'connected') {
        void this.#transport?.call('state.unsubscribe', { topics: ['connections'] }).catch(() => {});
      }
    };
  }

  #forgetRates(): void {
    this.rates = null;
    this.connections = null;
    this.#seen = null;
  }

  /** Turns two consecutive lists of byte counters into bytes per second. */
  #measure(list: Connection[]): void {
    this.connections = list;
    const at = performance.now();
    const seen = new Map<string, { up: number; down: number }>();
    const rates: Rates = { vpn: { conns: 0, up: 0, down: 0 }, direct: { conns: 0, up: 0, down: 0 } };
    for (const c of list) {
      seen.set(c.id, { up: c.up, down: c.down });
      const path = rates[c.route];
      path.conns++;
      if (!this.#seen) continue;
      // A connection opened since the last look counts with all it has moved.
      const before = this.#seen.get(c.id) ?? { up: 0, down: 0 };
      path.up += Math.max(0, c.up - before.up);
      path.down += Math.max(0, c.down - before.down);
    }
    if (this.#seen) {
      // Lists come once a second, but a sleeping window may skip a few.
      const seconds = Math.min(Math.max((at - this.#seenAt) / 1000, 0.5), 10);
      for (const path of [rates.vpn, rates.direct]) {
        path.up /= seconds;
        path.down /= seconds;
      }
      this.rates = rates;
    }
    this.#seen = seen;
    this.#seenAt = at;
  }
}

export const daemon = new Daemon();
