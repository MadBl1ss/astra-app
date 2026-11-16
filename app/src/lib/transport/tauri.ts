import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import type { MethodName, Methods, NotificationName, Notifications } from '../proto';
import { LinkError, Listeners, RpcError, type LinkStatus, type Transport, type Unsubscribe } from './types';

// subs.add may take 30 s per subscription; nothing the window calls is slower.
const TIMEOUT_MS = 90_000;

interface Pending {
  resolve: (value: never) => void;
  reject: (reason: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

interface Wire {
  id?: string | number | null;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

/**
 * The real daemon behind the Rust bridge. The bridge moves lines of JSON and
 * reconnects by itself; matching answers to requests is done here.
 */
export class TauriTransport implements Transport {
  #listeners = new Listeners();
  #pending = new Map<string, Pending>();
  #status: LinkStatus = 'connecting';
  #seq = 0;
  // The bridge outlives a reload of the page and may still deliver answers to
  // the previous page's requests: ids of each page load are its own.
  #session = Math.random().toString(36).slice(2, 8);

  constructor() {
    void this.#attach();
  }

  async #attach(): Promise<void> {
    await listen<string>('daemon://line', (event) => this.#onLine(event.payload));
    await listen<LinkStatus>('daemon://status', (event) => this.#setStatus(event.payload));
    // The bridge may have connected before anyone was listening.
    this.#setStatus(await invoke<LinkStatus>('daemon_status'));
  }

  #setStatus(status: LinkStatus): void {
    if (status === this.#status) return;
    this.#status = status;
    if (status !== 'connected') this.#failAll();
    this.#listeners.emitStatus(status);
  }

  #failAll(): void {
    for (const [id, pending] of this.#pending) {
      clearTimeout(pending.timer);
      pending.reject(new LinkError());
      this.#pending.delete(id);
    }
  }

  #onLine(line: string): void {
    let msg: Wire;
    try {
      msg = JSON.parse(line) as Wire;
    } catch {
      return;
    }
    if (msg.method !== undefined && (msg.id === undefined || msg.id === null)) {
      this.#listeners.emit(msg.method, msg.params);
      return;
    }
    const pending = this.#pending.get(String(msg.id));
    if (!pending) return;
    this.#pending.delete(String(msg.id));
    clearTimeout(pending.timer);
    if (msg.error) pending.reject(new RpcError(msg.error.code, msg.error.message, msg.error.data));
    else pending.resolve(msg.result as never);
  }

  call<M extends MethodName>(method: M, params?: Methods[M]['params']): Promise<Methods[M]['result']> {
    const id = `${this.#session}-${++this.#seq}`;
    const line = JSON.stringify({ jsonrpc: '2.0', id, method, params: params ?? {} });
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new LinkError('timeout'));
      }, TIMEOUT_MS);
      this.#pending.set(id, { resolve, reject, timer });
      invoke('daemon_send', { line }).catch(() => {
        if (!this.#pending.delete(id)) return;
        clearTimeout(timer);
        reject(new LinkError());
      });
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
    void invoke('daemon_retry');
  }
}
