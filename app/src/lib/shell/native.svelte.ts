// What the window does outside its own page: keeps the tray icon in step
// with the daemon's state and registers itself to start at login. Rust owns
// the tray and knows nothing about the protocol; it is told an icon name and
// ready texts.

import { invoke } from '@tauri-apps/api/core';
import { emit, listen } from '@tauri-apps/api/event';
import { ru } from '../i18n/ru';
import { daemon } from '../store.svelte';
import { theme } from '../theme.svelte';
import { toasts } from '../toasts.svelte';
import { describe, displayName, selectedServer, type TrayIcon } from '../view';
import { TOASTS_EVENTS, type ActionPayload, type ShowPayload } from './toastwire';
import { win } from './window.svelte';

interface TrayServer {
  id: string;
  label: string;
  checked: boolean;
}

interface TrayView {
  icon: TrayIcon;
  tooltip: string;
  toggleLabel: string;
  canToggle: boolean;
  /** The favourites, in the user's order: the "Сервер" submenu. */
  servers: TrayServer[];
}

function trayServers(): TrayServer[] {
  const s = daemon.state;
  if (!s) return [];
  return s.servers
    .filter((x) => x.favorite)
    .sort((a, b) => a.order - b.order)
    .map((x) => ({
      id: x.id,
      // Menus draw no flags: the name alone, and the last ping if known.
      label: x.ping !== null && x.alive ? `${displayName(x)} · ${ru.units.ms(x.ping)}` : displayName(x),
      checked: x.id === s.server.selected,
    }));
}

const PULSE_MS = 700;
const AUTOSTART_KEY = 'strelka.autostart.offered';

function trayView(): TrayView {
  const s = daemon.state;
  if (daemon.link !== 'connected' || !s || daemon.outdated) {
    return { icon: 'problem', tooltip: ru.tray.noDaemon, toggleLabel: ru.tray.turnOn, canToggle: false, servers: [] };
  }
  // The clock is not read here on purpose: the tooltip carries no durations,
  // and the tray must not be redrawn every second.
  const picture = describe(s, Date.now());
  const server = selectedServer(s);
  const detail =
    s.vpn.phase === 'off' || !server
      ? null
      : [displayName(server), s.server.ping !== null && picture.tone === 'ok' ? ru.units.ms(s.server.ping) : null]
          .filter(Boolean)
          .join(' · ');
  return {
    icon: picture.tray,
    tooltip: ru.tray.tooltip(picture.word, detail),
    toggleLabel: picture.wantOn ? ru.tray.turnOff : ru.tray.turnOn,
    canToggle: true,
    servers: trayServers(),
  };
}

function push(view: TrayView, frame: string): void {
  void invoke('tray_set', {
    icon: frame,
    tooltip: view.tooltip,
    labels: {
      open: ru.tray.open,
      toggle: view.toggleLabel,
      server: ru.tray.server,
      noFavorites: ru.tray.noFavorites,
      quit: ru.tray.quit,
    },
    canToggle: view.canToggle,
    servers: view.servers,
  });
}

function watchTray(): void {
  const view = $derived(trayView());
  // State arrives every two seconds with new traffic figures; the tray only
  // cares when something it shows has changed.
  const key = $derived(JSON.stringify(view));

  $effect(() => {
    void key;
    const current = view;
    push(current, current.icon);
    if (current.icon !== 'connecting') return;
    // "Connecting" pulses: two frames of the same icon take turns.
    let dim = false;
    const timer = setInterval(() => {
      dim = !dim;
      push(current, dim ? 'connecting-dim' : 'connecting');
    }, PULSE_MS);
    return () => clearInterval(timer);
  });
}

/**
 * The first run of an installed build registers the window to start hidden
 * at login: the tray icon is how the user sees the VPN. Only once — if the
 * user turns it off later, it stays off. Development builds never do it:
 * a debug binary in the startup list is nobody's wish.
 */
async function offerAutostart(): Promise<void> {
  if (import.meta.env.DEV) return;
  try {
    if (localStorage.getItem(AUTOSTART_KEY)) return;
    const { enable, isEnabled } = await import('@tauri-apps/plugin-autostart');
    if (!(await isEnabled())) await enable();
    localStorage.setItem(AUTOSTART_KEY, '1');
  } catch {
    // Not registered; the next start tries again.
  }
}

async function watchWindow(): Promise<void> {
  await listen<boolean>('window://shown', (event) => {
    win.shown = event.payload;
  });
  // Started with --hidden, the page loads into a window nobody sees.
  const { getCurrentWindow } = await import('@tauri-apps/api/window');
  win.shown = await getCurrentWindow().isVisible();
}

/**
 * Toasts in the app: their own window, or the system's notifications when
 * the user asked for those or the window could not be made. Which one is
 * the shell's question; what to show is decided in toasts.svelte.ts.
 */
async function startToasts(): Promise<void> {
  toasts.showMain = () => void invoke('main_show');
  toasts.screenBusy = () => invoke<boolean>('screen_busy');
  toasts.ownWindow = await invoke<boolean>('toasts_native').catch(() => false);

  const send = () => {
    const payload: ShowPayload = {
      view: toasts.mode === 'window' ? toasts.view : { cards: [], more: 0 },
      theme: theme.resolved,
    };
    void emit(TOASTS_EVENTS.show, payload);
  };
  await listen(TOASTS_EVENTS.ready, send);
  await listen<ActionPayload>(TOASTS_EVENTS.action, ({ payload }) => {
    if (payload.action === 'more') toasts.more();
    else void toasts.act(payload.id, payload.action);
  });
  await listen<boolean>(TOASTS_EVENTS.hover, ({ payload }) => toasts.hover(payload));

  // What the system was already told, by card: a replacement is a new
  // notification there, since it cannot change one in place.
  const told = new Map<string, number>();
  $effect.root(() => {
    $effect(() => {
      toasts.mode = toasts.ownWindow && !toasts.systemPref ? 'window' : 'system';
    });
    $effect(() => {
      void toasts.view;
      void theme.resolved;
      send();
    });
    $effect(() => {
      const cards = toasts.mode === 'system' ? toasts.toasts.shown : [];
      for (const id of [...told.keys()]) if (!cards.some((c) => c.id === id)) told.delete(id);
      for (const c of cards) {
        if (told.get(c.id) === c.rev) continue;
        told.set(c.id, c.rev);
        void notifySystem(c.title, c.body);
      }
    });
  });
}

async function notifySystem(title: string, body: string): Promise<void> {
  try {
    const { isPermissionGranted, requestPermission, sendNotification } = await import('@tauri-apps/plugin-notification');
    if (!(await isPermissionGranted()) && (await requestPermission()) !== 'granted') return;
    sendNotification({ title, body });
  } catch {
    // Nowhere left to say it: the journal and the tray icon have it.
  }
}

export function start(): void {
  $effect.root(watchTray);
  void watchWindow();
  void listen('tray://toggle', () => {
    if (daemon.ready) void daemon.toggleVpn();
  });
  void listen<string>('tray://server', ({ payload }) => {
    if (daemon.ready) void daemon.selectServer(payload);
  });
  void startToasts();
  void offerAutostart();
}
