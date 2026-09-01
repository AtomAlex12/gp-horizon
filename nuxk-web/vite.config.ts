import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// mode "lite" → the router build: no history/graphs/multi-controller.
export default defineConfig(({ mode }) => ({
  plugins: [svelte()],
  define: { __LITE__: JSON.stringify(mode === 'lite') },
  build: { target: 'es2020' },
  server: {
    // Same-origin dev: proxy /api to a locally running nuxk-core.
    // To hit a remote controller instead, set VITE_API_BASE in .env — the
    // api client then calls it directly (this proxy is bypassed).
    proxy: {
      '/api': { target: 'http://127.0.0.1:4141', changeOrigin: true },
    },
  },
}));
