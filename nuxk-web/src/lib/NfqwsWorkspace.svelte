<script lang="ts">
  import { api, type LogEntry } from './api';

  type Section = 'config' | 'lists' | 'scripts';
  const sections = [
    { id: 'config', label: 'Настройки' },
    { id: 'lists', label: 'Списки' },
    { id: 'logs', label: 'Логи' },
    { id: 'scripts', label: 'Скрипты' },
  ] as const;
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
  const assignment = /^([A-Z_][A-Z0-9_]*)="((?:[^"\\\n]|\\\n)*)"$/gm;

  let section = $state<Section | 'logs' | null>(null);
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
  const drafts = new Map<string, string>();
  const blocks = $derived([...content.matchAll(assignment)]);
  const selected = $derived((blocks.find(match => match[1] === 'ISP_INTERFACE')?.[2] ?? '').split(/\s+/).filter(Boolean));
  const choices = $derived([...interfaces, ...selected.filter(item => !interfaces.some(iface => iface.name === item)).map(item => ({ name: item, label: 'Из файла', type: '' }))]);

  async function open(next: Section | 'logs') {
    busy = true;
    section = next;
    files = [];
    loaded = false;
    name = '';
    error = '';
    search = '';
    try {
      if (next === 'logs') {
        logs = await api.logs();
      } else {
        files = await api.nfqwsFiles(next);
        if (next === 'config') interfaces = await api.nfqwsInterfaces().catch(() => []);
        if (files[0]) await read(next, files[0].name);
      }
    } catch {
      error = 'Не удалось загрузить данные. Проверьте подключение к агенту.';
    } finally {
      busy = false;
    }
  }

  async function read(current: Section, file: string) {
    name = file;
    content = '';
    loaded = false;
    busy = true;
    error = '';
    try {
      content = drafts.get(current + '/' + file) ?? (await api.nfqwsFile(current, file)).text;
      loaded = true;
    } catch {
      error = 'Не удалось прочитать файл.';
    } finally {
      busy = false;
    }
  }

  function edit(value: string) {
    content = value;
    drafts.set(section + '/' + name, value);
  }

  function setBlock(match: RegExpMatchArray, value: string) {
    if (/["\\]/.test(value)) {
      error = 'Для кавычек и обратных косых черт откройте полный файл.';
      return;
    }
    error = '';
    const start = match.index ?? 0;
    const joined = value.split('\n').join(' ' + String.fromCharCode(92) + '\n');
    edit(content.slice(0, start) + match[1] + '="' + joined + '"' + content.slice(start + match[0].length));
  }

  function toggleInterface(match: RegExpMatchArray, iface: string) {
    setBlock(match, (selected.includes(iface) ? selected.filter(item => item !== iface) : [...selected, iface]).join(' '));
  }
</script>

<section class="card">
  <div class="card-head"><h2>Управление NFQWS2</h2></div>
  <nav class="row" aria-label="Управление NFQWS2">
    {#each sections as item}
      <button class:ghost={section !== item.id} aria-pressed={section === item.id} disabled={busy} onclick={() => open(item.id)}>{item.label}</button>
    {/each}
  </nav>
  {#if busy}<p class="hint">Загрузка…</p>{/if}
  {#if error}<p class="err-text" role="status">{error}</p>{/if}
  {#if section === 'logs'}
    <button class="ghost sm" disabled={busy} onclick={() => open('logs')}>Обновить</button>
    {#if logs.length}
      <pre class="mono">{logs.map(entry => new Date(entry.ts).toLocaleTimeString('ru-RU') + ' ' + entry.level + ' ' + entry.msg).join('\n')}</pre>
    {:else if !busy}<p class="hint">Журнал агента пуст.</p>{/if}
  {:else if section}
    {@const current = section}
    <div class:catalog={section === 'lists'} class="workspace">
      <nav class="file-nav" aria-label="Файлы">
        {#if section === 'lists'}
          <label>Поиск файла <input type="text" bind:value={search} /></label>
          {#each groups as group}
            {@const items = files.filter(file => file.group === group.id && file.name.toLowerCase().includes(search.toLowerCase()))}
            {#if items.length}
              <strong>{group.label}</strong>
              {#each items as file}
                <button class="mono sm" class:ghost={file.name !== name} aria-pressed={file.name === name} disabled={busy} onclick={() => read(current, file.name)}>{file.name}</button>
              {/each}
            {/if}
          {/each}
        {:else}
          {#each files as file}
            <button class="mono sm" class:ghost={file.name !== name} aria-pressed={file.name === name} disabled={busy} onclick={() => read(current, file.name)}>{file.name}</button>
          {/each}
        {/if}
      </nav>
      <div class="editor">
        {#if loaded}
          {@const readonly = files.find(file => file.name === name)?.read_only ?? false}
          <p class="hint">{readonly ? 'Просмотр распакованного файла' : 'Черновик в текущем сеансе'}</p>
          {#if section === 'config'}
            <div class="row modes">
              <button class:ghost={raw} onclick={() => raw = false}>Конфиг</button>
              <button class:ghost={!raw} onclick={() => raw = true}>Полный файл</button>
            </div>
          {/if}
          {#if section === 'config' && !raw}
            {#each Object.entries(labels) as [key, label]}
              {@const match = blocks.find(block => block[1] === key)}
              {#if match}
                <fieldset>
                  <legend>{label} · {key}</legend>
                  {#if key === 'ISP_INTERFACE'}
                    <details>
                      <summary>{selected.join(', ') || 'Выберите интерфейсы'}</summary>
                      {#each choices as iface}
                        <label><input type="checkbox" checked={selected.includes(iface.name)} onchange={() => toggleInterface(match, iface.name)} /> {iface.name} {iface.label || iface.type}</label>
                      {/each}
                      {#if !interfaces.length}<p class="hint">Данные Keenetic недоступны. Укажите имя в полном файле.</p>{/if}
                    </details>
                  {:else}
                    <textarea class="mono" aria-label={label} rows={Math.max(1, match[2].split('\n').length)} value={match[2].replace(/\\\n/g, '\n')} onchange={(event) => setBlock(match, event.currentTarget.value)}></textarea>
                  {/if}
                </fieldset>
              {/if}
            {/each}
          {:else}
            <textarea class="mono" aria-label={name} rows="16" spellcheck="false" {readonly} value={content} oninput={(event) => edit(event.currentTarget.value)}></textarea>
          {/if}
        {:else if !busy && !error}
          <p class="hint">Файлы не найдены.</p>
        {/if}
      </div>
    </div>
  {/if}
</section>

<style>
  .workspace, pre, .modes { margin-top: 12px; }
  .catalog { display: grid; grid-template-columns: minmax(150px, 220px) minmax(0, 1fr); gap: 16px; }
  .file-nav { display: flex; flex-wrap: wrap; gap: 6px; align-content: start; }
  .catalog .file-nav { display: grid; }
  .file-nav strong { color: var(--muted); margin-top: 8px; }
  .file-nav input, textarea { width: 100%; }
  fieldset { border: 1px solid var(--line); border-radius: 8px; margin: 10px 0; min-width: 0; }
  legend { color: var(--muted); }
  summary { cursor: pointer; padding: 6px; }
  details label { display: block; }
  pre { white-space: pre-wrap; max-height: 30rem; overflow: auto; }
  @media (max-width: 650px) { .catalog { grid-template-columns: minmax(0, 1fr); } }
</style>
