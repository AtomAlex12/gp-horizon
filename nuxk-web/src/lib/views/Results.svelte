<script lang="ts">
  // «Результаты»: GP's run history, newest first. A row opens the run: why it
  // ended, its log, its settings, the strategies it found, and «Повторить» —
  // the same domains back in «Прогоны».
  import GpResults from '@gp-plane/gp-ui';
  import { validCandidates, type Candidate } from '@gp-plane/gp-ui/format';
  import { gp, runVerdict, RUN_STATUS_LABEL, RUN_STATUS_CHIP, RUN_MODE_LABEL, type RunHistoryItem, type RunLogTail, type RunSettingsPatch } from '../gp';
  import { runDraft } from '../gpdraft.svelte';
  import { copyText } from '../clipboard';
  import { fmtDur } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  let runs = $state<RunHistoryItem[] | null>(null);
  let err = $state('');
  let open = $state<string | null>(null);
  let runLog = $state<RunLogTail | null>(null);
  let logErr = $state('');
  // the open run's own finds, shown by GP Plane's shared component (gp-ui):
  // the list and exact copying are its, loading and the clipboard ours
  let found = $state<Candidate[]>([]);
  let foundBusy = $state(false);
  let foundErr = $state('');
  // 'run': GP marked which candidates this run saw; 'domains': it didn't
  // (v0.4.4 leaves seen[].run_id empty) — the run's domains' candidates then
  let foundScope = $state<'run' | 'domains'>('run');
  let foundAll = $state(0);
  const FOUND_MAX = 50; // a long list belongs to «Стратегии», with its filters

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
    void loadFound(r);
    try {
      runLog = await gp.runLog(r.run_id);
    } catch (e) {
      logErr = e instanceof Error ? e.message : String(e);
    }
  }

  // candidates this run saw work — or, when GP doesn't say which run saw
  // them, every candidate of the run's domains, and the screen says so
  async function loadFound(r: RunHistoryItem) {
    found = [];
    foundErr = '';
    foundBusy = true;
    try {
      const all = r.domains?.length ? (await gp.candidates({ domains: r.domains })).candidates : [];
      const byRun = all.filter((c) => (c.seen ?? []).some((s) => s.run_id === r.run_id));
      const list = byRun.length ? byRun : all;
      if (!validCandidates(list)) throw new Error('GP прислал стратегии в неожиданном виде');
      if (open === r.run_id) {
        foundScope = byRun.length ? 'run' : 'domains';
        foundAll = list.length;
        found = list.slice(0, FOUND_MAX);
      }
    } catch (e) {
      if (open === r.run_id) foundErr = 'Стратегии прогона: ' + (e instanceof Error ? e.message : String(e));
    } finally {
      if (open === r.run_id) foundBusy = false;
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
  const failed = (r: RunHistoryItem) => r.status === 'failed' || r.status === 'timeout';
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
                      {@const v = runVerdict(r.status, runLog)}
                      {#each v.causes as c, i (i)}
                        <div class="banner {c.level} cause">
                          <div>
                            <b>{c.title}</b>
                            {#if c.hint}<div>{c.hint}</div>{/if}
                            {#if c.line}<code class="line">{c.line}</code>{/if}
                          </div>
                        </div>
                      {/each}
                      {#if v.facts.length}<p class="hint">{v.facts.join(' · ')}</p>{/if}
                      <pre class="log">{tail(runLog.stdout_tail, 60) || (failed(r) ? 'blockcheck2 не успел ничего записать — причина выше.' : 'журнал пуст')}</pre>
                      {#if runLog.stderr_tail}
                        <details open={failed(r)}><summary class="hint">ошибки GP (stderr)</summary><pre class="log">{tail(runLog.stderr_tail, 40)}</pre></details>
                      {/if}
                    {/if}
                    {#if !foundBusy && !foundErr && foundAll}
                      <p class="hint">
                        {foundScope === 'run'
                          ? 'Стратегии, которые нашёл этот прогон.'
                          : 'GP не отмечает, какой прогон нашёл стратегию, — показаны все найденные для доменов этого прогона.'}
                        {#if foundAll > found.length}
                          Здесь первые {found.length} из {foundAll}; все, с фильтрами и применением на роутере, — во вкладке
                          <button class="linkbtn" onclick={() => go('strategies')}>«Стратегии»</button>.
                        {/if}
                      </p>
                    {/if}
                    <div class="gpui">
                      <GpResults candidates={found} loading={foundBusy} error={foundErr} onCopy={copyText} />
                    </div>
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
  .cause .line {
    display: block;
    margin-top: 6px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--muted);
    word-break: break-word;
  }
  /* GP Plane's component brings light colours of its own: put it in ours */
  .gpui :global(section) {
    border-top: 1px solid var(--line);
    padding-top: 8px;
  }
  .gpui :global(h2) {
    font-size: 13px;
    margin: 0 0 6px;
  }
  .gpui :global(h3) {
    font-size: 12.5px;
    margin: 0 0 4px;
  }
  .gpui :global(article) {
    border-color: var(--line);
    background: var(--surface);
  }
  .gpui :global(pre) {
    background: var(--surface-2);
    color: var(--ink);
    font-family: var(--font-mono);
    font-size: 11.5px;
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
