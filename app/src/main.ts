import { mount } from 'svelte';
import App from './App.svelte';
import './lib/styles/base.css';
import { daemon } from './lib/store.svelte';
import './lib/theme.svelte';
import { createTransport, inWindow } from './lib/transport';

daemon.attach(await createTransport());

if (inWindow) {
  // The tray and autostart exist only in the real window; a browser tab with
  // the mock daemon never loads this code.
  const shell = await import('./lib/shell/native.svelte');
  shell.start();
}

mount(App, { target: document.getElementById('app')! });
