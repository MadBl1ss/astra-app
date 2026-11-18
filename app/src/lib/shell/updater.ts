// The real source of updates: Tauri's updater plugin, reading latest.json of
// the newest GitHub release (plugins.updater in tauri.conf.json). The plugin
// checks the package against the public key there before installing it.

import type { Update } from '@tauri-apps/plugin-updater';
import type { UpdateInfo, UpdateSource } from '../update.svelte';

export function pluginSource(): UpdateSource {
  let pending: Update | null = null;
  return {
    async check(): Promise<UpdateInfo | null> {
      const { check } = await import('@tauri-apps/plugin-updater');
      const found = await check({ timeout: 30_000 });
      // The previous offer holds a resource on the Rust side.
      if (pending && pending !== found) void pending.close().catch(() => {});
      pending = found;
      if (!found) return null;
      return { version: found.version, date: found.date ?? null, notes: found.body?.trim() ?? '' };
    },
    async download(progress, downloaded): Promise<void> {
      if (!pending) throw new Error('nothing to download');
      let got = 0;
      let total: number | null = null;
      await pending.download((event) => {
        switch (event.event) {
          case 'Started':
            total = event.data.contentLength ?? null;
            progress(0, total);
            break;
          case 'Progress':
            got += event.data.chunkLength;
            progress(got, total);
            break;
          case 'Finished':
            // The bytes are in; the signature is checked before download()
            // resolves.
            downloaded();
            break;
        }
      });
    },
    async install(): Promise<void> {
      if (!pending) throw new Error('nothing to install');
      await pending.install();
    },
  };
}
