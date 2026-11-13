<script lang="ts">
  import { gp2, gp2Pages, watchSearch, type GP } from './gp2';
  import { onMount } from 'svelte';
  import { runDraft } from './gpdraft.svelte';

  let engine = $state('blockcheck2-gp');
  let domainText = $state('');
  let preset = $state('');
  let presets = $state<GP['DomainList'][]>([]);
  let parameters = $state<GP['BlockcheckGpParameters']>({
    enable_http: false, enable_tls12: true, enable_tls13: false, include_quic: true,
    enable_ipv6: false, scan_level: 'standard', repeats: 1, repeat_parallel: false,
    skip_dnscheck: true, skip_ipblock: true, curl_max_time: 2, curl_max_time_quic: 2,
    curl_max_time_doh: 2, debug_stdout: false, curl_parallelism: 4,
  });
  let search = $state<GP['CurrentSearch']>();
  let milestone = $state<number>();
  let details = $state<GP['RunDetail']>();
  let lastRun = $state('');
  let journalCursor: string | null = null;
  let lines = $state<GP['LogEntry'][]>([]);
  let truncated = $state(false);
  let error = $state('');
  let connection = $state('');
  let busy = $state(false);
  const domains = $derived([...new Set(domainText.split(/[\s,]+/).filter(Boolean))]);
  const run = $derived(search?.active ? search.run : undefined);

  const toggles = [
    ['enable_http','HTTP'], ['enable_tls12','TLS 1.2'], ['enable_tls13','TLS 1.3'], ['include_quic','QUIC'],
    ['enable_ipv6','IPv6'], ['repeat_parallel','Повторы параллельно'], ['skip_dnscheck','Пропустить проверку DNS'],
    ['skip_ipblock','Пропустить проверку IP'], ['debug_stdout','Подробный журнал'],
  ] as const;
  const numbers = [
    ['repeats','Повторы'], ['curl_max_time','Таймаут HTTP / TLS, с'], ['curl_max_time_quic','Таймаут QUIC, с'],
    ['curl_max_time_doh','Таймаут DoH, с'],
  ] as const;

  function update(value: GP['CurrentSearch']) {
    search = value;
    if (value.active) selectRun(value.run.id);
  }

  function selectRun(id: string) {
    if (id === lastRun) return;
    journalCursor = null;
    lines = [];
    truncated = false;
    details = undefined;
    lastRun = id;
  }

  onMount(() => {
    if (runDraft.v) {
      domainText = runDraft.v.domains.join('\n');
      runDraft.v = null;
    }
    const controller = new AbortController();
    const signal = controller.signal;
    void gp2Pages<GP['DomainList']>('data/domain-lists').then(items => { if (!signal.aborted) presets = items; }).catch(() => {});
    void gp2<GP['MilestonePage']>('data/milestones?limit=1').then(page => { if (!signal.aborted) milestone = page.current_id; }).catch(() => {});
    async function connect() {
      while (!signal.aborted) {
        try {
          const status = await gp2<GP['CurrentSearch']>('search/current');
          if (signal.aborted) return;
          update(status);
          connection = '';
          await watchSearch(signal, update);
          if (!signal.aborted) connection = 'Связь с Core прервана. Повторяем подключение.';
        } catch {
          if (!signal.aborted) connection = 'Связь с Core прервана. Повторяем подключение.';
        }
        if (!signal.aborted) await new Promise(resolve => setTimeout(resolve, 2000));
      }
    }
    void connect();
    return () => controller.abort();
  });

  $effect(() => {
    const id = lastRun;
    const active = search?.active;
    if (!id) return;
    let closed = false;
    let pending = false;

    const path = 'data/runs/' + encodeURIComponent(id);
    async function load() {
      if (pending || closed) return;
      pending = true;
      try {
        const page = await gp2<GP['LogPage']>(path + '/log?limit=200' + (journalCursor ? '&after=' + encodeURIComponent(journalCursor) : ''));
        if (closed) return;
        journalCursor = page.next_after ?? journalCursor;
        truncated = truncated || page.truncated;
        lines = [...lines, ...page.entries].slice(-1500);
        if (!details || !active) {
          const loaded = await gp2<GP['RunDetail']>(path);
          if (!closed) details = loaded;
        }
      } catch (reason) {
        if (!closed) error = String(reason);
      } finally {
        pending = false;
      }
    }
    void load();
    const timer = setInterval(load, 2500);
    return () => { closed = true; clearInterval(timer); };
  });

  async function choosePreset(id: string) {
    preset = '';
    if (!id) return;
    await action(async () => {
      const items = await gp2Pages<GP['DomainEntry']>('data/domain-lists/' + id + '/domains');
      domainText = items.map(item => item.domain).join('\n');
      preset = id;
    });
  }

  async function action(work: () => Promise<void>) {
    busy = true;
    error = '';
    try { await work(); }
    catch (reason) { error = reason instanceof Error ? reason.message : String(reason); }
    finally { busy = false; }
  }

  function start() {
    return action(async () => {
      const { curl_parallelism, ...stock } = parameters;
      const accepted = await gp2<GP['SearchAccepted']>('engines/' + engine + '/start', 'POST', {
        domains, parameters: engine === 'blockcheck2-gp' ? { ...stock, curl_parallelism } : stock,
        ...(preset ? { domain_list_id: Number(preset) } : {}),
      });
      selectRun(accepted.id);
      update(await gp2<GP['CurrentSearch']>('search/current'));
    });
  }

  function stop() {
    return action(async () => {
      await gp2<GP['StopSearchResult']>('search/stop', 'POST');
      update(await gp2<GP['CurrentSearch']>('search/current'));
    });
  }

  function resume(id: string) {
    return action(async () => {
      const accepted = await gp2<GP['SearchAccepted']>('engines/blockcheck2-gp/runs/' + encodeURIComponent(id) + '/resume', 'POST');
      selectRun(accepted.id);
      update(await gp2<GP['CurrentSearch']>('search/current'));
    });
  }
</script>

{#if connection}<p class="hint" role="status">{connection}</p>{/if}
{#if error}<p class="err-text" role="alert">{error}</p>{/if}

{#if run}
  <div class="columns">
    <section class="card">
      <div class="card-head"><h2>Текущий поиск</h2><span class="chip">{run.status}</span></div>
      <p>{run.engine.name} · веха {run.milestone_id}</p>
      {#if run.progress?.checked != null}<p>Проверено {run.progress.checked}{run.progress.total != null ? ' / ' + run.progress.total : ''}</p>{/if}
      {#if run.progress?.current_args}<pre>{run.progress.current_args}</pre>{/if}
      <button class="warn" disabled={busy || run.status === 'stopping'} onclick={stop}>Остановить</button>
    </section>
    <aside class="card run-domains"><h2>Домены поиска</h2>{#each details?.domains ?? [] as domain}<div class="mono">{domain}</div>{/each}</aside>
  </div>
{:else}
  <form class="columns" onsubmit={event => { event.preventDefault(); void start(); }}>
    <section class="card">
      <div class="card-head"><h2>Новый поиск</h2></div>
      <label>Движок <select bind:value={engine}><option value="blockcheck2">Обычный blockcheck2</option><option value="blockcheck2-gp">GP · доработанный blockcheck2</option></select></label>
      <label>Пресет доменов
        <select value={preset} disabled={busy} onchange={event => choosePreset(event.currentTarget.value)}>
          <option value="">Свои домены</option>
          {#each presets as item (item.id)}<option value={String(item.id)}>{item.name}</option>{/each}
        </select>
      </label>
      <label>Домены <textarea rows="4" class="mono" value={domainText} oninput={event => { domainText = event.currentTarget.value; preset = ''; }} required></textarea></label>
      <fieldset>
        <legend>Параметры движка</legend>
        <div class="row">
          {#each toggles.slice(0, 4) as [key, label]}<label class="check"><input type="checkbox" bind:checked={parameters[key]} />{label}</label>{/each}
        </div>
        <details>
          <summary>Тонкая настройка</summary>
          <label>Режим <select bind:value={parameters.scan_level}><option value="quick">Быстрый</option><option value="standard">Обычный</option><option value="force">Полный</option></select></label>
          <div class="row">
            {#each numbers as [key, label]}<label>{label}<input type="number" min="1" required bind:value={parameters[key]} /></label>{/each}
            {#if engine === 'blockcheck2-gp'}<label>Параллельность GP<input type="number" min="1" max="10" required bind:value={parameters.curl_parallelism} /></label>{/if}
          </div>
          <div class="row">
            {#each toggles.slice(4) as [key, label]}<label class="check"><input type="checkbox" bind:checked={parameters[key]} />{label}</label>{/each}
          </div>
        </details>
      </fieldset>
    </section>
    <aside class="card summary">
      <div class="card-head"><h2>Перед запуском</h2></div>
      <dl class="kv"><dt>Движок</dt><dd>{engine === 'blockcheck2-gp' ? 'GP' : 'Stock'}</dd><dt>Домены</dt><dd>{domains.length}</dd>
        <dt>Протоколы</dt><dd>{toggles.slice(0,4).filter(([key]) => parameters[key]).map(([, label]) => label).join(', ') || '—'}</dd>
        <dt>Веха</dt><dd>{milestone ?? '—'}</dd>
      </dl>
      <button type="submit" disabled={busy || !domains.length || !search || !!connection}>Запустить поиск</button>
    </aside>
  </form>
{/if}

{#if lastRun}
  <details class="card journal" open>
    <summary>Журнал поиска {details ? '· ' + details.status : ''}</summary>
    {#if truncated}<p class="hint">Начало журнала недоступно.</p>{/if}
    {#if !lines.length}<p class="hint">Записей пока нет.</p>{/if}
    <pre>{lines.map(line => line.message).join('\n')}</pre>
    {#if !run && details?.resume_available}<button class="ghost" disabled={busy} onclick={() => resume(lastRun)}>Продолжить GP-поиск</button>{/if}
  </details>
{/if}

<style>
  .columns { display:grid; grid-template-columns:minmax(0, 2fr) minmax(220px, 1fr); gap:16px; align-items:start; }
  label { display:grid; gap:4px; margin-bottom:10px; }
  .check { display:flex; gap:5px; align-items:center; }
  fieldset { border:1px solid var(--line); border-radius:8px; min-width:0; }
  input[type=number] { width:8rem; }
  .summary button { margin-top:12px; }
  .run-domains, pre { max-height:22rem; overflow:auto; }
  pre { white-space:pre-wrap; overflow-wrap:anywhere; }
  .journal { margin-top:14px; }
  summary { cursor:pointer; }
  @media (max-width:820px) { .columns { grid-template-columns:minmax(0, 1fr); } }
</style>
