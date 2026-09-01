<script lang="ts">
  import { status, startPolling } from './lib/status.svelte';
  import EngineCard from './lib/EngineCard.svelte';

  startPolling();

  const engines = $derived(status.data?.engines ?? []);
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

<main>
  {#if status.loading}
    <p class="muted">Подключение к nuxk-core…</p>
  {:else if status.error}
    <p class="err">nuxk-core недоступен: {status.error}</p>
  {:else if engines.length === 0}
    <p class="muted">Ни один движок не подключён в контроллере.</p>
  {:else}
    <section class="grid">
      {#each engines as e (e.kind)}
        <EngineCard engine={e} />
      {/each}
    </section>
  {/if}

  <p class="foot muted">
    MVP-1 — движки: usque. Дальше: nfqws2, VLESS, автоподбор режима.
  </p>
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
  .brand { display: flex; align-items: baseline; gap: 10px; }
  .brand strong { font-size: 16px; letter-spacing: 0.2px; }
  .right { display: flex; align-items: center; gap: 10px; }
  .muted { color: var(--muted); font-size: 12px; }
  .badge {
    font-family: var(--font-mono);
    font-size: 10px;
    background: color-mix(in srgb, var(--accent) 16%, transparent);
    color: var(--accent);
    padding: 2px 6px;
    border-radius: 999px;
  }
  main { max-width: 900px; margin: 0 auto; padding: 22px; }
  .grid { display: grid; gap: 14px; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
  .err { color: var(--warn); }
  .foot { margin-top: 26px; font-size: 12px; }
</style>
