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
  const [{ default: App }, { daemon }, { toasts }, { createTransport, inWindow }, , { updates }, { app }] = await Promise.all([
    import('./App.svelte'),
    import('./lib/store.svelte'),
    import('./lib/toasts.svelte'),
    import('./lib/transport'),
    import('./lib/theme.svelte'),
    import('./lib/update.svelte'),
    import('./lib/version'),
  ]);
  const query = new URLSearchParams(location.search);
  if (inWindow) {
    // The build's own version: a test build is stamped with another one
    // than package.json says.
    const { getVersion } = await import('@tauri-apps/api/app');
    app.version = await getVersion().catch(() => app.version);
  }
  daemon.onToast((t) => toasts.receive(t));
  daemon.attach(await createTransport());

  // Updates: the plugin in an installed build; `?update[=kind]` shows the
  // made-up one of transport/mockupdate.ts anywhere. A development build
  // looks for nothing - it is not what the releases replace.
  const fakeUpdate = query.get('update');
  if (fakeUpdate !== null) {
    const { mockSource, seedRestart } = await import('./lib/transport/mockupdate');
    seedRestart(fakeUpdate, app.version);
    updates.attach(mockSource(fakeUpdate), true);
  } else if (inWindow && !import.meta.env.DEV) {
    const { pluginSource } = await import('./lib/shell/updater');
    updates.attach(pluginSource(), false);
  }

  if (inWindow) {
    // The tray, autostart and the toast window exist only in the real
    // window; a browser tab with the mock daemon never loads this code.
    const shell = await import('./lib/shell/native.svelte');
    shell.start();
  }

  mount(App, { target });
}
