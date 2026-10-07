<script lang="ts">
  // The agent's own log, live (SSE) — the last entries it keeps in memory.
  import { logs, status, node } from '../status.svelte';

  let { go }: { go: (tab: string) => void } = $props();

  const LEVELS = ['all', 'warn', 'error'] as const;
  let level = $state<(typeof LEVELS)[number]>('all');
  let q = $state('');
  let paused = $state(false);
  let frozen = $state<typeof logs.items>([]);
  let box = $state<HTMLDivElement>();

  const src = $derived(paused ? frozen : logs.items);
  const shown = $derived(
    src
      .filter((e) => level === 'all' || (level === 'warn' ? e.level === 'warn' || e.level === 'error' : e.level === 'error'))
      .filter((e) => !q || `${e.msg} ${e.attrs ?? ''}`.toLowerCase().includes(q.toLowerCase()))
      .slice(-400),
  );

  function toggle() {
    paused = !paused;
    if (paused) frozen = logs.items.slice();
  }
  // follow the tail unless the reader scrolled up
  $effect(() => {
    void shown.length;
    if (!box) return;
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    if (atBottom) queueMicrotask(() => box && (box.scrollTop = box.scrollHeight));
  });
  const clock = (ms: number) => new Date(ms).toTimeString().slice(0, 8);
</script>

<section class="card">
  <div class="card-head">
    <h2>Логи nuxk-core</h2>
    <span class="chip {status.live ? 'ok' : ''}">{status.live ? 'в реальном времени' : 'обновление раз в 5 с'}</span>
    <span class="spacer"></span>
    <input type="text" bind:value={q} placeholder="поиск" aria-label="Поиск по логу" class="search" />
    <div class="seg" role="group" aria-label="Уровень">
      {#each LEVELS as l (l)}<button class:on={level === l} onclick={() => (level = l)}>{l === 'all' ? 'все' : l === 'warn' ? 'предупреждения' : 'ошибки'}</button>{/each}
    </div>
    <button class="ghost sm" onclick={toggle}>{paused ? '▶ Продолжить' : '❚❚ Пауза'}</button>
  </div>
  <!-- a scrollable region must be reachable by keyboard -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="log" bind:this={box} tabindex="0" role="log" aria-label="Журнал nuxk-core">
    {#each shown as e (e.seq)}
      <div class="line">
        <span class="muted">{clock(e.ts)}</span>
        <span class="lvl {e.level}">{e.level.toUpperCase()}</span>
        <span>{e.msg}{#if e.attrs}<span class="attrs">{e.attrs}</span>{/if}</span>
      </div>
    {:else}
      <div class="muted">Записей пока нет.</div>
    {/each}
  </div>
  <p class="hint">Хранятся последние 500 записей в памяти nuxk-core. Полный файл на роутере: <span class="mono">/opt/var/log/nuxk-core.log</span>.</p>
  {#if !__LITE__ && node.via === 'controller'}
    <p class="hint">
      Это журнал агента на роутере. Подбор стратегий идёт на Pi, и сюда он не пишет: почему прогон завершился — в
      <button class="linkbtn" onclick={() => go('results')}>«Результатах»</button> (раскройте строку прогона), журнал
      самого GP — в <button class="linkbtn" onclick={() => go('plugins')}>«Плагинах»</button>.
    </p>
  {/if}
</section>

<style>
  .search {
    width: 180px;
  }
  .log {
    font-family: var(--font-mono);
    font-size: 12px;
    background: var(--surface-2);
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 8px 10px;
    height: 480px;
    overflow-y: auto;
    margin-bottom: 8px;
  }
  .line {
    display: grid;
    grid-template-columns: 64px 52px 1fr;
    gap: 8px;
    padding: 1px 0;
    word-break: break-word;
  }
  .lvl.info {
    color: var(--ok);
  }
  .lvl.warn {
    color: var(--degraded);
  }
  .lvl.error {
    color: var(--warn);
  }
  .lvl.debug {
    color: var(--muted);
  }
  .attrs {
    color: var(--muted);
    margin-left: 0.6em;
  }
  @media (max-width: 480px) {
    .line {
      grid-template-columns: 56px 1fr;
    }
    .lvl {
      display: none;
    }
    .search {
      width: 100%;
    }
  }
</style>
