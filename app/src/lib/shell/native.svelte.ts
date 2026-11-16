// What the window does outside its own page: keeps the tray icon in step
// with the daemon's state and registers itself to start at login. Rust owns
// the tray and knows nothing about the protocol; it is told an icon name and
// ready texts.

import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { ru } from '../i18n/ru';
import { daemon } from '../store.svelte';
import { describe, displayName, selectedServer, type TrayIcon } from '../view';

interface TrayView {
  icon: TrayIcon;
  tooltip: string;
  toggleLabel: string;
  canToggle: boolean;
}

const PULSE_MS = 700;
const AUTOSTART_KEY = 'strelka.autostart.offered';

function trayView(): TrayView {
  const s = daemon.state;
  if (daemon.link !== 'connected' || !s || daemon.outdated) {
    return { icon: 'problem', tooltip: ru.tray.noDaemon, toggleLabel: ru.tray.turnOn, canToggle: false };
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
  };
}

function push(view: TrayView, frame: string): void {
  void invoke('tray_set', {
    icon: frame,
    tooltip: view.tooltip,
    labels: { open: ru.tray.open, toggle: view.toggleLabel, quit: ru.tray.quit },
    canToggle: view.canToggle,
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

export function start(): void {
  $effect.root(watchTray);
  void listen('tray://toggle', () => {
    if (daemon.ready) void daemon.toggleVpn();
  });
  void offerAutostart();
}
