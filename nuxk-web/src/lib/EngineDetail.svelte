<script lang="ts">
  import type { Snippet } from 'svelte';
  import { api, type EngineKind, type EngineState } from './api';
  import { refresh } from './status.svelte';

  let {
    kind,
    label,
    engine,
    extra,
  }: {
    kind: EngineKind;
    label: string;
    engine: EngineState | undefined;
    extra?: Snippet;
  } = $props();

  let busy = $state(false);
  let probing = $state(false);
  let showRaw = $state(false);

  const dot = $derived(
    !engine
      ? 'idle'
      : engine.health === 'ok'
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

  function fmtBytes(v?: string): string {
    const n = Number(v ?? 0);
    if (!n) return '0 Б';
    const units = ['Б', 'КБ', 'МБ', 'ГБ'];
    let i = 0,
      x = n;
    while (x >= 1024 && i < units.length - 1) {
      x /= 1024;
      i++;
    }
    return `${x.toFixed(x < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
  }

  let probeErr = $state<string | null>(null);

  async function act(action: 'start' | 'stop' | 'restart') {
    busy = true;
    try {
      await api.engineAction(kind, action);
    } catch {
      // the controller records it as last_error — shown after the refresh
    } finally {
      await refresh();
      busy = false;
    }
  }

  async function probe() {
    probing = true;
    probeErr = null;
    try {
      await api.engineProbe(kind);
      await refresh();
    } catch (e) {
      probeErr = e instanceof Error ? e.message : String(e);
    } finally {
      probing = false;
    }
  }

  const autoLabel = $derived(
    engine?.want_run === true ? 'держать запущенным' : engine?.want_run === false ? 'держать остановленным' : 'не управляется',
  );

  const detailEntries = $derived(Object.entries(engine?.detail ?? {}).filter(([k]) => k !== 'items' && k !== 'error'));
</script>

<div class="panel">
  {#if !engine}
    <p class="muted">
      Движок не подключён в контроллере: скрипт не задан в <span class="mono">nuxk.conf</span> или не установлен.
    </p>
  {:else}
  <div class="head">
    <span class="d {dot}"></span>
    <strong>{label}</strong>
    <span class="muted">{engine?.running ? 'работает' : 'остановлен'}</span>
    <span class="spacer"></span>
    <span class="muted mono">{engine?.version ?? ''}</span>
  </div>

  <dl class="stats">
    <dt>здоровье</dt><dd>{engine?.health ?? 'unknown'}</dd>
    <dt>аптайм</dt><dd>{engine?.running ? fmtDur(engine.uptime_sec) : '—'}</dd>
    {#if engine?.iface}
      <dt>интерфейс</dt><dd class="mono">{engine.iface}</dd>
    {/if}
    {#if engine?.endpoint}
      <dt>endpoint</dt><dd class="mono">{engine.endpoint}</dd>
    {/if}
    <dt>маршруты</dt><dd>{engine?.routes ?? 0}</dd>
    <dt>автозапуск</dt><dd>{autoLabel}</dd>
    {#if engine?.detail?.rx_bytes}
      <dt>rx / tx</dt><dd class="mono">{fmtBytes(engine.detail.rx_bytes)} / {fmtBytes(engine.detail.tx_bytes)}</dd>
    {/if}
  </dl>

  <div class="actions">
    <button onclick={() => act('start')} disabled={busy || engine?.running}>Запуск</button>
    <button class="warn" onclick={() => act('stop')} disabled={busy || !engine?.running}>Стоп</button>
    <button class="ghost" onclick={() => act('restart')} disabled={busy || !engine?.running}>Рестарт</button>
    <button class="ghost" onclick={probe} disabled={probing}>{probing ? 'Проверка…' : 'Проба'}</button>
  </div>

  {#if engine?.last_error}
    <p class="err mono">{engine.last_error}</p>
  {/if}
  {#if engine?.detail?.error}
    <p class="err mono">{engine.detail.error}</p>
  {/if}
  {#if probeErr}
    <p class="err mono">проба: {probeErr}</p>
  {/if}

  {#if engine?.probe}
    <div class="probe">
      <strong class="muted">Последняя проба</strong>
      <div class="mono probe-line">
        {engine.probe.ok ? '✓ ok' : '✗ ' + (engine.probe.reason ?? 'fail')}
        {#if engine.probe.egress_ip}
          · {engine.probe.egress_ip}
        {/if}
        {#if engine.probe.rtt_ms}
          · {engine.probe.rtt_ms}ms
        {/if}
        {#if engine.probe.detail?.target}
          · target={engine.probe.detail.target}
        {/if}
      </div>
    </div>
  {/if}

  {#if extra}
    <div class="extra">{@render extra()}</div>
  {/if}

  {#if detailEntries.length > 0}
    <button class="ghost raw-toggle" onclick={() => (showRaw = !showRaw)}>
      {showRaw ? 'Скрыть' : 'Показать'} технические детали
    </button>
    {#if showRaw}
      <dl class="stats raw">
        {#each detailEntries as [k, v] (k)}
          <dt>{k}</dt><dd class="mono">{v}</dd>
        {/each}
      </dl>
    {/if}
  {/if}
  {/if}
</div>

<style>
  .panel {
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 12px;
    padding: 18px 20px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 14px;
  }
  .head strong {
    font-size: 15px;
  }
  .spacer {
    flex: 1;
  }
  .muted {
    color: var(--muted);
    font-size: 12px;
  }
  .err {
    color: var(--warn);
    font-size: 12px;
    margin: 0 0 12px;
    word-break: break-word;
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
  .stats {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 5px 14px;
    margin: 0 0 14px;
    font-size: 13px;
  }
  .stats dt {
    color: var(--muted);
  }
  .stats dd {
    margin: 0;
    text-align: right;
    word-break: break-all;
  }
  .stats.raw {
    margin-top: 10px;
    font-size: 12px;
  }
  .actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    margin-bottom: 4px;
  }
  .probe {
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--line);
  }
  .probe-line {
    margin-top: 4px;
    font-size: 13px;
  }
  .extra {
    margin-top: 16px;
    padding-top: 16px;
    border-top: 1px solid var(--line);
  }
  .raw-toggle {
    margin-top: 14px;
    font-size: 12px;
    padding: 4px 10px;
  }
</style>
