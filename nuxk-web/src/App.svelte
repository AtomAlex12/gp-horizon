<script lang="ts">
  import { status, startPolling } from './lib/status.svelte';
  import EngineCard from './lib/EngineCard.svelte';
  import EngineDetail from './lib/EngineDetail.svelte';
  import NfqwsRouting from './lib/NfqwsRouting.svelte';
  import type { EngineKind } from './lib/api';

  startPolling();

  const engines = $derived(status.data?.engines ?? []);
  const byKind = $derived(new Map(engines.map((e) => [e.kind, e])));
  const okCount = $derived(engines.filter((e) => e.health === 'ok').length);

  let tab = $state<'overview' | EngineKind>('overview');

  const TABS: { kind: EngineKind; label: string }[] = [
    { kind: 'nfqws2', label: 'nfqws2' },
    { kind: 'usque', label: 'usque' },
    { kind: 'xray', label: 'xray' },
  ];

  function dotClass(e: { health: string } | undefined): string {
    if (!e) return 'idle';
    if (e.health === 'ok') return 'ok';
    if (e.health === 'degraded') return 'degraded';
    if (e.health === 'down') return 'warn';
    return 'idle';
  }
</script>

<header>
  <div class="brand">
    <strong>nuxk Horizon</strong>
    <span class="muted">Keenetic · обходная платформа</span>
  </div>
  <div class="right">
    {#if status.data}<span class="muted mono">core {status.data.version}</span>{/if}
    {#if !__LITE__}<span class="badge">full</span>{/if}
  </div>
</header>

<nav>
  <button class="tab" class:active={tab === 'overview'} onclick={() => (tab = 'overview')}>
    Обзор
    {#if status.data}<span class="kpi mono">{okCount}/{engines.length}</span>{/if}
  </button>
  {#each TABS as t (t.kind)}
    <button class="tab" class:active={tab === t.kind} onclick={() => (tab = t.kind)}>
      <span class="d {dotClass(byKind.get(t.kind))}"></span>
      {t.label}
    </button>
  {/each}
</nav>

<main>
  {#if status.loading}
    <p class="muted">Подключение к nuxk-core…</p>
  {:else if status.error}
    <p class="err">nuxk-core недоступен: {status.error}</p>
  {:else if tab === 'overview'}
    {#if engines.length === 0}
      <p class="muted">Ни один движок не подключён в контроллере.</p>
    {:else}
      <section class="grid">
        {#each engines as e (e.kind)}
          <EngineCard engine={e} onOpen={() => (tab = e.kind as EngineKind)} />
        {/each}
      </section>
    {/if}
    <p class="plane muted">routing plane: <span class="mono">{JSON.stringify(status.data?.plane ?? {})}</span></p>
  {:else if tab === 'nfqws2'}
    <EngineDetail kind="nfqws2" label="nfqws2 — DPI-десинк" engine={byKind.get('nfqws2')}>
      {#snippet extra()}
        <NfqwsRouting engine={byKind.get('nfqws2')} />
      {/snippet}
    </EngineDetail>
  {:else if tab === 'usque'}
    <EngineDetail kind="usque" label="usque — WARP-туннель" engine={byKind.get('usque')} />
  {:else if tab === 'xray'}
    <div class="panel">
      <p class="muted">xray (VLESS-Reality) — движок ещё не реализован (Ф.3, после MVP-2).</p>
    </div>
  {/if}

  <p class="foot muted">MVP-1+2 — движки: usque, nfqws2. Дальше: VLESS, автоподбор режима.</p>
</main>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 22px;
    border-bottom: 1px solid var(--line);
    background: var(--surface);
  }
  .brand {
    display: flex;
    align-items: baseline;
    gap: 10px;
  }
  .brand strong {
    font-size: 16px;
    letter-spacing: 0.2px;
  }
  .right {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .badge {
    font-family: var(--font-mono);
    font-size: 10px;
    background: color-mix(in srgb, var(--accent) 16%, transparent);
    color: var(--accent);
    padding: 2px 6px;
    border-radius: 999px;
  }
  nav {
    display: flex;
    gap: 4px;
    padding: 10px 22px 0;
    max-width: 900px;
    margin: 0 auto;
    flex-wrap: wrap;
  }
  .tab {
    display: flex;
    align-items: center;
    gap: 6px;
    background: transparent;
    color: var(--muted);
    border: 1px solid transparent;
    border-bottom: none;
    border-radius: 8px 8px 0 0;
    padding: 8px 14px;
  }
  .tab.active {
    background: var(--surface);
    color: var(--ink);
    border-color: var(--line);
  }
  .tab .kpi {
    font-size: 11px;
    color: var(--muted);
  }
  .d {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--idle);
    flex: none;
  }
  .d.ok {
    background: var(--ok);
  }
  .d.warn {
    background: var(--warn);
  }
  .d.degraded {
    background: var(--degraded);
  }
  .d.idle {
    background: var(--idle);
  }
  main {
    max-width: 900px;
    margin: 0 auto;
    padding: 18px 22px 22px;
    border-top: 1px solid var(--line);
  }
  .grid {
    display: grid;
    gap: 14px;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  }
  .err {
    color: var(--warn);
  }
  .plane {
    margin-top: 14px;
  }
  .panel {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 16px;
  }
  .foot {
    margin-top: 26px;
    font-size: 12px;
  }
</style>
