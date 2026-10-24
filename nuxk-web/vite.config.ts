import { readFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import pkg from './package.json';

// The licences of what the bundle carries, next to it: MIT asks for its notice
// with every copy. The lite build carries no gp-ui.
const notices = (lite: boolean): Plugin => ({
  name: 'third-party-notices',
  generateBundle() {
    const parts: [string, string][] = [['svelte', 'node_modules/svelte/LICENSE.md']];
    if (!lite) parts.push(['@gp-plane/gp-ui', 'node_modules/@gp-plane/gp-ui/LICENSE']);
    const source = parts.map(([name, file]) => `${name}\n\n${readFileSync(file, 'utf8').trim()}\n`).join('\n---\n\n');
    this.emitFile({ type: 'asset', fileName: 'THIRD-PARTY-NOTICES.txt', source });
  },
});

// mode "lite" → the router build: no history/graphs/multi-controller.
export default defineConfig(({ mode }) => ({
  plugins: [svelte(), notices(mode === 'lite')],
  // __APP_VERSION__ comes from package.json, which `make version-check` keeps
  // equal to the repo-root VERSION file.
  define: { __LITE__: JSON.stringify(mode === 'lite'), __APP_VERSION__: JSON.stringify(pkg.version) },
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
