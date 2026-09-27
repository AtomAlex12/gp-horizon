<script lang="ts">
  // Full version: VLESS links and 3x-ui subscriptions kept on the Pi, as many
  // as you like. "На роутер" sends the router one server as a single link —
  // the router never fetches a subscription itself. Links and subscription
  // URLs go to the Pi and never come back to the browser.
  import { vless, type VlessBook, type VlessSource } from './vless';
  import { xray, loadUpstream, every } from './xray.svelte';
  import { refresh } from './status.svelte';
  import UsageBar from './UsageBar.svelte';
  import { ago } from './ui';

  let book = $state<VlessBook | null>(null);
  let err = $state('');
  let busy = $state(''); // what's being done: "add", a source id, a server key

  async function run(what: string, fn: () => Promise<VlessBook>): Promise<boolean> {
    busy = what;
    err = '';
    try {
      book = await fn();
      return true;
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
      return false;
    } finally {
      busy = '';
    }
  }
  $effect(() => {
    void run('', vless.list);
  });

  // the server the router actually runs, as the router itself reports it
  const onRouter = $derived(xray.up && xray.up.servers.length ? xray.up.servers[xray.up.active]?.key : '');

  let adding = $state(false);
  let kind = $state<VlessSource['kind']>('subscription');
  let url = $state('');
  let name = $state('');
  const valid = $derived(kind === 'link' ? url.trim().startsWith('vless://') : /^https?:\/\//.test(url.trim()));
  async function add() {
    if (await run('add', () => vless.add(kind, url.trim(), name.trim()))) {
      url = '';
      name = '';
      adding = false;
    }
  }
  async function use(src: VlessSource, key: string) {
    if (await run(key, () => vless.use(src.id, key))) {
      await loadUpstream();
      await refresh();
    }
  }
  function remove(src: VlessSource) {
    if (confirm(`Удалить «${label(src)}» с Pi? Сервер, который сейчас на роутере, продолжит работать.`)) void run(src.id, () => vless.remove(src.id));
  }
  const label = (s: VlessSource) => s.name || s.title || (s.kind === 'link' ? s.servers[0]?.name || 'Ссылка' : 'Подписка');
</script>

<section class="card">
  <div class="card-head">
    <h2>Серверы VLESS на Pi</h2>
    {#if book}<span class="chip">{book.sources.length}</span>{/if}
    <span class="spacer"></span>
    {#if !adding}<button class="ghost sm" onclick={() => (adding = true)}>Добавить</button>{/if}
  </div>

  {#if adding}
    <div class="add">
      <div class="src" role="radiogroup" aria-label="Что добавить">
        <label><input type="radio" bind:group={kind} value="subscription" /> Подписка 3x-ui</label>
        <label><input type="radio" bind:group={kind} value="link" /> Ссылка vless://</label>
      </div>
      <input bind:value={name} placeholder="Название (необязательно), например «Дом» или «Нидерланды»" maxlength="60" />
      <input
        class="mono"
        type="password"
        autocomplete="off"
        spellcheck="false"
        bind:value={url}
        placeholder={kind === 'link' ? 'vless://uuid@host:443?security=reality&sni=…' : 'https://panel.example/sub/…'}
      />
      <p class="hint">Хранится на Pi (controller.json, 0600) и в браузер обратно не отдаётся. Подписка скачается сразу — так видно, что она рабочая.</p>
      <div class="row">
        <button onclick={add} disabled={busy === 'add' || !valid}>{busy === 'add' ? 'Проверяю…' : 'Добавить'}</button>
        <button class="ghost" onclick={() => (adding = false)} disabled={busy === 'add'}>Отмена</button>
      </div>
    </div>
  {/if}
  {#if err}<p class="err-text">{err}</p>{/if}

  {#if book === null}
    <p class="muted">Загрузка…</p>
  {:else if !book.sources.length}
    <div class="empty">Пока пусто — добавьте подписку 3x-ui или ссылку vless://. Их может быть сколько угодно: на роутер уходит только выбранный сервер.</div>
  {:else}
    {#each book.sources as src (src.id)}
      <div class="source">
        <div class="row head">
          <b>{label(src)}</b>
          <span class="chip">{src.kind === 'subscription' ? 'подписка' : 'ссылка'}</span>
          {#if src.kind === 'subscription'}<span class="hint">обновлена {ago(src.fetched_at)} · {every(src.refresh_s)}</span>{/if}
          <span class="spacer"></span>
          {#if src.kind === 'subscription'}
            <button class="ghost sm" onclick={() => run(src.id, () => vless.refresh(src.id))} disabled={!!busy}>{busy === src.id ? 'Обновляю…' : 'Обновить'}</button>
          {/if}
          <button class="ghost sm" onclick={() => remove(src)} disabled={!!busy}>Удалить</button>
        </div>
        {#if src.usage}<UsageBar usage={src.usage} />{/if}
        {#if src.error}<p class="err-text">{src.error}</p>{/if}
        <div class="tbl-wrap">
          <table>
            <tbody>
              {#each src.servers as s (s.key)}
                <tr class:on={s.key === onRouter}>
                  <td><b>{s.name || '—'}</b></td>
                  <td class="mono">{s.address}</td>
                  <td class="mono muted">{s.security} / {s.network}{s.flow ? ` · ${s.flow}` : ''}</td>
                  <td class="act">
                    {#if s.key === onRouter}<span class="chip ok">на роутере</span>
                    {:else}<button class="ghost sm" onclick={() => use(src, s.key)} disabled={!!busy}>{busy === s.key ? 'Отправляю…' : 'На роутер'}</button>{/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if src.skipped}<p class="hint">Ещё {src.skipped} ссылок в подписке не удалось разобрать — они пропущены.</p>{/if}
      </div>
    {/each}
    <p class="hint">
      Подписки контроллер перечитывает сам, по расписанию панели. Если у сервера, который сейчас на роутере, поменялись
      настройки (ключи, порт), роутер получит новые автоматически.
    </p>
  {/if}
</section>

<style>
  .add,
  .source {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px;
    border: 1px solid var(--line);
    border-radius: 10px;
    margin-bottom: 10px;
  }
  .head {
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }
  .src {
    display: flex;
    gap: 16px;
    font-size: 13px;
    flex-wrap: wrap;
  }
  .src label {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  tr.on td {
    background: color-mix(in srgb, var(--accent) 8%, transparent);
  }
  .act {
    text-align: right;
    white-space: nowrap;
  }
  .muted {
    color: var(--muted);
  }
</style>
