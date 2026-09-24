<script lang="ts">
  import { api, type EngineState, type EngineKind } from './api';
  import { refresh } from './status.svelte';

  let { engine, onOpen }: { engine: EngineState; onOpen?: () => void } = $props();

  let busy = $state(false);

  const dot = $derived(
    engine.health === 'ok'
      ? 'ok'
      : engine.health === 'degraded'
        ? 'degraded'
        : engine.health === 'down'
          ? 'warn'
          : 'idle',
  );

  function fmtDur(s: number): string {
    s = Math.max(0, Math.floor(s || 0));
    const h = Math.floor(s / 3600),
      m = Math.floor((s % 3600) / 60);
    if (h) return `${h}ч ${m}м`;
    if (m) return `${m}м ${s % 60}с`;
    return `${s}с`;
  }

  async function act(e: MouseEvent, action: 'start' | 'stop' | 'restart') {
    e.stopPropagation();
    busy = true;
    try {
      await api.engineAction(engine.kind as EngineKind, action);
    } catch {
      // the controller records it as last_error — shown after the refresh
    } finally {
      await refresh();
      busy = false;
    }
  }
</script>

<div
  class="card"
  role="button"
  tabindex="0"
  onclick={onOpen}
  onkeydown={(e) => e.key === 'Enter' && onOpen?.()}
>
  <div class="head">
    <span class="d {dot}"></span>
    <strong>{engine.kind}</strong>
    <span class="muted">{engine.running ? 'работает' : 'остановлен'}</span>
    <span class="spacer"></span>
    <span class="muted mono">{engine.version ?? ''}</span>
  </div>

  <dl>
    <dt>здоровье</dt><dd>{engine.health}</dd>
    <dt>аптайм</dt><dd>{engine.running ? fmtDur(engine.uptime_sec) : '—'}</dd>
    {#if engine.iface}
      <dt>интерфейс</dt><dd class="mono">{engine.iface}</dd>
    {/if}
    {#if engine.endpoint}
      <dt>endpoint</dt><dd class="mono">{engine.endpoint}</dd>
    {/if}
    <dt>маршруты</dt><dd>{engine.routes}</dd>
    {#if engine.probe}
      <dt>проба</dt>
      <dd class="mono">
        {engine.probe.ok ? `ok · ${engine.probe.egress_ip ?? ''}` : (engine.probe.reason ?? 'fail')}
        {engine.probe.rtt_ms ? ` · ${engine.probe.rtt_ms}ms` : ''}
      </dd>
    {/if}
  </dl>
  {#if engine.last_error || engine.detail?.error}
    <p class="err mono">{engine.last_error || engine.detail?.error}</p>
  {/if}

  <div class="actions">
    <button onclick={(e) => act(e, 'start')} disabled={busy || engine.running}>Запуск</button>
    <button class="warn" onclick={(e) => act(e, 'stop')} disabled={busy || !engine.running}>Стоп</button>
    <button class="ghost" onclick={(e) => act(e, 'restart')} disabled={busy || !engine.running}>Рестарт</button>
    {#if onOpen}
      <button class="ghost" onclick={(e) => { e.stopPropagation(); onOpen?.(); }}>Подробнее →</button>
    {/if}
  </div>
</div>

<style>
  .err {
    color: var(--warn);
    font-size: 11px;
    margin: 0 0 10px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .card {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 15px 16px;
    cursor: pointer;
  }
  .card:hover {
    border-color: var(--accent);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
  }
  .head strong {
    text-transform: capitalize;
  }
  .spacer {
    flex: 1;
  }
  .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .d {
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--idle);
    flex: none;
  }
  .d.ok {
    background: var(--ok);
  }
  .d.warn {
    background: var(--warn);
  }
  .d.degraded {
    background: var(--degraded);
  }
  .d.idle {
    background: var(--idle);
  }
  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 5px 14px;
    margin: 0 0 14px;
    font-size: 13px;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    text-align: right;
    word-break: break-all;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
</style>
