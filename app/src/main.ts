import { polyfillCountryFlagEmojis } from 'country-flag-emoji-polyfill';
import flagsFont from 'country-flag-emoji-polyfill/dist/TwemojiCountryFlags.woff2?url';
import { mount } from 'svelte';
import App from './App.svelte';
import './lib/styles/base.css';
import { daemon } from './lib/store.svelte';
import './lib/theme.svelte';
import { createTransport, inWindow } from './lib/transport';

// Windows has no glyphs for flag emoji and prints two letters instead. This
// adds a small font that has only the flags, from the app's own files (the
// package's default is a CDN), and only where the system cannot draw them.
polyfillCountryFlagEmojis('Twemoji Country Flags', flagsFont);

daemon.attach(await createTransport());

if (inWindow) {
  // The tray and autostart exist only in the real window; a browser tab with
  // the mock daemon never loads this code.
  const shell = await import('./lib/shell/native.svelte');
  shell.start();
}

mount(App, { target: document.getElementById('app')! });
