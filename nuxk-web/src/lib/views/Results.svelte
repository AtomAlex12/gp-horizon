<script lang="ts">
  // «Результаты»: GP's run history, newest first.
  import { gp, RUN_STATUS_LABEL, type RunHistoryItem } from '../gp';
  import { fmtDur } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  let runs = $state<RunHistoryItem[] | null>(null);
  let err = $state('');

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

  const when = (s?: string) => (s ? new Date(s).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' }) : '—');
  const took = (r: RunHistoryItem) =>
    r.completed_at ? fmtDur((Date.parse(r.completed_at) - Date.parse(r.started_at)) / 1000) : '—';
  const MODE: Record<string, string> = { standard: 'по доменам', multi_domain: 'общая', common_strategy: 'проверка стратегии' };
  const chip = (s: string) => (s === 'success' ? 'ok' : s === 'failed' || s === 'timeout' ? 'warn' : s === 'stopped' ? 'deg' : 'acc');
  // summary is GP's free-form object: show its numeric counters as they come
  const counters = (r: RunHistoryItem) =>
    Object.entries(r.summary ?? {})
      .filter(([, v]) => typeof v === 'number')
      .map(([k, v]) => `${k}: ${v}`)
      .join(' · ');
</script>

{#if err}<div class="banner warn">{err}</div>{/if}
<section class="card">
  <div class="card-head">
    <h2>История прогонов</h2>
    <span class="spacer"></span>
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
            <tr>
              <td class="mono">{when(r.started_at)}</td>
              <td>{MODE[r.mode ?? ''] ?? r.mode ?? r.kind ?? '—'}</td>
              <td title={(r.domains ?? []).join(', ')}>
                {#if r.domains?.length}{r.domains.slice(0, 2).join(', ')}{r.domains.length > 2 ? ` +${r.domains.length - 2}` : ''}{:else}—{/if}
              </td>
              <td><span class="chip {chip(r.status)}">{RUN_STATUS_LABEL[r.status] ?? r.status}</span></td>
              <td>{took(r)}</td>
              <td class="hint">{counters(r) || '—'}</td>
            </tr>
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
</style>
