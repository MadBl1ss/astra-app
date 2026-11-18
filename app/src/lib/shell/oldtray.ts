// The old Strelka's tray icon starts at logon from the user's own Run key,
// which the daemon (running as SYSTEM) does not see; retiring the old
// Strelka is the daemon's legacy.retire plus this. In a browser, and in the
// window with the mock daemon, the entry is made up: clicking through the
// first run on `?mock=empty` must not touch the developer's real one.

import { mock as text } from '../i18n/ru';
import { inWindow } from '../transport';

const real = inWindow && !new URLSearchParams(location.search).has('mock');
let fake: string | null = new URLSearchParams(location.search).get('legacy') === 'none' ? null : text.legacyTray;

/** The command line the old tray starts with at logon, or null. */
export async function oldTrayAutostart(): Promise<string | null> {
  if (!real) return fake;
  const { invoke } = await import('@tauri-apps/api/core');
  return invoke<string | null>('legacy_tray_autostart').catch(() => null);
}

/** Takes the entry off. Throws with the reason when Windows refused. */
export async function oldTrayAutostartOff(): Promise<void> {
  if (!real) {
    fake = null;
    return;
  }
  const { invoke } = await import('@tauri-apps/api/core');
  await invoke<boolean>('legacy_tray_autostart_off');
}
