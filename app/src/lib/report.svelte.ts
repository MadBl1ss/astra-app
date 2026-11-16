import { daemon } from './store.svelte';
import { inWindow } from './transport';

/**
 * The report for support. The daemon writes the archive into its own data
 * folder; the window shows that file in the file manager, because a user
 * asked for "the logs" needs a file to attach, not a path to retype.
 * Journal and Settings both offer it and share this one state.
 */
class Report {
  busy = $state(false);
  /** Where the last archive was written, while it is worth showing. */
  path = $state<string | null>(null);

  async make(): Promise<void> {
    if (this.busy) return;
    this.busy = true;
    try {
      const { path } = await daemon.call('diag.export');
      this.path = path;
      await this.reveal();
    } catch (err) {
      daemon.say((err as Error).message);
    } finally {
      this.busy = false;
    }
  }

  async reveal(): Promise<void> {
    // A browser tab has no file manager to show; the path is on screen.
    if (!this.path || !inWindow) return;
    try {
      const { revealItemInDir } = await import('@tauri-apps/plugin-opener');
      await revealItemInDir(this.path);
    } catch {
      // The path stays on screen; nothing else to do.
    }
  }

  dismiss(): void {
    this.path = null;
  }
}

export const report = new Report();

/** The file name of a path, whatever the separators. */
export function fileName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path;
}
