import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  // Tauri prints its own logs to the same terminal; do not wipe them.
  clearScreen: false,
  server: {
    host: '127.0.0.1',
    port: 1420,
    strictPort: true,
    // The mock daemon loads core/proto/testdata/fixtures/*.json, above app/.
    fs: { allow: ['..'] },
  },
  build: {
    outDir: 'build',
    emptyOutDir: true,
    // WebView2 and WebKitGTK are both evergreen enough for this.
    target: 'es2022',
  },
});
