// The real source of updates: the shell's update_* commands (src-tauri/src/
// update.rs) around Tauri's updater plugin, which reads latest.json of the
// newest GitHub release and checks the package against the public key in
// tauri.conf.json. The page does not call the plugin itself: its install
// tears the window down before the UAC prompt is answered (see update.rs).

import { Channel, invoke } from '@tauri-apps/api/core';
import type { UpdateInfo, UpdateSource } from '../update.svelte';

type Progress = { event: 'chunk'; got: number; total: number | null } | { event: 'downloaded' };

export function shellSource(): UpdateSource {
  return {
    check: () => invoke<UpdateInfo | null>('update_check'),
    async download(progress, downloaded): Promise<void> {
      const channel = new Channel<Progress>();
      channel.onmessage = (m) => {
        if (m.event === 'chunk') progress(m.got, m.total);
        else downloaded();
      };
      await invoke('update_download', { progress: channel });
    },
    install: () => invoke('update_install'),
  };
}
