# nuxk-web

The GP Horizon UI. **Svelte** SPA, talks only to `nuxk-core` `/api/v1`.

Two builds from one codebase:

| Build | Where | Contents | Size target |
|---|---|---|---|
| `full` | Docker / RPi / any host | status, live graphs (uPlot), history, list editors, presets, per-domain discovery view, multi-controller | — |
| `lite` | the router (`opkg`), served by `nuxk-core -web` | status, controls, list editor, presets, log tail — **no** history / graphs / multi-controller | ~30–40 KB gz |

`lite` is a build flag, not a fork: history routes and heavy deps are behind
`import.meta.env.MODE === 'lite'` guards + dynamic imports, tree-shaken out.

## Scaffold (to do)

```sh
npm create vite@latest . -- --template svelte-ts
npm i
npm i -D uplot                 # graphs, full only
```

```jsonc
// package.json scripts
{
  "dev":       "vite",
  "build":     "vite build",                       // → dist/      (full)
  "build:lite": "vite build --mode lite --outDir dist-lite"
}
```

```ts
// vite.config.ts — lite drops history + graphs
export default defineConfig(({ mode }) => ({
  plugins: [svelte()],
  define: { __LITE__: mode === 'lite' },
  build: { target: 'es2020', cssMinify: 'lightningcss' },
}))
```

## Structure (planned)

```
src/
  lib/api.ts          typed client for /api/v1  (stub committed)
  lib/stores.ts       reactive state: status poll, SSE events
  routes/
    Dashboard.svelte  engine cards, plane status, traffic
    Lists.svelte      per-engine list editor + presets
    Discover.svelte   per-domain ladder sweep view
    History.svelte    graphs — full only (dynamic import)
  App.svelte
```

## Dev against a real router

```sh
VITE_API_BASE=http://192.168.1.1:4141 VITE_API_TOKEN=... npm run dev
```
