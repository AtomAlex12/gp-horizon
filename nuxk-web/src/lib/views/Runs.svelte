<script lang="ts">
  // «Прогоны»: GP runs zapret2's blockcheck2 from the Pi — through the same
  // ISP as the router — and saves the strategies that open the domains.
  import { status } from '../status.svelte';
  import { gp, RUN_STATUS_LABEL, type PreflightStatus, type RunLogTail, type RunMode, type RunProgress, type ScanLevel } from '../gp';
  import { fmtDur, planeOf, splitDomains, modeTitle } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  const plane = $derived(planeOf(status.data));
  const lists = $derived(plane?.lists ?? []);

  let preflight = $state<PreflightStatus | null>(null);
  let progress = $state<RunProgress | null>(null);
  let log = $state<RunLogTail | null>(null);
  let err = $state('');
  let busy = $state(false);

  // the form
  let chosen = $state<Record<string, boolean>>({});
  let extra = $state('');
  let mode = $state<RunMode>('standard');
  let scan = $state<ScanLevel>('quick');
  let tls12 = $state(true);
  let tls13 = $state(false);
  let http = $state(false);
  let quic = $state(false);

  const domains = $derived.by(() => {
    const out = new Set<string>();
    for (const l of lists) if (chosen[l.name]) l.domains.forEach((d) => out.add(d));
    splitDomains(extra).forEach((d) => out.add(d));
    return [...out];
  });
  const active = $derived(!!progress && ['queued', 'running', 'saving', 'stopping'].includes(progress.status));

  async function poll() {
    try {
      progress = await gp.progress();
      if (progress && progress.status !== 'idle') log = await gp.log();
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    gp.preflight().then((p) => (preflight = p), (e) => (err = e instanceof Error ? e.message : String(e)));
    void poll();
    const t = setInterval(poll, 3000);
    return () => clearInterval(t);
  });

  async function start() {
    busy = true;
    err = '';
    try {
      await gp.start({
        mode,
        domains,
        protocols: quic ? ['tcp', 'quic'] : ['tcp'],
        settings: { scan_level: scan, enable_tls12: tls12, enable_tls13: tls13, enable_http: http, include_quic: quic },
      });
      await poll();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function stop() {
    if (!confirm('Остановить прогон? Найденные к этому моменту стратегии сохранятся.')) return;
    busy = true;
    try {
      await gp.stop();
      await poll();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const pct = (a?: number, b?: number) => (a && b ? Math.min(100, Math.round((a / b) * 100)) : 0);
  const tailLines = (s: string | undefined, n: number) => (s ?? '').split('\n').slice(-n).join('\n');
</script>

{#if err}<div class="banner warn">{err}</div>{/if}

<div class="banner deg">
  <div>
    <b>Проверка идёт с Pi, через роутер.</b> Для доменов, которые уже есть в списке DPI роутера, nfqws2 на роутере
    добавляет свою стратегию поверх проверяемой — пока Pi не исключён из обработки nfqws2 на роутере (политика
    Keenetic), результаты по этим доменам могут быть неточными.
  </div>
</div>

{#if preflight && !preflight.ready}
  <section class="card">
    <div class="card-head"><h2>GP не готов к прогону</h2></div>
    <ul class="checks">
      {#each preflight.checks as c (c.name)}
        <li><span class="chip {c.status === 'ok' ? 'ok' : 'warn'}">{c.status}</span> <b>{c.name}</b> {c.message ?? ''}</li>
      {/each}
    </ul>
  </section>
{/if}

{#if active && progress}
  <section class="card">
    <div class="card-head">
      <h2>Идёт прогон</h2>
      <span class="chip acc">{RUN_STATUS_LABEL[progress.status] ?? progress.status}</span>
      {#if progress.stage}<span class="hint">{progress.stage}</span>{/if}
      <span class="spacer"></span>
      <button class="warn" onclick={stop} disabled={busy || progress.status === 'stopping'}>Остановить</button>
    </div>
    <dl class="kv">
      {#if progress.domains_total}
        <dt>домены</dt><dd>{progress.domains_processed ?? 0} из {progress.domains_total}</dd>
      {/if}
      {#if progress.attempts_total}
        <dt>попытки</dt><dd>{progress.attempts_processed ?? 0} из {progress.attempts_total}</dd>
      {/if}
      <dt>идёт</dt><dd>{fmtDur(progress.elapsed_seconds ?? 0)}</dd>
      {#if progress.eta_seconds}<dt>осталось примерно</dt><dd>{fmtDur(progress.eta_seconds)}</dd>{/if}
    </dl>
    <div class="bar"><div class="bar-fill" style="width:{pct(progress.attempts_processed ?? progress.domains_processed, progress.attempts_total ?? progress.domains_total)}%"></div></div>
    {#if log?.stdout_tail}<pre class="log">{tailLines(log.stdout_tail, 40)}</pre>{/if}
    <p class="hint">Найденные стратегии появляются во вкладке <button class="linkbtn" onclick={() => go('strategies')}>«Стратегии»</button> по ходу прогона.</p>
  </section>
{:else}
  <section class="card">
    <div class="card-head"><h2>Новый прогон</h2></div>
    <div class="grid g2">
      <div class="stack">
        <span class="label">Домены</span>
        {#each lists as l (l.name)}
          <label class="check"><input type="checkbox" bind:checked={chosen[l.name]} /> {l.name} <span class="muted">· {modeTitle(l.mode)} · {l.domains.length}</span></label>
        {:else}
          <p class="hint">Списков nuxk пока нет — впишите домены ниже.</p>
        {/each}
        <textarea rows="4" bind:value={extra} placeholder={'rutracker.org\nbrowserleaks.com'}></textarea>
        <span class="hint">Выбрано доменов: {domains.length}. Больше доменов — дольше прогон: подбор может идти часами.</span>
      </div>
      <div class="stack">
        <label class="field">
          Режим
          <select bind:value={mode}>
            <option value="standard">по каждому домену отдельно</option>
            <option value="multi_domain">общая стратегия для всех (экспериментально)</option>
          </select>
        </label>
        <label class="field">
          Глубина перебора
          <select bind:value={scan}>
            <option value="quick">быстро — до первой рабочей</option>
            <option value="standard">обычно</option>
            <option value="force">полный перебор (очень долго)</option>
          </select>
        </label>
        <span class="label">Что проверять</span>
        <div class="row">
          <label class="check"><input type="checkbox" bind:checked={tls12} /> TLS 1.2</label>
          <label class="check"><input type="checkbox" bind:checked={tls13} /> TLS 1.3</label>
          <label class="check"><input type="checkbox" bind:checked={http} /> HTTP</label>
          <label class="check"><input type="checkbox" bind:checked={quic} /> QUIC</label>
        </div>
      </div>
    </div>
    <div class="row start">
      <button onclick={start} disabled={busy || !domains.length || !(tls12 || tls13 || http || quic) || (preflight !== null && !preflight.ready)}>
        {busy ? 'Запускаю…' : `Начать прогон (${domains.length})`}
      </button>
      {#if progress && progress.status !== 'idle'}
        <span class="hint">Прошлый прогон: {RUN_STATUS_LABEL[progress.status] ?? progress.status} — см. «Результаты».</span>
      {/if}
    </div>
  </section>
{/if}

<style>
  .label {
    font-size: 12px;
    color: var(--muted);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
  }
  .checks {
    margin: 0;
    padding-left: 18px;
    font-size: 13px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .bar {
    margin: 12px 0;
  }
  .log {
    margin: 0 0 8px;
    max-height: 300px;
    overflow: auto;
    background: var(--surface-2);
    border-radius: 8px;
    padding: 10px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .start {
    margin-top: 14px;
  }
  .linkbtn {
    display: inline;
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    font-size: inherit;
    text-decoration: underline;
  }
  h2 {
    font-size: 15px;
  }
</style>
