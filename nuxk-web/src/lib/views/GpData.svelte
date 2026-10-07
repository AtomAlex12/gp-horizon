<script lang="ts">
  // «Данные GP»: what GP keeps besides its runs — its domain lists, the v2fly
  // categories, its backups — and the core itself with its common settings.
  // Every write here is GP's own call through the controller; GP's password
  // stays with the controller.
  import {
    gp,
    LIST_KIND,
    type BackupSnapshot,
    type DomainList,
    type RunSettings,
    type ServiceStatus,
    type V2flyStorage,
    type CoreStatus,
  } from '../gp';
  import { splitDomains } from '../ui';

  let lists = $state<DomainList[] | null>(null);
  let storage = $state<V2flyStorage | null>(null);
  let backups = $state<BackupSnapshot[] | null>(null);
  let service = $state<ServiceStatus | null>(null);
  let core = $state<CoreStatus | null>(null);
  let settings = $state<RunSettings | null>(null);
  let err = $state('');
  let msg = $state('');
  let busy = $state('');

  const fail = (e: unknown) => (err = e instanceof Error ? e.message : String(e));
  async function load() {
    const tasks: [Promise<unknown>, (v: never) => void][] = [
      [gp.domainLists(), (r: { lists: DomainList[] }) => (lists = r.lists)],
      [gp.v2flyStorage(), (r: V2flyStorage) => (storage = r)],
      [gp.backups(), (r: { backups: BackupSnapshot[] }) => (backups = r.backups.slice().sort((a, b) => (a.created_at < b.created_at ? 1 : -1)))],
      [gp.service(), (r: ServiceStatus) => (service = r)],
      [gp.status(), (r: CoreStatus) => (core = r)],
    ];
    await Promise.all(tasks.map(([p, set]) => p.then(set as (v: unknown) => void, fail)));
    if (!settings) gp.runSettings().then((s) => (settings = s), fail);
  }
  $effect(() => {
    void load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  });

  async function act(what: string, fn: () => Promise<unknown>, done: string) {
    busy = what;
    err = '';
    msg = '';
    try {
      await fn();
      msg = done;
      await load();
    } catch (e) {
      fail(e);
    } finally {
      busy = '';
    }
  }

  // --- lists
  let editing = $state<{ id: string; name: string; text: string; kind: DomainList['kind'] } | null>(null);
  let peek = $state<Record<string, boolean>>({});
  const newList = () => (editing = { id: '', name: '', text: '', kind: 'user' });
  const edit = (l: DomainList) => (editing = { id: l.list_id, name: l.name, text: l.domains.join('\n'), kind: l.kind });
  function saveList() {
    const e = editing;
    if (!e) return;
    const domains = splitDomains(e.text);
    // a new list's id: from its name, latin letters only
    const id = e.id || 'user-' + (e.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || Date.now().toString(36));
    void act('list', () => gp.saveDomainList({ list_id: id, kind: e.kind, name: e.name.trim(), domains }), `Список «${e.name}» сохранён: ${domains.length} доменов.`).then(() => {
      if (!err) editing = null;
    });
  }
  function removeList(l: DomainList) {
    if (!confirm(`Удалить список «${l.name}» (${l.domains.length} доменов)? Найденные стратегии останутся.`)) return;
    void act('list', () => gp.deleteDomainLists([l.list_id]), `Список «${l.name}» удалён.`);
  }

  // --- backups
  let fileInput = $state<HTMLInputElement>();
  function restore(b: BackupSnapshot) {
    if (!confirm(`Восстановить данные GP на ${when(b.created_at)}? Прогоны, стратегии и списки после этой даты пропадут.`)) return;
    void act('backup', () => gp.restoreBackup(b.snapshot_id), 'Данные GP восстановлены из бэкапа.');
  }
  function removeBackup(b: BackupSnapshot) {
    if (!confirm(`Удалить бэкап от ${when(b.created_at)}?`)) return;
    void act('backup', () => gp.deleteBackup(b.snapshot_id), 'Бэкап удалён.');
  }
  function upload(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!f) return;
    void act('backup', () => gp.uploadBackup(f), `Бэкап «${f.name}» загружен — его можно восстановить из списка.`);
    if (fileInput) fileInput.value = '';
  }

  // --- settings
  function saveSettings() {
    if (!settings) return;
    const s = settings;
    void act('settings', () => gp.saveRunSettings(s).then((r) => (settings = r)), 'Настройки GP сохранены.');
  }

  const when = (s?: string) => (s ? new Date(s).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' }) : '—');
  const size = (b?: number) => (b ? (b > 1 << 20 ? (b / (1 << 20)).toFixed(1) + ' МБ' : Math.ceil(b / 1024) + ' КБ') : '—');
  const STORAGE: Record<string, { label: string; chip: string }> = {
    ready: { label: 'готовы', chip: 'ok' },
    missing: { label: 'не скачаны', chip: 'deg' },
    preparing: { label: 'скачиваются', chip: 'acc' },
    error: { label: 'ошибка', chip: 'warn' },
    unknown: { label: 'неизвестно', chip: '' },
  };
</script>

{#if err}<div class="banner warn">{err}</div>{/if}
{#if msg}<div class="banner acc">{msg}</div>{/if}

<div class="grid g2">
  <section class="card">
    <div class="card-head">
      <h2>Списки GP</h2>
      <span class="spacer"></span>
      <button class="sm" onclick={newList} disabled={!!editing}>Новый список</button>
    </div>
    <p class="hint">
      Наборы доменов для прогонов. «Обязательные» и «желательные» — встроенные в GP; свои можно создавать, править и удалять.
      На маршрутизацию роутера они не влияют — для неё «Списки» GP Horizon.
    </p>
    {#if editing}
      <div class="editor">
        <label class="field">Название<input type="text" bind:value={editing.name} placeholder="YouTube и Google" /></label>
        <label class="field">Домены — по одному в строке<textarea rows="6" bind:value={editing.text}></textarea></label>
        <div class="row">
          <button onclick={saveList} disabled={busy === 'list' || !editing.name.trim() || !splitDomains(editing.text).length}>
            {busy === 'list' ? 'Сохраняю…' : 'Сохранить'}
          </button>
          <button class="ghost" onclick={() => (editing = null)}>Отмена</button>
          <span class="hint">{splitDomains(editing.text).length} доменов</span>
        </div>
      </div>
    {/if}
    {#if lists === null}
      <p class="muted">Загрузка…</p>
    {:else}
      <div class="tbl-wrap">
        <table>
          <thead><tr><th>Список</th><th>Вид</th><th class="r">Доменов</th><th>Изменён</th><th></th></tr></thead>
          <tbody>
            {#each lists as l (l.list_id)}
              <tr>
                <td>{l.name}</td>
                <td><span class="chip {LIST_KIND[l.kind]?.chip}">{LIST_KIND[l.kind]?.label ?? l.kind}</span></td>
                <td class="r">{l.domains.length}</td>
                <td class="hint">{when(l.updated_at)}</td>
                <td class="act">
                  <button class="ghost sm" onclick={() => (peek[l.list_id] = !peek[l.list_id])}>{peek[l.list_id] ? 'Скрыть' : 'Домены'}</button>
                  {#if l.kind === 'user'}
                    <button class="ghost sm" onclick={() => edit(l)} disabled={!!editing}>Изменить</button>
                    <button class="ghost sm" onclick={() => removeList(l)} disabled={!!busy}>Удалить</button>
                  {/if}
                </td>
              </tr>
              {#if peek[l.list_id]}
                <tr><td colspan="5" class="mono small">{l.domains.join(', ')}</td></tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <div class="stack">
    <section class="card">
      <div class="card-head">
        <h2>Категории v2fly</h2>
        {#if storage}<span class="chip {STORAGE[storage.state]?.chip}">{STORAGE[storage.state]?.label ?? storage.state}</span>{/if}
        <span class="spacer"></span>
        <button class="ghost sm" onclick={() => act('v2fly', gp.v2flyCheck, 'Проверка обновлений категорий запущена.')} disabled={!!busy}>Проверить обновления</button>
        <button class="sm" onclick={() => act('v2fly', gp.v2flyUpdate, 'Категории скачиваются — обновится через минуту.')} disabled={!!busy}>
          {storage?.state === 'ready' ? 'Обновить' : 'Скачать'}
        </button>
      </div>
      <p class="hint">Готовые наборы доменов сервисов (YouTube, Telegram, Discord…) со всеми их CDN — из v2fly/domain-list-community.</p>
      {#if storage}
        <dl class="kv top">
          {#if storage.source_repo}<dt>источник</dt><dd class="mono">{storage.source_repo}</dd>{/if}
          {#if storage.source_ref || storage.source_commit}<dt>версия</dt><dd class="mono">{storage.source_ref ?? ''} {storage.source_commit?.slice(0, 7) ?? ''}</dd>{/if}
          {#if storage.group_count}<dt>категорий</dt><dd>{storage.group_count.toLocaleString('ru-RU')}</dd>{/if}
          <dt>скачаны</dt><dd>{when(storage.prepared_at)}</dd>
        </dl>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Бэкапы GP</h2>
        <span class="spacer"></span>
        <button class="sm" onclick={() => act('backup', gp.createBackup, 'Бэкап создан.')} disabled={!!busy}>Создать</button>
        <button class="ghost sm" onclick={() => fileInput?.click()} disabled={!!busy}>Загрузить файл</button>
        <input bind:this={fileInput} type="file" accept=".zip,application/zip" hidden onchange={upload} />
      </div>
      <p class="hint">Прогоны, стратегии, списки и настройки GP. Перед восстановлением GP сам сохраняет текущие данные.</p>
      {#if backups === null}
        <p class="muted">Загрузка…</p>
      {:else if !backups.length}
        <p class="hint top">Бэкапов пока нет.</p>
      {:else}
        <div class="tbl-wrap top">
          <table>
            <thead><tr><th>Когда</th><th class="r">Размер</th><th class="r">Стратегий</th><th></th></tr></thead>
            <tbody>
              {#each backups as b (b.snapshot_id)}
                <tr>
                  <td class="mono">{when(b.created_at)}</td>
                  <td class="r">{size(b.size_bytes)}</td>
                  <td class="r">{b.entity_counts?.strategies ?? '—'}</td>
                  <td class="act">
                    <a class="ghost-link" href={gp.backupUrl(b.snapshot_id)} download={b.filename ?? 'gp-backup.zip'}>Скачать</a>
                    <button class="ghost sm" onclick={() => restore(b)} disabled={!!busy}>Восстановить</button>
                    <button class="ghost sm" onclick={() => removeBackup(b)} disabled={!!busy}>Удалить</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Ядро GP</h2>
        {#if core}<span class="chip {core.state === 'idle' || core.state === 'running' ? 'ok' : core.state === 'error' ? 'warn' : 'deg'}">{core.state}</span>{/if}
      </div>
      <dl class="kv">
        <dt>версия</dt><dd>{service?.version?.version ?? service?.version?.installed_ref ?? '—'} · обновляется в «Плагинах»</dd>
        {#if core?.storage}<dt>хранилище</dt><dd>{core.storage.ready ? 'готово' : 'не готово'}{core.storage.schema_version ? ` · схема ${core.storage.schema_version}` : ''}</dd>{/if}
        <dt>пароль ядра</dt><dd>ведёт контроллер, наружу не выходит</dd>
      </dl>
      {#if settings}
        <details class="top">
          <summary class="hint">Общие настройки прогонов</summary>
          <dl class="kv top">
            <dt>параллельных curl по умолчанию</dt><dd><input type="number" min="1" bind:value={settings.curl_parallelism_default} /></dd>
            <dt>параллельных curl максимум</dt><dd><input type="number" min="1" bind:value={settings.curl_parallelism_max} /></dd>
            <dt>таймаут curl, с</dt><dd><input type="number" min="1" bind:value={settings.curl_max_time} /></dd>
            <dt>таймаут QUIC, с</dt><dd><input type="number" min="1" bind:value={settings.curl_max_time_quic} /></dd>
            <dt>таймаут DoH, с</dt><dd><input type="number" min="1" bind:value={settings.curl_max_time_doh} /></dd>
            <dt>проверять и IPv6</dt><dd><input type="checkbox" bind:checked={settings.enable_ipv6} /></dd>
            <dt>подробный журнал</dt><dd><input type="checkbox" bind:checked={settings.debug_stdout} /></dd>
          </dl>
          <div class="row top"><button class="sm" onclick={saveSettings} disabled={!!busy}>{busy === 'settings' ? 'Сохраняю…' : 'Сохранить'}</button></div>
        </details>
      {/if}
    </section>
  </div>
</div>

<style>
  h2 {
    font-size: 15px;
  }
  .top {
    margin-top: 10px;
  }
  .editor {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    margin: 10px 0;
    border: 1px solid var(--line);
    border-radius: 10px;
  }
  .act {
    text-align: right;
    white-space: nowrap;
  }
  .small {
    font-size: 11.5px;
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  td.r,
  th.r {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .ghost-link {
    font-size: 12px;
    padding: 3px 9px;
    border: 1px solid var(--line);
    border-radius: 8px;
    color: var(--ink);
    text-decoration: none;
    background: var(--surface);
  }
  dd input[type='number'] {
    width: 80px;
    text-align: right;
  }
</style>
