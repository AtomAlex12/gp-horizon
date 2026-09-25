<script lang="ts">
  import EngineCard from '../EngineCard.svelte';
  import type { EngineKind } from '../api';
  import { status } from '../status.svelte';
  import { MODES, planeOf, ago } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  const engines = $derived(status.data?.engines ?? []);
  const okCount = $derived(engines.filter((e) => e.health === 'ok').length);
  const plane = $derived(planeOf(status.data));
  const perMode = $derived(
    MODES.map((m) => ({
      ...m,
      n: (plane?.lists ?? []).filter((l) => l.mode === m.mode).reduce((a, l) => a + l.domains.length, 0),
    })),
  );
  const pendingN = $derived((plane?.pending ?? []).length);
  const conflictsN = $derived((plane?.conflicts ?? []).length);
</script>

<div class="grid g4">
  <div class="card kpi">
    <span class="label">Движки</span>
    <span class="value">{okCount}/{engines.length}</span>
    <span class="sub">{engines.length ? 'в порядке' : 'ни один не подключён'}</span>
  </div>
  {#each perMode as m (m.mode)}
    <button class="card kpi link" onclick={() => go('lists')}>
      <span class="label"><span class="mode {m.mode}">{m.title}</span></span>
      <span class="value">{m.n}</span>
      <span class="sub">{plane ? 'доменов' : 'плоскость выключена'}</span>
    </button>
  {/each}
</div>

{#if plane}
  <section class="card">
    <div class="card-head">
      <h2>Маршрутизация</h2>
      <span class="chip {plane.apply ? 'acc' : 'deg'}">{plane.apply ? 'применяется' : 'только план'}</span>
      <span class="spacer"></span>
      <button class="ghost sm" onclick={() => go('lists')}>Списки →</button>
    </div>
    <div class="row facts">
      <span>проверено <b>{ago(plane.checked_at)}</b></span>
      <span>изменений в плане <b class="num">{pendingN}</b></span>
      <span>конфликтов <b class="num">{conflictsN}</b></span>
      <span>при падении туннеля <b>{plane.on_down === 'block' ? 'блокировать' : 'напрямую'}</b></span>
    </div>
    {#if plane.last_error}<p class="err-text">{plane.last_error}</p>{/if}
    {#each plane.warnings ?? [] as w}<p class="hint warnline">⚠ {w}</p>{/each}
  </section>
{/if}

{#if engines.length === 0}
  <div class="card empty">Ни один движок не подключён: проверьте ENGINE_* в nuxk.conf.</div>
{:else}
  <section class="engines">
    {#each engines as e (e.kind)}
      <EngineCard engine={e} onOpen={() => go(e.kind as EngineKind)} />
    {/each}
  </section>
{/if}

<style>
  .link {
    text-align: left;
    color: inherit;
    font-weight: 400;
    white-space: normal;
    align-items: stretch;
    border-color: var(--line);
    background: var(--surface);
  }
  .link:hover {
    border-color: var(--accent);
    filter: none;
  }
  .facts {
    gap: 18px;
    font-size: 13px;
    color: var(--muted);
  }
  .facts b {
    color: var(--ink);
    font-weight: 600;
  }
  .warnline {
    color: var(--degraded);
    margin-top: 6px;
  }
  .engines {
    display: grid;
    gap: 16px;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  }
</style>
