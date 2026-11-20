<script lang="ts">
  import { gp2, gp2Pages, watchSearch, type GP } from './gp2';
  import { onMount } from 'svelte';
  import Icon from './Icon.svelte';
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
    ['curl_max_time','HTTP / HTTPS'], ['curl_max_time_quic','QUIC'], ['curl_max_time_doh','DNS-over-HTTPS'],
  ] as const;

  const scanNames = { quick: 'Быстрый', standard: 'Стандартный', force: 'Полный перебор' };
  const scanHints = { quick: 'Сокращённая проверка: повторы могут завершиться после первой неудачи.', standard: 'Обычный режим исследования стратегий.', force: 'Перебор продолжается независимо от уже найденного результата.' };
  const engineName = $derived(engine === 'blockcheck2-gp' ? 'Blockcheck2 GP' : 'Blockcheck2 · штатный');
  const protocolLabel = $derived(toggles.slice(0,4).filter(([key]) => parameters[key]).map(([, label]) => label).join(' · ') || 'Не выбраны');
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
    if (!toggles.slice(0, 4).some(([key]) => parameters[key])) { error = 'Выберите хотя бы один протокол проверки.'; return; }
    return action(async () => {
      const { curl_parallelism, ...values } = parameters;
      const stock = { ...values, repeat_parallel: parameters.repeats > 1 && parameters.repeat_parallel };
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
  <header class="page-heading">
    <div><div class="eyebrow">Поиск стратегий</div><h1>Новый поиск</h1><p>Выберите движок и домены. Параметры можно уточнить перед запуском.</p></div>
    <span class="milestone">{milestone === undefined ? 'Веха загружается…' : `Веха №${milestone} · текущая`}</span>
  </header>
  <form class="columns" onsubmit={event => { event.preventDefault(); void start(); }}>
    <section class="card form-stack">
      <label class="field"><span id="gp-engine-label">Движок</span><select aria-labelledby="gp-engine-label" bind:value={engine}><option value="blockcheck2-gp">Blockcheck2 GP</option><option value="blockcheck2">Blockcheck2 · штатный</option></select>
        <span class="hint">{engine === 'blockcheck2-gp' ? 'Параллельные проверки и продолжение остановленного поиска.' : 'Штатный подбор zapret2. Остановленный поиск запускается заново.'}</span>
      </label>
      <label class="field"><span id="gp-list-label">Список доменов</span>
        <select aria-labelledby="gp-list-label" value={preset} disabled={busy} onchange={event => choosePreset(event.currentTarget.value)}>
          <option value="">Свои домены</option>
          {#each presets as item (item.id)}<option value={String(item.id)}>{item.name}</option>{/each}
        </select>
      </label>
      <label class="field"><span class="field-label">Домены <span class="hint">{domains.length}</span></span>
        <textarea rows="4" class="mono" value={domainText} spellcheck="false" oninput={event => { domainText = event.currentTarget.value; preset = ''; }} required></textarea>
        <span class="hint">По одному домену в строке. Все домены равноправны.</span>
      </label>
      <section class="engine-settings">
        <h2>Параметры движка</h2>
        <fieldset><legend>Протоколы проверки</legend><div class="protocols">
          {#each toggles.slice(0, 4) as [key, label]}<label class="check"><input type="checkbox" bind:checked={parameters[key]} />{label}</label>{/each}
        </div></fieldset>
        <label class="parameter-check"><input type="checkbox" bind:checked={parameters.enable_ipv6} /><span>Также проверять IPv6<span class="hint">IPv4 остаётся включённым.</span></span></label>
        <label class="field"><span id="gp-mode-label">Режим проверки</span><select aria-labelledby="gp-mode-label" bind:value={parameters.scan_level}>
          <option value="quick">Быстрый</option><option value="standard">Стандартный</option><option value="force">Полный перебор</option>
        </select><span class="hint">{scanHints[parameters.scan_level]}</span></label>
        <div class="values-grid">
          <label class="field"><span>Повторы проверки</span><input type="number" min="1" step="1" required bind:value={parameters.repeats} /></label>
          {#if engine === 'blockcheck2-gp'}<label class="field"><span>Параллельные запросы</span><input type="number" min="1" max="10" step="1" required bind:value={parameters.curl_parallelism} /></label>{/if}
        </div>
        <label class="parameter-check"><input type="checkbox" bind:checked={parameters.repeat_parallel} disabled={!(parameters.repeats > 1)} /><span>Выполнять повторы параллельно<span class="hint">{parameters.repeats > 1 ? 'Повторы одного теста выполняются одновременно.' : 'Доступно при двух и более повторах.'}</span></span></label>
        <details class="fine-settings">
          <summary>Тонкая настройка<span class="fine-note">Сетевые проверки, лимиты запросов и журнал</span></summary>
          <div class="fine-content">
            <label class="parameter-check"><input type="checkbox" checked={!parameters.skip_dnscheck} onchange={event => parameters.skip_dnscheck = !event.currentTarget.checked} /><span>Проверять корректность DNS<span class="hint">Предварительная проверка ответов DNS перед подбором.</span></span></label>
            <label class="parameter-check"><input type="checkbox" checked={!parameters.skip_ipblock} onchange={event => parameters.skip_ipblock = !event.currentTarget.checked} /><span>Проверять блокировку IP/порта<span class="hint">Дополнительная проверка доступности адресов и портов.</span></span></label>
            <fieldset><legend>Лимиты отдельных запросов, с</legend><div class="values-grid">
              {#each numbers as [key, label]}<label class="field"><span>{label}</span><input type="number" min="1" step="1" required bind:value={parameters[key]} /></label>{/each}
            </div><p class="hint time-hint">DoH — когда движок использует DNS-over-HTTPS. Длительность всего поиска не ограничивается.</p></fieldset>
            <label class="parameter-check"><input type="checkbox" bind:checked={parameters.debug_stdout} /><span>Сохранять подробный журнал<span class="hint">Больше записей на накопитель. Обычно достаточно кратких итогов и ошибок.</span></span></label>
          </div>
        </details>
      </section>
    </section>
    <aside class="card summary" aria-label="Сводка запуска">
      <h2>Перед запуском</h2>
      <dl class="summary-main">
        <div><dt>Движок</dt><dd>{engineName}</dd></div>
        <div><dt>Домены</dt><dd>{domains.length} · {preset ? `список «${presets.find(item => String(item.id) === preset)?.name ?? ''}»` : 'свой список'}</dd></div>
        <div><dt>Протоколы</dt><dd>{protocolLabel}</dd></div>
        <div><dt>Веха результатов</dt><dd>{milestone === undefined ? '—' : `№${milestone} · текущая`}</dd></div>
      </dl>
      <dl class="summary-details">
        <dt>Сеть</dt><dd>{parameters.enable_ipv6 ? 'IPv4 + IPv6' : 'IPv4'}</dd>
        <dt>Режим</dt><dd>{scanNames[parameters.scan_level]}</dd>
        <dt>Повторы</dt><dd>{parameters.repeats ?? '—'}</dd>
        <dt>Повторы параллельно</dt><dd>{parameters.repeats <= 1 ? 'Не применяется' : parameters.repeat_parallel ? 'Да' : 'Нет'}</dd>
        {#if engine === 'blockcheck2-gp'}<dt>Параллельность</dt><dd>{parameters.curl_parallelism ?? '—'}</dd>{/if}
        <dt class="summary-label">Дополнительные проверки</dt>
        <dt>Корректность DNS</dt><dd>{parameters.skip_dnscheck ? 'Пропускается' : 'Проверять'}</dd>
        <dt>Блокировка IP/порта</dt><dd>{parameters.skip_ipblock ? 'Пропускается' : 'Проверять'}</dd>
        <dt class="summary-label">Лимиты запросов</dt>
        <dt>HTTP / HTTPS</dt><dd>{parameters.curl_max_time ?? '—'} с</dd>
        <dt>QUIC</dt><dd>{parameters.curl_max_time_quic ?? '—'} с</dd>
        <dt>DoH</dt><dd>{parameters.curl_max_time_doh ?? '—'} с</dd>
        <dt>Подробный журнал</dt><dd>{parameters.debug_stdout ? 'Сохранять' : 'Не сохранять'}</dd>
      </dl>
      <button type="submit" disabled={busy || !domains.length || !search || !!connection}><Icon name="runs" size={15} />Запустить поиск</button>
      <p class="small-note">Сначала проверим параметры и готовность движка. Одновременно работает один поиск.</p>
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
  .page-heading { display: flex; justify-content: space-between; align-items: start; gap: 16px; margin-bottom: 24px; }
  .eyebrow { text-transform: uppercase; font-size: 10px; letter-spacing: 1px; font-weight: 600; color: var(--ink-2); margin-bottom: 6px; }
  h1 { font-size: 25px; font-weight: 600; letter-spacing: -.6px; line-height: 1.25; }
  .page-heading p { margin: 7px 0 0; color: var(--ink-2); font-size: 13px; }
  .milestone { white-space: nowrap; padding: 5px 8px; background: var(--surface-2); border-radius: 5px; color: var(--ink-2); font-size: 11px; }
  .columns { display: grid; grid-template-columns: minmax(0, 1fr) 285px; gap: 20px; align-items: start; }
  .form-stack { display: grid; gap: 22px; }
  .field { display: grid; gap: 6px; min-width: 0; font-size: 13px; font-weight: 500; }
  .field-label { display: flex; justify-content: space-between; gap: 8px; }
  .hint { font-weight: 400; }
  textarea { min-height: 115px; padding: 10px 11px; line-height: 1.9; font-size: 13px; background: var(--surface); border-radius: 6px; }
  fieldset { border: 0; margin: 0; padding: 0; min-width: 0; }
  legend { font-weight: 500; font-size: 13px; margin-bottom: 9px; }
  h2 { font-size: 17px; font-weight: 600; letter-spacing: -.2px; }
  .protocols { display: flex; gap: 8px; flex-wrap: wrap; }
  .check { display: inline-flex; align-items: center; gap: 6px; padding: 7px 10px; border: 1px solid var(--line); border-radius: 5px; font-size: 12px; }
  input[type=checkbox] { accent-color: var(--accent); width: 15px; height: 15px; margin: 0; }
  .engine-settings { display: grid; gap: 17px; border-top: 1px solid var(--line); padding-top: 22px; }
  .values-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(175px, 100%), 1fr)); gap: 14px; }
  .parameter-check { display: flex; align-items: start; gap: 9px; font-size: 13px; min-height: 30px; }
  .parameter-check input { flex: none; margin-top: 3px; }
  .parameter-check .hint { display: block; margin-top: 4px; }
  .parameter-check:has(input:disabled) { color: var(--ink-2); }
  .fine-settings { border-top: 1px solid var(--line); padding-top: 15px; }
  summary { cursor: pointer; font-size: 13px; font-weight: 500; }
  summary::marker { color: var(--accent); }
  .fine-note { display: block; font-weight: 400; color: var(--ink-2); font-size: 12px; margin: 4px 0 0 16px; }
  .fine-content { display: grid; gap: 18px; margin-top: 18px; }
  .time-hint { margin-top: 9px; }
  .summary { display: grid; gap: 19px; }
  .summary dl { margin: 0; }
  .summary-main { display: grid; gap: 14px; }
  .summary-main dt { font-size: 11px; color: var(--ink-2); }
  .summary-main dd { margin: 2px 0 0; font-size: 13px; font-weight: 500; overflow-wrap: anywhere; }
  .summary-details { display: grid; grid-template-columns: 1fr auto; gap: 10px 12px; border-top: 1px solid var(--line); padding-top: 16px; font-size: 12px; }
  .summary-details dt { color: var(--ink-2); }
  .summary-details dd { margin: 0; text-align: right; font-weight: 500; max-width: 135px; }
  .summary-label { grid-column: 1 / -1; font-size: 11px; margin-top: 6px; }
  .summary button { width: 100%; justify-content: center; min-height: 40px; }
  .small-note { margin: 0; font-size: 11px; color: var(--ink-2); }
  .run-domains, pre { max-height: 22rem; overflow: auto; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; }
  .journal { margin-top: 20px; }
  @container (max-width: 850px) { .columns { grid-template-columns: minmax(0, 1fr) 245px; } }
  @container (max-width: 690px) { .columns { grid-template-columns: minmax(0, 1fr); } .page-heading { flex-wrap: wrap; } .summary-main { grid-template-columns: 1fr 1fr; } }
</style>
