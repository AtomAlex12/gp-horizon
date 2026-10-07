<script lang="ts">
  // «Результаты»: GP's run history, newest first. A row opens the run: its
  // log, its settings, and «Повторить» — the same domains back in «Прогоны».
  import { gp, RUN_STATUS_LABEL, RUN_STATUS_CHIP, RUN_MODE_LABEL, type RunHistoryItem, type RunLogTail, type RunSettingsPatch } from '../gp';
  import { runDraft } from '../gpdraft.svelte';
  import { fmtDur } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  let runs = $state<RunHistoryItem[] | null>(null);
  let err = $state('');
  let open = $state<string | null>(null);
  let runLog = $state<RunLogTail | null>(null);
  let logErr = $state('');

  async function load() {
    try {
      runs = (await gp.history()).runs.slice().sort((a, b) => (a.started_at < b.started_at ? 1 : -1));
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    void load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  });

  async function show(r: RunHistoryItem) {
    if (open === r.run_id) {
      open = null;
      return;
    }
    open = r.run_id;
    runLog = null;
    logErr = '';
    try {
      runLog = await gp.runLog(r.run_id);
    } catch (e) {
      logErr = e instanceof Error ? e.message : String(e);
    }
  }

  function again(r: RunHistoryItem) {
    runDraft.v = { domains: r.domains ?? [], mode: r.mode, settings: (r.settings ?? {}) as RunSettingsPatch };
    go('runs');
  }

  const when = (s?: string) => (s ? new Date(s).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' }) : '—');
  const took = (r: RunHistoryItem) => (r.completed_at ? fmtDur((Date.parse(r.completed_at) - Date.parse(r.started_at)) / 1000) : '—');
  // summary and settings are GP's free-form objects: their simple values, as they come
  const pairs = (o?: Record<string, unknown>) =>
    Object.entries(o ?? {})
      .filter(([, v]) => ['number', 'string', 'boolean'].includes(typeof v))
      .map(([k, v]) => `${k}: ${v === true ? 'да' : v === false ? 'нет' : v}`)
      .join(' · ');
  const tail = (s: string | undefined, n: number) => (s ?? '').split('\n').slice(-n).join('\n');
</script>

{#if err}<div class="banner warn">{err}</div>{/if}
<section class="card">
  <div class="card-head">
    <h2>История прогонов</h2>
    <span class="spacer"></span>
    <span class="hint">нажмите строку — журнал и настройки прогона</span>
    <button class="ghost sm" onclick={() => go('strategies')}>Найденные стратегии →</button>
  </div>
  {#if runs === null}
    <p class="muted">Загрузка…</p>
  {:else if !runs.length}
    <div class="empty">Прогонов ещё не было — запустите первый во вкладке «Прогоны».</div>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Начат</th><th>Режим</th><th>Домены</th><th>Итог</th><th>Длился</th><th>Счётчики</th></tr></thead>
        <tbody>
          {#each runs as r (r.run_id)}
            <tr class="click" class:sel={open === r.run_id} onclick={() => show(r)}>
              <td class="mono">{when(r.started_at)}</td>
              <td>{RUN_MODE_LABEL[r.mode ?? ''] ?? RUN_MODE_LABEL[r.kind ?? ''] ?? r.mode ?? r.kind ?? '—'}</td>
              <td title={(r.domains ?? []).join(', ')}>
                {#if r.domains?.length}{r.domains.slice(0, 2).join(', ')}{r.domains.length > 2 ? ` +${r.domains.length - 2}` : ''}{:else}—{/if}
              </td>
              <td><span class="chip {RUN_STATUS_CHIP(r.status)}">{RUN_STATUS_LABEL[r.status] ?? r.status}</span></td>
              <td>{took(r)}</td>
              <td class="hint">{pairs(r.summary) || '—'}</td>
            </tr>
            {#if open === r.run_id}
              <tr class="detail-row">
                <td colspan="6">
                  <div class="detail">
                    <div class="row">
                      <b>Прогон {when(r.started_at)}</b>
                      <span class="chip {RUN_STATUS_CHIP(r.status)}">{RUN_STATUS_LABEL[r.status] ?? r.status}</span>
                      <span class="hint mono">{r.run_id}</span>
                      <span class="spacer"></span>
                      {#if r.domains?.length}<button class="ghost sm" onclick={() => again(r)}>Повторить с теми же доменами</button>{/if}
                    </div>
                    {#if r.domains?.length}
                      <p class="hint">Домены ({r.domains.length}): <span class="mono">{r.domains.slice(0, 30).join(', ')}{r.domains.length > 30 ? ' …' : ''}</span></p>
                    {/if}
                    {#if pairs(r.settings)}<p class="hint">Настройки: {pairs(r.settings)}</p>{/if}
                    {#if logErr}
                      <p class="err-text">{logErr}</p>
                    {:else if !runLog}
                      <p class="muted">Журнал загружается…</p>
                    {:else}
                      <pre class="log">{tail(runLog.stdout_tail, 60) || 'журнал пуст'}</pre>
                      {#if runLog.stderr_tail}<details><summary class="hint">ошибки</summary><pre class="log">{tail(runLog.stderr_tail, 40)}</pre></details>{/if}
                    {/if}
                  </div>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<style>
  h2 {
    font-size: 15px;
  }
  tr.click {
    cursor: pointer;
  }
  tr.click:hover td,
  tr.sel td {
    background: var(--surface-2);
  }
  .detail-row td {
    background: var(--surface-2);
  }
  .detail {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .log {
    margin: 0;
    max-height: 260px;
    overflow: auto;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    padding: 10px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>
