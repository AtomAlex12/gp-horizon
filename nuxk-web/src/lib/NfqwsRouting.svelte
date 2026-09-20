<script lang="ts">
  import type { EngineState } from './api';
  import { api } from './api';
  import { refresh } from './status.svelte';

  let { engine }: { engine: EngineState | undefined } = $props();

  let domainsText = $state('');
  let endpointsText = $state('');
  let initialized = $state(false);
  let saving = $state(false);
  let message = $state<{ ok: boolean; text: string } | null>(null);

  // Pre-fill once from the engine's own reported list — never overwrite an
  // in-progress edit on later polls.
  $effect(() => {
    if (!initialized && engine?.detail?.items) {
      domainsText = engine.detail.items.split(',').filter(Boolean).join('\n');
      initialized = true;
    }
  });

  async function apply() {
    saving = true;
    message = null;
    try {
      const domains = domainsText
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      const endpoints = endpointsText
        .split('\n')
        .map((s) => s.trim())
        .filter(Boolean);
      await api.applyRouting('nfqws2', { domains, endpoints });
      await refresh();
      message = { ok: true, text: `Применено: ${domains.length} доменов, ${endpoints.length} endpoint'ов` };
    } catch (e) {
      message = { ok: false, text: e instanceof Error ? e.message : String(e) };
    } finally {
      saving = false;
    }
  }
</script>

<div class="routing">
  <strong>Список принудительного десинка</strong>
  <p class="hint muted">По одному домену на строку. Десинкается всегда, независимо от auto-обучения.</p>
  <textarea rows="6" bind:value={domainsText} placeholder={'www.youtube.com\nbrowserleaks.com'}></textarea>

  <strong class="second">Endpoint'ы других движков (hardening)</strong>
  <p class="hint muted">
    IP апстримов usque/xray — чтобы их хендшейки тоже переживали DPI ("WARP через nfqws"). Список не
    читается обратно с сервера — пустое поле оставляет текущий список без изменений.
  </p>
  <textarea rows="3" bind:value={endpointsText} placeholder="162.159.198.2"></textarea>

  <div class="row">
    <button onclick={apply} disabled={saving}>{saving ? 'Применяю…' : 'Применить'}</button>
    {#if message}
      <span class={message.ok ? 'ok-msg' : 'err-msg'}>{message.text}</span>
    {/if}
  </div>
</div>

<style>
  .routing strong {
    display: block;
    font-size: 13px;
    margin-bottom: 4px;
  }
  .routing strong.second {
    margin-top: 14px;
  }
  .hint {
    margin: 0 0 8px;
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
