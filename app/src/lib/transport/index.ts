import { isTauri } from '@tauri-apps/api/core';
import type { Transport } from './types';

export * from './types';

/** True in the real window, false in a browser tab. */
export const inWindow = isTauri();

/**
 * The real daemon in the window, the fake one in a browser. `?mock=<name>`
 * forces the fake one anywhere, so a scenario can be looked at in the real
 * window too; `&still` freezes its numbers for screenshots, `&legacy=` picks
 * the old Strelkas of a fresh profile (see MockTransport).
 */
export async function createTransport(): Promise<Transport> {
  const query = new URLSearchParams(location.search);
  const scenario = query.get('mock');
  if (scenario !== null || !inWindow) {
    const { MockTransport } = await import('./mock');
    return new MockTransport(scenario || 'ok', { still: query.has('still'), legacy: query.get('legacy') });
  }
  const { TauriTransport } = await import('./tauri');
  return new TauriTransport();
}
