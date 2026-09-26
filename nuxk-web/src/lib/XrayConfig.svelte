<script lang="ts">
  import type { EngineState } from './api';
  import { api } from './api';
  import { refresh } from './status.svelte';

  let { engine }: { engine: EngineState | undefined } = $props();

  // Exactly one source: a raw vless:// link or a 3x-ui subscription URL.
  let source = $state<'vless_uri' | 'sub_url'>('vless_uri');
  let value = $state('');
  let saving = $state(false);
  let message = $state<{ ok: boolean; text: string } | null>(null);

  const trimmed = $derived(value.trim());
  const valid = $derived(
    source === 'vless_uri' ? trimmed.startsWith('vless://') : /^https?:\/\//.test(trimmed),
  );

  async function apply() {
    saving = true;
    message = null;
    try {
      await api.setConfig('xray', { [source]: trimmed });
      await refresh();
      value = ''; // the link carries the UUID — don't keep it on screen after it's applied
      message = { ok: true, text: 'Конфиг применён, xray перезапущен' };
    } catch (e) {
      message = { ok: false, text: e instanceof Error ? e.message : String(e) };
    } finally {
      saving = false;
    }
  }
</script>

<section class="card xcfg">
  <div class="card-head"><h2>Сервер VLESS</h2></div>
  <p class="hint muted">
    Текущий:
    <span class="mono">{engine?.detail?.server || engine?.endpoint || '— не задан'}</span>
    {#if engine?.detail?.security}· <span class="mono">{engine.detail.security}{engine.detail.network ? ` / ${engine.detail.network}` : ''}</span>{/if}
    {#if engine?.detail?.sni}· SNI <span class="mono">{engine.detail.sni}</span>{/if}
    {#if engine?.detail?.flow}· <span class="mono">{engine.detail.flow}</span>{/if}
  </p>
  <p class="hint muted">
    Ссылку даёт панель 3x-ui: «Инбаунды» → ваш клиент → «Поделиться». Подходят Reality и TLS, транспорт tcp, ws, grpc,
    xhttp. Из подписки берётся первый сервер VLESS. xray проверит конфиг сам; если с новым сервером не запустится —
    вернётся прежний.
  </p>

  <div class="src" role="radiogroup" aria-label="Источник конфига">
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
  <p class="hint muted">Ссылка содержит UUID-ключ — поле скрыто и очищается после применения.</p>

  <div class="row">
    <button onclick={apply} disabled={saving || !valid}>{saving ? 'Применяю…' : 'Применить'}</button>
    {#if trimmed && !valid}
      <span class="err-msg">{source === 'vless_uri' ? 'Нужна ссылка vless://' : 'Нужен http(s):// URL'}</span>
    {:else if message}
      <span class={message.ok ? 'ok-msg' : 'err-msg'}>{message.text}</span>
    {/if}
  </div>
</section>

<style>
  .hint {
    margin: 0 0 8px;
  }
  .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .src {
    display: flex;
    gap: 16px;
    margin-bottom: 8px;
    font-size: 13px;
    flex-wrap: wrap;
  }
  .src label {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 10px;
    flex-wrap: wrap;
  }
  .ok-msg {
    color: var(--ok);
    font-size: 12px;
  }
  .err-msg {
    color: var(--warn);
    font-size: 12px;
  }
</style>
