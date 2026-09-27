<script lang="ts">
  // «Сервер VLESS»: what the router's xray runs now, the subscription's traffic
  // and term. The lite version (the router alone) sets its one source here —
  // a vless:// link or one 3x-ui subscription; the full version keeps any
  // number of them on the Pi and sends the router one server at a time.
  import type { EngineState } from './api';
  import { api } from './api';
  import { refresh, node } from './status.svelte';
  import { xray, loadUpstream, change, every } from './xray.svelte';
  import UsageBar from './UsageBar.svelte';
  import { ago } from './ui';

  let { engine }: { engine: EngineState | undefined } = $props();

  $effect(() => {
    void loadUpstream();
  });

  const full = !__LITE__ && node.via === 'controller';
  const up = $derived(xray.up);
  const cur = $derived(up && up.servers.length ? up.servers[up.active] : null);

  // exactly one source on the router: a vless:// link or a subscription URL
  let source = $state<'vless_uri' | 'sub_url'>('vless_uri');
  let value = $state('');
  let message = $state<{ ok: boolean; text: string } | null>(null);
  const trimmed = $derived(value.trim());
  const valid = $derived(source === 'vless_uri' ? trimmed.startsWith('vless://') : /^https?:\/\//.test(trimmed));

  async function apply() {
    message = null;
    if (await change(() => api.setConfig('xray', { [source]: trimmed }))) {
      value = ''; // it carries the UUID — not kept on screen once applied
      message = { ok: true, text: 'Применено, xray перезапущен' };
      await refresh();
    }
  }
</script>

<section class="card xcfg">
  <div class="card-head"><h2>Сервер VLESS</h2>{#if up?.source}<span class="chip">{up.source === 'subscription' ? 'подписка' : 'ссылка'}</span>{/if}</div>

  {#if cur}
    <div class="cur">
      <b>{cur.name || 'без названия'}</b>
      <span class="mono">{cur.address}</span>
      <span class="mono muted">{cur.security} / {cur.network}{cur.flow ? ` · ${cur.flow}` : ''}</span>
      {#if engine?.detail?.sni}<span class="mono muted">SNI {engine.detail.sni}</span>{/if}
    </div>
  {:else}
    <p class="hint">Сервер не задан{engine?.endpoint ? ` (на роутере — ${engine.endpoint})` : ''}.</p>
  {/if}

  {#if up?.source === 'subscription'}
    <div class="sub">
      <div class="row between">
        <b>{up.title || 'Подписка'}</b>
        <span class="hint">обновлена {ago(up.fetched_at)} · {every(up.refresh_s)}</span>
      </div>
      {#if up.usage}<UsageBar usage={up.usage} />{/if}
      {#if up.notice}<p class="warn-text">{up.notice}</p>{/if}
      {#if up.error}<p class="err-text">Последнее обновление не удалось: {up.error}. Работает прежний сервер.</p>{/if}
    </div>
  {/if}

  {#if full}
    <p class="hint">
      Ссылки и подписки хранятся на Pi — ниже, в «Серверах VLESS». Роутер получает от контроллера только один выбранный
      сервер и подписки сам не скачивает.
    </p>
  {:else}
    <div class="divider"></div>
    <p class="hint">
      Одна ссылка <span class="mono">vless://</span> или одна подписка 3x-ui («Инбаунды» → клиент → «Поделиться»).
      Подписку роутер перечитывает сам, по расписанию панели; если с новым сервером xray не запустится — вернётся прежний.
    </p>
    <div class="src" role="radiogroup" aria-label="Источник">
      <label><input type="radio" bind:group={source} value="vless_uri" /> Ссылка vless://</label>
      <label><input type="radio" bind:group={source} value="sub_url" /> Подписка 3x-ui</label>
    </div>
    <input
      class="mono"
      type="password"
      autocomplete="off"
      spellcheck="false"
      bind:value
      placeholder={source === 'vless_uri' ? 'vless://uuid@host:443?security=reality&sni=…' : 'https://panel.example/sub/…'}
    />
    <p class="hint">В ссылке ключ — поле скрыто и очищается после применения.</p>
    <div class="row">
      <button onclick={apply} disabled={xray.busy || !valid}>{xray.busy ? 'Применяю…' : up?.source ? 'Заменить' : 'Применить'}</button>
      {#if trimmed && !valid}<span class="err-text">{source === 'vless_uri' ? 'Нужна ссылка vless://' : 'Нужен http(s):// адрес'}</span>
      {:else if message}<span class={message.ok ? 'ok-text' : 'err-text'}>{message.text}</span>{/if}
    </div>
  {/if}
  {#if xray.err}<p class="err-text">{xray.err}</p>{/if}
</section>

<style>
  .xcfg {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .cur {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
    align-items: baseline;
  }
  .sub {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 10px;
  }
  .between {
    justify-content: space-between;
    gap: 8px;
    flex-wrap: wrap;
  }
  .muted {
    color: var(--muted);
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
  .warn-text {
    color: var(--degraded);
    font-size: 12px;
    margin: 0;
  }
</style>
