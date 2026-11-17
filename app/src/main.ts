import { polyfillCountryFlagEmojis } from 'country-flag-emoji-polyfill';
import flagsFont from 'country-flag-emoji-polyfill/dist/TwemojiCountryFlags.woff2?url';
import { mount } from 'svelte';
import './lib/styles/base.css';

// Windows has no glyphs for flag emoji and prints two letters instead. This
// adds a small font that has only the flags, from the app's own files (the
// package's default is a CDN), and only where the system cannot draw them.
polyfillCountryFlagEmojis('Twemoji Country Flags', flagsFont);

const target = document.getElementById('app')!;

if (new URLSearchParams(location.search).get('window') === 'toasts') {
  // The toast window: it draws what the main page sends and nothing else,
  // so none of the daemon's machinery is loaded into it.
  const { default: ToastWindow } = await import('./lib/shell/ToastWindow.svelte');
  mount(ToastWindow, { target });
} else {
  const [{ default: App }, { daemon }, { toasts }, { createTransport, inWindow }] = await Promise.all([
    import('./App.svelte'),
    import('./lib/store.svelte'),
    import('./lib/toasts.svelte'),
    import('./lib/transport'),
    import('./lib/theme.svelte'),
  ]);
  daemon.onToast((t) => toasts.receive(t));
  daemon.attach(await createTransport());

  if (inWindow) {
    // The tray, autostart and the toast window exist only in the real
    // window; a browser tab with the mock daemon never loads this code.
    const shell = await import('./lib/shell/native.svelte');
    shell.start();
  }

  mount(App, { target });
}
