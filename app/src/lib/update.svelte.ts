// Updates of the app, from GitHub releases through Tauri's updater plugin.
//
// The window looks for a new version a while after it starts and every few
// hours after that, and whenever the user asks in Settings. A new one puts
// the "Обновить" item over Settings and opens a dialog once per version, the
// first time the window is in sight; asked for, it opens at once.
// Installing stops the service for a minute (the installer replaces it),
// so the VPN and the guard are down meanwhile, and Windows closes the app;
// the installer starts it again. The next start says how it went.

import { ru } from './i18n/ru';
import { app } from './version';

export interface UpdateInfo {
  version: string;
  /** RFC 3339, when the release says. */
  date: string | null;
  notes: string;
}

/** Where updates come from: the plugin in the window, a fake in a browser. */
export interface UpdateSource {
  check(): Promise<UpdateInfo | null>;
  /** Resolves once the package is downloaded and its signature checked. */
  download(progress: (got: number, total: number | null) => void, downloaded: () => void): Promise<void>;
  /** On Windows the app exits here when the installer has started. */
  install(): Promise<void>;
}

/** How the last look for a new version ended. */
export interface LastCheck {
  /** Epoch ms. */
  at: number;
  /** Words for the user when it failed. */
  error: string | null;
}

export type UpdatePhase =
  | 'offer'
  | 'download'
  | 'verify'
  | 'install'
  | 'error'
  /** The app was restarted by the installer with the new version. */
  | 'done'
  /** The app was restarted, but with the old version: the installer did not finish. */
  | 'missed';

const FIRST_CHECK_MS = 8_000;
const CHECK_EVERY_MS = 6 * 60 * 60 * 1000;
const SHOWN_KEY = 'strelka.update.shown';
const PENDING_KEY = 'strelka.update.pending';

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, value);
  } catch {
    // Without storage the dialog may open twice; nothing worse.
  }
}

/** Turns what the plugin throws into words for the user. */
export function explain(err: unknown, stage: 'check' | 'download' | 'install'): string {
  const raw = String(err instanceof Error ? err.message : err).toLowerCase();
  const t = ru.update.errors;
  if (stage === 'install') {
    // ShellExecute's answer when the UAC prompt was declined.
    if (raw.includes('os error 5') || raw.includes('denied') || raw.includes('отказано')) return t.uac;
    return t.install;
  }
  if (raw.includes('signature') || raw.includes('minisign') || raw.includes('подпис')) return t.signature;
  return stage === 'check' ? t.check : t.network;
}

class Updates {
  /** The newer version on offer; null when there is none or nobody asked yet. */
  available = $state.raw<UpdateInfo | null>(null);
  /** The dialog is open. */
  open = $state(false);
  phase = $state<UpdatePhase>('offer');
  got = $state(0);
  total = $state<number | null>(null);
  error = $state<string | null>(null);
  checking = $state(false);
  /** Updates come to this window at all: a development build gets none. */
  attached = $state(false);
  lastCheck = $state.raw<LastCheck | null>(null);
  /** The version the last start found itself updated to, for the 'done' line. */
  installed = $state<string | null>(null);

  #source: UpdateSource | null = null;
  #visible = false;

  /**
   * Starts looking for updates. `visible` tells whether the window is in
   * sight; the dialog never opens itself into a hidden window.
   */
  attach(source: UpdateSource, visible: boolean): void {
    this.#source = source;
    this.#visible = visible;
    this.attached = true;
    this.#afterRestart();
    // After a missed update the retry button needs the offer at once.
    setTimeout(() => void this.check(), this.phase === 'missed' ? 0 : FIRST_CHECK_MS);
    setInterval(() => void this.check(), CHECK_EVERY_MS);
  }

  /** The window was shown or hidden. */
  visible(shown: boolean): void {
    this.#visible = shown;
    if (shown) this.#offer();
  }

  /**
   * Looks once. In the background a failure stays quiet (no network, GitHub
   * down: next time) and only Settings shows it under the button; `asked`
   * is the button itself, and a new version found then opens at once.
   */
  async check(asked = false): Promise<void> {
    if (!this.#source || this.checking || this.#busy()) return;
    this.checking = true;
    let error: string | null = null;
    try {
      this.available = await this.#source.check();
    } catch (err) {
      error = explain(err, 'check');
    } finally {
      this.checking = false;
    }
    this.lastCheck = { at: Date.now(), error };
    const v = this.available?.version;
    if (asked && v && !error) {
      write(SHOWN_KEY, v);
      this.show();
      return;
    }
    this.#offer();
  }

  /** The item over Settings, or the button on the "update" screen. */
  show(): void {
    if (!this.#busy() && this.phase !== 'error') this.phase = 'offer';
    this.open = true;
  }

  close(): void {
    // Once the download has started there is no stopping it half-way;
    // the dialog stays until it ends one way or the other.
    if (this.#busy()) return;
    this.open = false;
    if (this.phase === 'done' || this.phase === 'missed') this.phase = 'offer';
  }

  async install(): Promise<void> {
    const source = this.#source;
    const target = this.available;
    if (!source || !target || this.#busy()) return;
    this.error = null;
    this.got = 0;
    this.total = null;
    this.phase = 'download';
    try {
      await source.download(
        (got, total) => {
          this.got = got;
          this.total = total;
        },
        () => (this.phase = 'verify'),
      );
    } catch (err) {
      this.#fail(explain(err, 'download'));
      return;
    }
    this.phase = 'install';
    // Remembered before the app goes: the next start compares.
    write(PENDING_KEY, target.version);
    try {
      await source.install();
    } catch (err) {
      write(PENDING_KEY, null);
      this.#fail(explain(err, 'install'));
    }
  }

  #fail(text: string): void {
    this.error = text;
    this.phase = 'error';
    this.open = true;
  }

  #busy(): boolean {
    return this.phase === 'download' || this.phase === 'verify' || this.phase === 'install';
  }

  /** Opens the dialog by itself once per version, and only in sight. */
  #offer(): void {
    const v = this.available?.version;
    if (!v || !this.#visible || this.open || read(SHOWN_KEY) === v) return;
    write(SHOWN_KEY, v);
    this.phase = 'offer';
    this.open = true;
  }

  /** Says how the update the last run started has gone. */
  #afterRestart(): void {
    const pending = read(PENDING_KEY);
    if (!pending) return;
    write(PENDING_KEY, null);
    this.installed = pending;
    this.phase = pending === app.version ? 'done' : 'missed';
    this.open = true;
  }
}

export const updates = new Updates();
