// A make-believe update for looking at the dialog in a browser:
// `?update` offers one, `?update=fail` loses the network half-way,
// `?update=signature` fails the signature, `?update=uac` is the declined UAC
// prompt, `?update=done` and `?update=missed` are the start after an update.

import { mock as text } from '../i18n/ru';
import type { UpdateInfo, UpdateSource } from '../update.svelte';

const SIZE = 36_400_000;
const STEP_MS = 120;

export function mockSource(kind: string): UpdateSource {
  const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
  return {
    async check(): Promise<UpdateInfo | null> {
      await wait(400);
      return { version: text.updateVersion, date: text.updateDate, notes: text.updateNotes };
    },
    async download(progress, downloaded): Promise<void> {
      progress(0, SIZE);
      for (let got = 0; got < SIZE; ) {
        await wait(STEP_MS);
        got = Math.min(SIZE, got + 1_300_000);
        progress(got, SIZE);
        if (kind === 'fail' && got > SIZE / 2) throw new Error('error sending request: connection reset');
      }
      downloaded();
      await wait(700);
      if (kind === 'signature') throw new Error('The signature verification failed');
    },
    async install(): Promise<void> {
      await wait(900);
      if (kind === 'uac') throw new Error('The operation was canceled by the user. (os error 5) Access is denied.');
      // The real window would be gone by now; the mock just stays here.
      await new Promise(() => {});
    },
  };
}

/** For `?update=done` and `?update=missed`: what the last run left behind. */
export function seedRestart(kind: string, current: string): void {
  try {
    if (kind === 'done') localStorage.setItem('strelka.update.pending', current);
    if (kind === 'missed') localStorage.setItem('strelka.update.pending', text.updateVersion);
  } catch {
    // No storage, no scene.
  }
}
