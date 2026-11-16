import type { MethodName, Methods, NotificationName, Notifications } from '../proto';

/** The link to the daemon, not the state of the VPN. */
export type LinkStatus = 'connecting' | 'connected' | 'disconnected';

export type Unsubscribe = () => void;

/**
 * How the interface talks to the daemon. Two implementations: the bridge of
 * the real window and a fake daemon in memory, so that every screen can be
 * opened in a plain browser.
 */
export interface Transport {
  call<M extends MethodName>(method: M, params?: Methods[M]['params']): Promise<Methods[M]['result']>;
  on<N extends NotificationName>(name: N, handler: (params: Notifications[N]) => void): Unsubscribe;
  status(): LinkStatus;
  onStatus(handler: (status: LinkStatus) => void): Unsubscribe;
  /** Try to reach the daemon now instead of waiting out the pause. */
  retry(): void;
}

/** An error answered by the daemon; `message` is a ready text for the user. */
export class RpcError extends Error {
  readonly code: number;
  readonly data: unknown;

  constructor(code: number, message: string, data?: unknown) {
    super(message);
    this.name = 'RpcError';
    this.code = code;
    this.data = data;
  }
}

/** The request never reached the daemon or the answer never came back. */
export class LinkError extends Error {
  constructor(message = 'link') {
    super(message);
    this.name = 'LinkError';
  }
}

/** Fan-out of notifications and status changes, shared by both transports. */
export class Listeners {
  #byName = new Map<string, Set<(params: never) => void>>();
  #status = new Set<(status: LinkStatus) => void>();

  on<N extends NotificationName>(name: N, handler: (params: Notifications[N]) => void): Unsubscribe {
    let set = this.#byName.get(name);
    if (!set) this.#byName.set(name, (set = new Set()));
    set.add(handler as (params: never) => void);
    return () => set.delete(handler as (params: never) => void);
  }

  onStatus(handler: (status: LinkStatus) => void): Unsubscribe {
    this.#status.add(handler);
    return () => this.#status.delete(handler);
  }

  emit(name: string, params: unknown): void {
    for (const handler of this.#byName.get(name) ?? []) handler(params as never);
  }

  emitStatus(status: LinkStatus): void {
    for (const handler of this.#status) handler(status);
  }
}
