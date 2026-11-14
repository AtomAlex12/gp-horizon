<script lang="ts">
  import { onMount } from 'svelte';
  import { gp2, gp2Pages, gp2URL, type GP } from './gp2';
  import { copyText } from './clipboard';

  let { section }: { section: 'history' | 'strategies' | 'service' } = $props();
  let error = $state('');
  let feedback = $state('');
  let busy = $state(false);
  let history = $state<GP['RunDetail'][]>([]);
  let selectedRun = $state<GP['RunDetail']>();
  let log = $state<GP['LogPage']>();
  let logLines = $state<GP['LogEntry'][]>([]);
  let strategies = $state<GP['Strategy'][]>([]);
  let bundles = $state<GP['Bundle'][]>([]);
  let selected = $state<GP['Strategy']>();
  let mode = $state<'strategies' | 'bundles'>('strategies');
  let match = $state('any');
  let domains = $state('');
  let protocol = $state('');
  let engine = $state('');
  let query = $state('');
  let milestones = $state<GP['Milestone'][]>([]);
  let milestone = $state<number>();
  let loadedMilestone = $state<number>();
  let assignments = $state<Record<string, GP['Strategy']>>({});
  const chosen = $derived(Object.entries(assignments).map(([key, strategy]) => ({ key, strategy, domain: key.split('|')[1] })));
  let core = $state<GP['CoreStatus']>();
  let source = $state<GP['Source']>();
  let sourceUpdate = $state<GP['SourceUpdate']>();
  let categories = $state<GP['Category'][]>([]);
  let category = $state('');
  let sourceDomains = $state<string[]>([]);
  let lists = $state<GP['DomainList'][]>([]);
  let listId = $state<number>();
  let listName = $state('');
  let listDomains = $state('');
  let editable = $state(true);
  let historyTotal = $state(0);
  let offset = $state(0);
  let total = $state(0);
  const limit = 40;

  function split(value: string) {
    return value.split(/[\s,]+/).filter(Boolean);
  }

  async function perform(work: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = '';
    feedback = '';
    try { await work(); }
    catch (reason) { error = reason instanceof Error ? reason.message : String(reason); }
    finally { busy = false; }
  }

  async function readHistory() {
    const page = await gp2<GP['RunPage']>('data/runs?limit=' + limit + '&offset=' + offset);
    history = page.items;
    historyTotal = page.total;
  }

  async function readRun(item: GP['RunDetail']) {
    selectedRun = await gp2<GP['RunDetail']>('data/runs/' + encodeURIComponent(item.id));
    logLines = [];
    log = undefined;
    await readLog();
  }

  async function readLog() {
    if (!selectedRun) return;
    const after = log?.next_after;
    log = await gp2<GP['LogPage']>('data/runs/' + encodeURIComponent(selectedRun.id) + '/log?limit=200' +
      (after ? '&after=' + encodeURIComponent(after) : ''));
    logLines = [...logLines, ...log.entries];
  }

  async function catalog() {
    const params = new URLSearchParams({ match, offset: String(offset), limit: String(limit) });
    if (milestone !== undefined) params.set('milestone_id', String(milestone));
    for (const domain of split(domains)) params.append('domains', domain);
    selected = undefined;
    if (mode === 'strategies') {
      if (protocol) params.set('protocol', protocol);
      if (engine) params.set('engine_code', engine);
      if (query.trim()) params.set('q', query.trim());
      params.set('sort', 'family');
      const page = await gp2<GP['StrategyPage']>('data/strategies?' + params);
      strategies = page.items;
      loadedMilestone = page.milestone_id;
      total = page.total;
    } else {
      params.set('view', domains.trim() ? 'confirmed' : 'all');
      const page = await gp2<GP['BundlePage']>('data/bundles?' + params);
      bundles = page.items;
      loadedMilestone = page.milestone_id;
      total = page.total;
    }
  }

  async function readService() {
    [core, lists] = await Promise.all([gp2<GP['CoreStatus']>('service/status'), gp2Pages<GP['DomainList']>('data/domain-lists')]);
  }

  function readSource() {
    return perform(async () => {
      source = await gp2<GP['Source']>('data/sources/v2fly');
      categories = source.state === 'ready' ? await gp2Pages<GP['Category']>('data/sources/v2fly/categories') : [];
    });
  }

  function selectList(item?: GP['DomainList']) {
    return perform(async () => {
      const entries = item ? await gp2Pages<GP['DomainEntry']>('data/domain-lists/' + item.id + '/domains') : [];
      listId = item?.id;
      listName = item?.name ?? '';
      listDomains = entries.map(entry => entry.domain).join('\n');
      editable = !item || item.kind === 'user';
    });
  }

  function saveList() {
    return perform(async () => {
      const path = 'data/domain-lists' + (listId === undefined ? '' : '/' + listId);
      const saved = await gp2<GP['DomainList']>(path, listId === undefined ? 'POST' : 'PUT', {
        name: listName.trim(), domains: split(listDomains).map(domain => ({ domain })),
      });
      listId = saved.id;
      await readService();
      feedback = 'Список сохранён.';
    });
  }

  function maintenance(path: string, method: string, question?: string) {
    if (question && !window.confirm(question)) return;
    void perform(async () => {
      await gp2(path, method);
      await readService();
      feedback = 'Операция завершена.';
    });
  }

  function restoreFile(file?: File) {
    if (!file || !window.confirm('Заменить данные GP Plane содержимым выбранной копии?')) return;
    void perform(async () => {
      const response = await fetch(gp2URL('backup/restore-file'), { method: 'POST', body: file, headers: { 'Content-Type': 'application/octet-stream' } });
      if (!response.ok) {
        const detail = await response.json().catch(() => null);
        throw new Error(detail?.error?.message ?? 'Ошибка восстановления: HTTP ' + response.status);
      }
      await readService();
      feedback = 'Данные восстановлены.';
    });
  }

  function page(delta: number) {
    return perform(async () => {
      const previous = offset;
      offset += delta * limit;
      try {
        if (section === 'history') await readHistory();
        else await catalog();
      } catch (reason) {
        offset = previous;
        throw reason;
      }
    });
  }

  onMount(() => {
    void perform(async () => {
      if (section === 'history') await readHistory();
      if (section === 'service') await readService();
      if (section === 'strategies') {
        milestones = await gp2Pages<GP['Milestone']>('data/milestones');
        await catalog();
      }
    });
  });
</script>

{#if error}<p class="err-text" role="alert">{error}</p>{/if}
{#if feedback}<p class="ok-text" role="status">{feedback}</p>{/if}
{#if section === 'history'}
  <section class="card">
    <div class="card-head"><h2>История поиска</h2><button class="ghost" disabled={busy} onclick={() => perform(readHistory)}>Обновить</button></div>
    {#each history as item (item.id)}
      <button class="ghost history" disabled={busy} onclick={() => perform(() => readRun(item))}>
        {item.engine?.name ?? 'Движок неизвестен'} · {item.status} · {item.started_at ? new Date(item.started_at).toLocaleString('ru-RU') : '—'} · {item.domains?.join(', ') ?? '—'}
      </button>
    {:else}<p class="hint">{busy ? 'Загрузка…' : 'Прогонов пока нет.'}</p>{/each}
    <div class="row paging"><button class="ghost" disabled={busy || offset === 0} onclick={() => page(-1)}>Назад</button><button class="ghost" disabled={busy || offset + limit >= historyTotal} onclick={() => page(1)}>Далее</button></div>
  </section>
  {#if selectedRun}
    <section class="card">
      <div class="card-head"><h2>{selectedRun.engine?.name ?? 'Движок неизвестен'} · {selectedRun.status}</h2></div>
      <p>{selectedRun.domains?.join(', ') ?? '—'} · веха {selectedRun.milestone_id}</p>
      {#if selectedRun.error}<p class="err-text">{JSON.stringify(selectedRun.error)}</p>{/if}
      <pre>{logLines.map(entry => entry.message).join('\n')}</pre>
      {#if log?.truncated}<p class="hint">Часть журнала была обрезана.</p>{/if}
      <div class="row">
        {#if log?.next_after && log.entries.length}<button class="ghost" disabled={busy} onclick={() => perform(readLog)}>Ещё строки</button>{/if}
        {#if selectedRun.resume_available}<button disabled={busy} onclick={() => perform(async () => {
          await gp2('engines/blockcheck2-gp/runs/' + encodeURIComponent(selectedRun!.id) + '/resume', 'POST');
          await readRun(selectedRun!);
          feedback = 'Запрос на продолжение принят. Текущий статус доступен в «Прогонах».';
        })}>Продолжить GP-поиск</button>{/if}
        <button class="warn" disabled={busy} onclick={() => {
          if (!window.confirm('Удалить запись истории поиска?')) return;
          void perform(async () => {
            await gp2('data/runs/' + encodeURIComponent(selectedRun!.id), 'DELETE');
            selectedRun = undefined;
            await readHistory();
          });
        }}>Удалить запись</button>
      </div>
    </section>
  {/if}
{:else if section === 'strategies'}
  <section class="card">
    <div class="card-head"><h2>Каталог GP Plane</h2></div>
    <form class="filters" onsubmit={event => { event.preventDefault(); void perform(async () => { offset = 0; await catalog(); }); }}>
      <label>Каталог<select bind:value={mode}><option value="strategies">Стратегии</option><option value="bundles">Готовые комплекты</option></select></label>
      <label>Веха<select bind:value={milestone}><option value={undefined}>Текущая</option>{#each milestones as item}<option value={item.id}>{item.id}</option>{/each}</select></label>
      <label>Домены<input type="text" bind:value={domains} placeholder="example.org, example.com" /></label>
      <label>Совпадение<select bind:value={match}><option value="any">Любой домен</option><option value="all">Все домены</option></select></label>
      {#if mode === 'strategies'}
        <label>Протокол<input type="text" bind:value={protocol} /></label>
        <label>Движок<select bind:value={engine}><option value="">Все</option><option value="blockcheck2">Stock</option><option value="blockcheck2-gp">GP</option></select></label>
        <label>Поиск по аргументам<input type="text" bind:value={query} /></label>
      {/if}
      <button disabled={busy}>Найти</button>
    </form>
    <p class="hint">Результатов: {total} · веха {loadedMilestone ?? '—'}</p>
    {#if mode === 'strategies'}
      <div class="catalog">
        <div>
          {#each strategies as item, index (item.id)}
            {#if index === 0 || strategies[index - 1].family !== item.family}<h3>{item.family}</h3>{/if}
            <button class="history" class:ghost={selected?.id !== item.id} onclick={() => selected = item}>{item.protocol} · {item.results.map(result => result.domain).join(', ') || 'Без подтверждённых доменов'}</button>
          {/each}
        </div>
        {#if selected}
          <section>
            <h3>{selected.family} · {selected.protocol}</h3>
            <p class="hint">{selected.family_reason}</p>
            {#each selected.results as result}<p>{result.domain} · {result.engines.map(item => item.name).join(', ') || 'источник неизвестен'}</p>{/each}
                        <pre>{selected.args}</pre>
            <div class="row">
              {#each selected.results as result (result.domain)}
                {@const key = selected.protocol + '|' + result.domain}
                {@const strategy = selected}
                <button class="ghost sm" disabled={assignments[key]?.id === strategy.id}
                  onclick={() => { assignments[key] = strategy; }}>
                  {assignments[key]?.id === strategy.id ? 'Выбрано' : assignments[key] ? 'Заменить для' : 'Добавить для'} {result.domain}
                </button>
              {/each}
            </div>
            <div class="row">
              <button class="ghost" onclick={() => perform(async () => { await copyText(selected!.args); feedback = 'Аргументы скопированы.'; })}>Копировать аргументы</button>
              {#if selected.archive_available}<a href={gp2URL('data/strategies/' + encodeURIComponent(selected.id) + '/archive')}>Скачать комплект</a>
              {:else}<span class="hint">Полный комплект недоступен.</span>{/if}
            </div>
          </section>
        {/if}
      </div>
    {:else}
      {#each bundles as item (item.id)}
        <details>
          <summary>{item.name || 'Комплект'} · {item.results.length} результатов</summary>
          <p>{item.results.map(result => result.domain).join(', ') || 'Подтверждённых доменов нет.'}</p>
          {#each item.sources as source}<p class="hint">{source.name || 'Источник без названия'}</p>{/each}
          {#if item.archive_available}<a href={gp2URL('data/bundles/' + item.id + '/archive')}>Скачать комплект</a>
          {:else}<p class="hint">Полный комплект недоступен.</p>{/if}
        </details>
      {/each}
    {/if}
    <div class="row paging"><button class="ghost" disabled={busy || offset === 0} onclick={() => page(-1)}>Назад</button><button class="ghost" disabled={busy || offset + limit >= total} onclick={() => page(1)}>Далее</button></div>
    {#if chosen.length}
      <section class="selection">
        <h3>Выбранные стратегии</h3>
        {#each [...new Set(chosen.map(item => item.strategy.protocol))] as protocol}
          <p class="hint">{protocol} · выбрано доменов: {chosen.filter(item => item.strategy.protocol === protocol).length}</p>
        {/each}
        {#each chosen as item (item.key)}
          <div class="row">
            <span>{item.domain} · {item.strategy.protocol} · {item.strategy.family} · веха {item.strategy.milestone_id}</span>
            <button class="ghost sm" onclick={() => { delete assignments[item.key]; }}>Убрать</button>
          </div>
        {/each}
        <p class="hint">Это черновик назначений. Core экспортирует отдельные стратегии и сохранённые комплекты.</p>
      </section>
    {/if}
  </section>
{:else}
  <section class="card">
    <div class="card-head"><h2>Списки доменов</h2><button class="ghost" disabled={busy} onclick={() => selectList()}>Новый список</button></div>
    <div class="catalog">
      <div>{#each lists as item (item.id)}<button class="history" class:ghost={listId !== item.id} disabled={busy} onclick={() => selectList(item)}>{item.name}</button>{/each}</div>
      <form onsubmit={event => { event.preventDefault(); void saveList(); }}>
        <label>Имя<input type="text" bind:value={listName} readonly={!editable} required /></label>
        <label>Домены<textarea rows="9" class="mono" bind:value={listDomains} readonly={!editable}></textarea></label>
        <div class="row">
          {#if editable}<button disabled={busy || !listName.trim()}>Сохранить</button>{/if}
          {#if editable && listId !== undefined}<button type="button" class="warn" disabled={busy} onclick={() => {
            if (!window.confirm('Удалить список доменов?')) return;
            void perform(async () => {
              await gp2('data/domain-lists/' + listId, 'DELETE');
              listId = undefined;
              listName = listDomains = '';
              await readService();
            });
          }}>Удалить</button>{/if}
        </div>
      </form>
    </div>
  </section>
  <section class="card">
    <div class="card-head"><h2>Обслуживание Core</h2><button class="ghost" disabled={busy} onclick={() => perform(readService)}>Обновить статус</button></div>
    {#if core}<p>Core {core.core_version} · API {core.api_version}{core.operation ? ' · ' + core.operation : ''}</p>{/if}
    <div class="row">
      <button class="ghost" disabled={busy || core?.busy} onclick={() => maintenance('backup','POST',core?.backup_exists ? 'Заменить сохранённую копию?' : undefined)}>Создать копию</button>
      {#if core?.backup_exists}
        <a href={gp2URL('backup/file')}>Скачать копию</a>
        <button class="warn" disabled={busy || core.busy} onclick={() => maintenance('backup/restore','POST','Заменить данные сохранённой копией?')}>Восстановить копию</button>
        <button class="ghost" disabled={busy || core.busy} onclick={() => maintenance('backup','DELETE','Удалить сохранённую копию?')}>Удалить копию</button>
      {/if}
    </div>
    <label class="upload">Восстановить из файла <input type="file" accept=".zip" disabled={busy || core?.busy} onchange={event => {
      restoreFile(event.currentTarget.files?.[0]);
      event.currentTarget.value = '';
    }} /></label>
  </section>
  <section class="card">
    <div class="card-head"><h2>Источник доменов v2fly</h2></div>
    <div class="row">
      <button class="ghost" disabled={busy} onclick={readSource}>Проверить состояние</button>
      <button class="ghost" disabled={busy} onclick={() => perform(async () => {
        sourceUpdate = await gp2<GP['SourceUpdate']>('data/sources/v2fly/update');
      })}>Проверить обновление</button>
      {#if sourceUpdate?.update_available}
        <button disabled={busy || core?.busy} onclick={() => perform(async () => {
          source = await gp2<GP['Source']>('data/sources/v2fly/update', 'POST');
          sourceUpdate = undefined;
          categories = await gp2Pages<GP['Category']>('data/sources/v2fly/categories');
        })}>Обновить источник</button>
      {/if}
    </div>
    {#if source}<p class="hint">{source.state} {source.installed_revision ?? ''} {source.error ?? ''}</p>{/if}
    {#if sourceUpdate}<p class="hint">{sourceUpdate.update_available ? 'Доступна новая ревизия' : 'Обновлений нет'}</p>{/if}
    {#if categories.length}
      <label>Категория
        <select bind:value={category} disabled={busy} onchange={() => perform(async () => {
          sourceDomains = category ? await gp2Pages<string>('data/sources/v2fly/categories/' + encodeURIComponent(category) + '/domains') : [];
        })}>
          <option value="">Выберите категорию</option>
          {#each categories as item (item.id)}<option value={item.id}>{item.name}</option>{/each}
        </select>
      </label>
      {#if sourceDomains.length}<pre>{sourceDomains.join('\n')}</pre>{/if}
    {/if}
  </section>
  <section class="card">
    <div class="card-head"><h2>Вехи результатов</h2></div>
    <button class="ghost" disabled={busy} onclick={() => perform(async () => {
      const page = await gp2<GP['MilestonePage']>('data/milestones?limit=1');
      loadedMilestone = page.current_id;
    })}>Показать текущую</button>
    {#if loadedMilestone !== undefined}<p>Текущая веха {loadedMilestone}</p>{/if}
    <button class="ghost" disabled={busy || core?.busy} onclick={() => {
      if (!window.confirm('Начать новую веху для следующих результатов?')) return;
      void perform(async () => {
        await gp2('data/milestones', 'POST');
        loadedMilestone = (await gp2<GP['MilestonePage']>('data/milestones?limit=1')).current_id;
      });
    }}>Создать веху</button>
  </section>
{/if}

<style>
  .card + .card, .paging, .upload { margin-top: 14px; }
  .filters, .catalog { display: grid; gap: 12px; align-items: start; }
  .filters { grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); align-items: end; }
  .catalog { grid-template-columns: minmax(180px, 1fr) minmax(0, 2fr); }
  label { display: grid; gap: 4px; }
  form label + label, form .row { margin-top: 8px; }
  .filters label + label { margin: 0; }
  .history { display: block; width: 100%; text-align: left; white-space: normal; margin: 4px 0; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 25rem; overflow: auto; }
  details { margin: 8px 0; }
  .selection { margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--line); }
  @media (max-width: 760px) { .catalog { grid-template-columns: 1fr; } }
</style>
