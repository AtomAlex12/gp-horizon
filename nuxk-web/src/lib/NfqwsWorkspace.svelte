<script lang="ts">
  import { api, type LogEntry } from './api';
  import { configBlocks, replaceBlock } from './nfqws-config';
  import CodeEditor from './CodeEditor.svelte';
  import { untrack } from 'svelte';

  type Section = 'config' | 'lists' | 'scripts';
  const labels: Record<string, string> = {
    ISP_INTERFACE: 'Сетевой интерфейс', NFQWS_BASE_ARGS: 'Параметры запуска',
    NFQWS_ARGS: 'Базовая стратегия', NFQWS_ARGS_QUIC: 'Стратегия QUIC',
    NFQWS_ARGS_UDP: 'Стратегия UDP', NFQWS_ARGS_CUSTOM: 'Пользовательская стратегия',
    NFQWS_ARGS_IPSET: 'Параметры IPSET', MODE_FILTER: 'Режим работы',
    TCP_PORTS: 'TCP-порты', UDP_PORTS: 'UDP-порты', POLICY_NAME: 'Название политики',
  };
  const groups = [
    { id: 'standard', label: 'Штатные' },
    { id: 'additional', label: 'Дополнительные' },
    { id: 'archive', label: 'Архивы и версии' },
  ];

  let { section }: { section: Section | 'logs' | 'overview' } = $props();
  let files = $state<Awaited<ReturnType<typeof api.nfqwsFiles>>>([]);
  let interfaces = $state<Awaited<ReturnType<typeof api.nfqwsInterfaces>>>([]);
  let logs = $state<LogEntry[]>([]);
  let name = $state('');
  let content = $state('');
  let search = $state('');
  let raw = $state(false);
  let busy = $state(false);
  let loaded = $state(false);
  let error = $state('');
  let feedback = $state('');
  let variable = $state('');
  const drafts = new Map<string, string>();
  const originals = new Map<string, string>();
  let request = 0;
  $effect(() => { const next = section; untrack(() => { void open(next); }); });
  const blocks = $derived(configBlocks(content));
  const selected = $derived((blocks.find(match => match.key === 'ISP_INTERFACE')?.value ?? '').split(/\s+/).filter(Boolean));
  const choices = $derived([...interfaces, ...selected.filter(item => !interfaces.some(iface => iface.name === item)).map(item => ({ name: item, label: 'Из файла', type: '' }))]);

  async function open(next: Section | 'logs' | 'overview') {
    const ticket = ++request;
    if (next === 'overview') return;
    busy = true;
    files = [];
    loaded = false;
    name = '';
    error = '';
    search = '';
    feedback = '';
    try {
      if (next === 'logs') {
        const result = await api.logs();
        if (ticket === request) logs = result;
      } else {
        const result = await api.nfqwsFiles(next);
        if (ticket !== request) return;
        files = result;
        if (next === 'config') {
          const result = await api.nfqwsInterfaces().catch(() => []);
          if (ticket !== request) return;
          interfaces = result;
        }
        if (files[0]) await read(next, files.find(file => file.name === 'nfqws2.conf')?.name ?? files[0].name);
      }
    } catch {
      if (ticket === request) error = 'Не удалось загрузить данные. Проверьте подключение к агенту.';
    } finally {
      if (ticket === request) busy = false;
    }
  }

  async function read(current: Section, file: string) {
    const ticket = ++request;
    name = file;
    content = '';
    loaded = false;
    busy = true;
    error = '';
    try {
      const key = current + '/' + file;
      const text = originals.get(key) ?? (await api.nfqwsFile(current, file)).text;
      if (ticket !== request) return;
      originals.set(key, text);
      content = drafts.get(key) ?? text;
      feedback = '';
      loaded = true;
    } catch {
      if (ticket === request) error = 'Не удалось прочитать файл.';
    } finally {
      if (ticket === request) busy = false;
    }
  }

  function edit(value: string) {
    content = value;
    drafts.set(section + '/' + name, value);
  }

  function setBlock(key: string, value: string) {
    try {
      edit(replaceBlock(content, key, value));
      error = '';
    } catch (reason) { error = reason instanceof Error ? reason.message : String(reason); }
  }

  function toggleInterface(iface: string) {
    setBlock('ISP_INTERFACE', (selected.includes(iface) ? selected.filter(item => item !== iface) : [...selected, iface]).join(' '));
  }
  function download() {
    const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }));
    const link = document.createElement('a');
    link.href = url;
    const readonly = files.find(file => file.name === name)?.read_only;
    link.download = name.replace(/\.gz$/, '') + (readonly ? '' : '.draft');
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
    feedback = 'Черновик скачан. Файл на роутере не изменён.';
  }

  function addBlock() {
    const key = variable.trim();
    if (!/^[A-Z_][A-Z0-9_]*$/.test(key)) { error = 'Имя переменной: заглавные латинские буквы, цифры и подчёркивания.'; return; }
    if (blocks.some(block => block.key === key)) { error = 'Такой блок уже существует.'; return; }
    setBlock(key, '');
    variable = '';
  }
</script>

<section class="card nfqws-workspace" hidden={section === 'overview'}>
  <div class="card-head">
    <h2>{section === 'config' ? 'Конфигурация NFQWS2' : section === 'lists' ? 'Файлы списков NFQWS2' : section === 'scripts' ? 'Скрипты NFQWS2' : 'Журнал NFQWS2'}</h2>
    {#if section === 'lists' || section === 'scripts'}<span class="chip mono">/opt/etc/nfqws2/{section === 'lists' ? 'lists' : 'lua'}</span>{/if}
  </div>
  {#if busy}<p class="hint" role="status">Загрузка…</p>{/if}
  {#if error}<p class="err-text" role="alert">{error}</p>{/if}
  {#if section === 'logs'}
    <div class="row"><span class="hint">События NFQWS2 из журнала агента</span><span class="spacer"></span><button class="ghost" disabled={busy} onclick={() => open('logs')}>Обновить</button></div>
    <pre class="log mono">{logs.filter(entry => /nfqws/i.test(entry.msg)).map(entry => new Date(entry.ts).toLocaleTimeString('ru-RU') + ' ' + entry.level + ' ' + entry.msg).join('\n') || 'Событий NFQWS2 в журнале агента пока нет.'}</pre>
  {:else if section !== 'overview'}
    {@const current = section}
    {#if section === 'config'}
      <nav class="modes row" aria-label="Режим редактирования">
        <button class="ghost" class:active={!raw} aria-pressed={!raw} onclick={() => raw = false}>Конфиг</button>
        <button class="ghost mono" class:active={raw} aria-pressed={raw} onclick={() => raw = true}>{name || 'nfqws2.conf'}</button>
        <span class="spacer"></span>
        {#if files.length > 1}<label class="file-choice">Файл <select disabled={busy} value={name} onchange={event => read('config', event.currentTarget.value)}>{#each files as file}<option value={file.name}>{file.name}</option>{/each}</select></label>{/if}
      </nav>
    {/if}
    <div class:catalog={section !== 'config'} class="workspace">
      {#if section !== 'config'}
        <nav class="file-nav" aria-label="Файлы NFQWS2">
          <label>Поиск файла <input type="text" bind:value={search} placeholder="Например, ipset" /></label>
          {#each section === 'lists' ? groups : [{ id: '', label: 'Lua-скрипты' }] as group}
            {@const items = files.filter(file => (section === 'scripts' || file.group === group.id) && file.name.toLowerCase().includes(search.toLowerCase()))}
            {#if items.length}
              <strong>{group.label}</strong>
              {#each items as file}
                <button class="ghost mono" class:active={file.name === name} aria-pressed={file.name === name} disabled={busy} onclick={() => read(current, file.name)}>{file.name}</button>
              {/each}
            {/if}
          {/each}
          {#if files.length && !files.some(file => file.name.toLowerCase().includes(search.toLowerCase()))}<p class="hint">Файлы не найдены.</p>{/if}
        </nav>
      {/if}
      <div class="editor">
        {#if loaded}
          {@const readonly = files.find(file => file.name === name)?.read_only ?? false}
          {#if section === 'config' && !raw}
            {#each [...Object.keys(labels), ...blocks.map(block => block.key).filter(key => !labels[key])] as key}
              {@const match = blocks.find(block => block.key === key)}
              {@const value = match?.value ?? ''}
              <fieldset>
                <legend>{labels[key] || key} <span>· {key}</span></legend>
                {#if key === 'ISP_INTERFACE'}
                  <details class="interfaces">
                    <summary>{selected.join(', ') || 'Выберите интерфейсы'}</summary>
                    <div class="interface-options">
                      {#each choices as iface}<label><input type="checkbox" checked={selected.includes(iface.name)} onchange={() => toggleInterface(iface.name)} /> <span class="mono">{iface.name}</span> <span>{iface.label || iface.type}</span></label>{/each}
                      {#if !interfaces.length}<p class="hint">Данные Keenetic недоступны. Укажите имя в полном файле.</p>{/if}
                    </div>
                  </details>
                  <p class="hint mono">ISP_INTERFACE="{selected.join(' ')}"</p>
                {:else if key === 'MODE_FILTER'}
                  <select aria-label={labels[key]} value={value} onchange={event => setBlock(key, event.currentTarget.value)}>
                    <option value="">Не задано в файле</option>
                    {#each [...new Set(['auto', 'list', 'all', ...(value ? [value] : [])])] as mode}<option value={mode}>{mode}</option>{/each}
                  </select>
                {:else if ['TCP_PORTS', 'UDP_PORTS', 'POLICY_NAME'].includes(key)}
                  <input type="text" class="mono" aria-label={labels[key]} {value} placeholder="Не задано в файле" onchange={event => setBlock(key, event.currentTarget.value)} />
                {:else}
                  <CodeEditor label={labels[key] || key} {value} rows={Math.max(key === 'NFQWS_ARGS_IPSET' ? 2 : 4, Math.min(12, value.split('\n').length))} placeholder="Не задано в файле" onchange={value => setBlock(key, value)} />
                {/if}
              </fieldset>
            {/each}
            <div class="add-block"><label>Имя переменной<input type="text" bind:value={variable} placeholder="NFQWS_ARGS_CUSTOM" /></label><button class="ghost" onclick={addBlock}>Добавить блок</button></div>
          {:else}
            <div class="file-caption"><span class="mono">{name}</span><span class="hint">{readonly ? 'Просмотр распакованного файла' : 'Черновик в текущем сеансе'}</span></div>
            <CodeEditor label={name} value={content} rows={section === 'config' ? 26 : 22} {readonly} onchange={edit} />
          {/if}
          <div class="actions row">
            <button onclick={download}>{readonly ? 'Скачать распакованный файл' : 'Скачать черновик'}</button>
            {#if !readonly}<button class="ghost" disabled={content === originals.get(current + '/' + name)} onclick={() => { edit(originals.get(current + '/' + name) ?? ''); feedback = 'Правки отменены.'; }}>Отменить правки</button>{/if}
            <span class="hint">Запись файлов на роутер пока недоступна.</span>
          </div>
          {#if feedback}<p class="hint" role="status">{feedback}</p>{/if}
        {:else if !busy && !error}<p class="hint">Файлы не найдены.</p>{/if}
      </div>
    </div>
  {/if}
</section>

<style>
  .nfqws-workspace { padding: 22px 24px; }
  .nfqws-workspace[hidden] { display: none; }
  .card-head { margin-bottom: 18px; }
  .modes { padding-bottom: 14px; border-bottom: 1px solid var(--line); margin-bottom: 24px; }
  .active { background: var(--accent-soft) !important; color: var(--accent) !important; border-color: transparent !important; }
  .file-choice { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--muted); }
  .catalog { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 20px; }
  .file-nav { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
  .file-nav label { display: grid; gap: 6px; margin-bottom: 4px; }
  .file-nav strong { color: var(--muted); font-size: 12px; margin-top: 8px; }
  .file-nav button { justify-content: flex-start; overflow-wrap: anywhere; white-space: normal; font-size: 12px; padding: 6px 10px; }
  .editor { min-width: 0; }
  fieldset { border: 1px solid var(--line); border-radius: 8px; margin: 0 0 18px; padding: 12px 14px 14px; min-width: 0; }
  legend { color: var(--ink-2); font-size: 13px; padding: 0 5px; }
  legend span { color: var(--muted); font-size: 12px; }
  fieldset > select { width: 100%; }
  .interfaces { max-width: 420px; }
  summary { cursor: pointer; padding: 10px 12px; background: var(--surface-2); border: 1px solid var(--line); border-radius: 7px; }
  .interface-options { padding: 5px; border: 1px solid var(--line); border-radius: 7px; margin-top: 5px; }
  .interface-options label { display: flex; align-items: center; gap: 10px; padding: 7px; }
  .interface-options label:has(input:checked) { background: var(--accent-soft); color: var(--accent); border-radius: 5px; }
  input[type=checkbox] { accent-color: var(--accent); }
  fieldset > .hint { margin-top: 10px; }
  .add-block { display: grid; gap: 10px; max-width: 760px; padding-top: 6px; }
  .add-block label { display: grid; gap: 8px; }
  .add-block button { justify-self: start; }
  .actions { margin: 20px 0 8px; }
  .file-caption { display: flex; gap: 12px; flex-wrap: wrap; margin-bottom: 10px; }
  .log { white-space: pre-wrap; overflow-wrap: anywhere; min-height: 360px; max-height: 65vh; overflow: auto; padding: 16px; border: 1px solid var(--line); background: var(--surface-2); border-radius: 8px; }
  @media (max-width: 800px) { .catalog { grid-template-columns: minmax(0, 1fr); } .file-nav { max-height: 240px; overflow: auto; } .nfqws-workspace { padding: 16px; } .file-choice { width: 100%; } }
</style>
