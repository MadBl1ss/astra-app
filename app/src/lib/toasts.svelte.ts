// The toasts of the main page: it alone listens to the daemon's "toast"
// topic, keeps the list (toasts.ts says what happens to it), runs the clock
// and carries out the buttons. Where the list is drawn is the shell's
// business: a layer over this page in a browser, a window of its own in the
// app, or the system's notifications.

import { ru } from './i18n/ru';
import type { IncidentAction, Toast } from './proto';
import { win } from './shell/window.svelte';
import { daemon } from './store.svelte';
import { EMPTY, dismiss, expire, hover, nextExpiry, receive, release, view, type Toasts, type ToastView } from './toasts';

/** How often a busy screen is looked at again while toasts wait for it. */
const BUSY_POLL_MS = 2000;

// The window's own setting, like the theme: the daemon has nothing to do
// with how the app shows what it says.
const SYSTEM_KEY = 'strelka.systemToasts';

function loadSystemPref(): boolean {
  try {
    return localStorage.getItem(SYSTEM_KEY) === '1';
  } catch {
    return false;
  }
}

export type ToastMode = 'layer' | 'window' | 'system';

class ToastCenter {
  toasts = $state.raw<Toasts>(EMPTY);
  /** Where they are drawn; the shell sets it once it knows. */
  mode = $state<ToastMode>('layer');
  view = $derived<ToastView>(view(this.toasts));
  /** The user wants the system's notifications instead of Strelka's own. */
  systemPref = $state(loadSystemPref());
  /** The app's toast window exists; null in a browser, where there is none. */
  ownWindow = $state<boolean | null>(null);

  setSystemPref(on: boolean): void {
    this.systemPref = on;
    try {
      if (on) localStorage.setItem(SYSTEM_KEY, '1');
      else localStorage.removeItem(SYSTEM_KEY);
    } catch {
      // Not saved; still in effect for this run.
    }
  }

  /** Whether a full-screen program is in front; the shell asks the system. */
  screenBusy: () => Promise<boolean> = async () => false;
  /** Brings the main window up; the shell knows how. */
  showMain: () => void = () => {};

  #timer: ReturnType<typeof setTimeout> | undefined;
  #poll: ReturnType<typeof setInterval> | undefined;
  #queue: Promise<void> = Promise.resolve();
  #local = 0;

  /**
   * A toast from the daemon. Asking the system whether the screen is busy
   * takes a moment, so toasts are taken one after another: a replacement
   * must not overtake the toast it replaces.
   */
  receive(t: Toast): void {
    this.#queue = this.#queue.then(async () => {
      const quiet = daemon.state?.settings.quietFullscreen ?? true;
      const busy = !t.close && t.severity !== 'critical' && quiet && (await this.screenBusy().catch(() => false));
      this.#set(receive(this.toasts, t, Date.now(), busy));
    });
  }

  dismiss(id: string): void {
    this.#set(dismiss(this.toasts, id));
  }

  hover(on: boolean): void {
    this.#set(hover(this.toasts, on, Date.now()));
  }

  more(): void {
    this.showMain();
  }

  /**
   * A button of a toast. "open" and "dismiss" are the window's own; the
   * rest go to the daemon, and the toast leaves: what came of it arrives
   * as the next toast with the same id ("Снова работает").
   */
  async act(id: string, action: string): Promise<void> {
    this.dismiss(id);
    if (action === 'dismiss') return;
    if (action === 'open') {
      this.showMain();
      return;
    }
    try {
      await daemon.call('incident.action', { id, action: action as IncidentAction });
    } catch (err) {
      // An answer to the user's own click: in the window if it is open,
      // otherwise the only place left is another toast.
      const body = (err as Error).message;
      if (win.shown) {
        daemon.say(body);
        return;
      }
      const id = `local-${++this.#local}`;
      this.receive({ id, severity: 'problem', title: ru.toasts.actionFailed, body, actions: [], sticky: false, close: false });
    }
  }

  #set(next: Toasts): void {
    if (next === this.toasts) return;
    this.toasts = next;
    this.#schedule();
    this.#watchScreen();
  }

  /** One timer, for whichever card's time is up first. */
  #schedule(): void {
    clearTimeout(this.#timer);
    const ms = nextExpiry(this.toasts, Date.now());
    if (ms === null) return;
    this.#timer = setTimeout(() => this.#set(expire(this.toasts, Date.now())), ms + 10);
  }

  /** While toasts wait for the end of a game, look at the screen now and then. */
  #watchScreen(): void {
    if (this.toasts.waiting.length === 0) {
      clearInterval(this.#poll);
      this.#poll = undefined;
      return;
    }
    if (this.#poll !== undefined) return;
    this.#poll = setInterval(() => {
      void this.screenBusy()
        .catch(() => false)
        .then((busy) => {
          if (!busy) this.#set(release(this.toasts, Date.now()));
        });
    }, BUSY_POLL_MS);
  }
}

export const toasts = new ToastCenter();
