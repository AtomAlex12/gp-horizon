<script lang="ts">
  // «Дашборд» — the console's first screen, fed by the real agent: engine
  // state from /status, rates from /metrics (or the controller's history).
  import Chart from '../Chart.svelte';
  import Sparkline from '../Sparkline.svelte';
  import type { EngineKind, EngineState } from '../api';
  import { status, node, history } from '../status.svelte';
  import {
    ENGINE_LABEL,
    HEALTH_LABEL,
    MODES,
    fmtBps,
    fmtBpsShort,
    fmtDur,
    fmtNum,
    healthChip,
    healthDot,
    last,
    planeOf,
    plural,
    probeText,
  } from '../ui';
  import { findGlued } from '../domains';

  let { go }: { go: (tab: string) => void } = $props();

  const engines = $derived(status.data?.engines ?? []);
  const byKind = $derived(new Map(engines.map((e) => [e.kind, e])));
  const okCount = $derived(engines.filter((e) => e.health === 'ok').length);
  const drift = $derived(engines.filter((e) => e.want_run !== undefined && e.want_run !== e.running).length);
  const plane = $derived(planeOf(status.data));
  const h = $derived(history.h);
  const ts = $derived(h?.ts ?? []);

  const perMode = $derived(
    MODES.map((m) => ({
      ...m,
      n: (plane?.lists ?? []).filter((l) => l.mode === m.mode).reduce((a, l) => a + l.domains.length, 0),
    })),
  );
  const listed = $derived(perMode.reduce((a, m) => a + m.n, 0));

  // tunnel interface → the engine that owns it (usque: opkgtunN, xray: its tun)
  function tunnelOf(k: EngineKind): string | undefined {
    const ifc = byKind.get(k)?.iface;
    return ifc && h?.tunnels[ifc] ? ifc : undefined;
  }
  function tunnelSum(ifc: string | undefined): number[] {
    const t = ifc ? h?.tunnels[ifc] : undefined;
    return t ? t.rx_bps.map((v, i) => v + (t.tx_bps[i] ?? 0)) : [];
  }
  const COLORS: Record<string, string> = { nfqws2: 'var(--s1)', usque: 'var(--s2)', xray: 'var(--s3)' };
  const tunnelSeries = $derived(
    Object.keys(h?.tunnels ?? {}).map((ifc) => {
      const owner = (['usque', 'xray'] as EngineKind[]).find((k) => byKind.get(k)?.iface === ifc);
      return {
        label: owner ? `${owner === 'usque' ? 'WARP' : 'VLESS'} · ${ifc}` : ifc,
        color: owner ? COLORS[owner] : 'var(--s1)',
        data: tunnelSum(ifc),
        fill: true,
      };
    }),
  );

  function spark(k: EngineKind): number[] {
    if (k === 'nfqws2') return h?.nfq_pps ?? [];
    return tunnelSum(tunnelOf(k));
  }
  function sparkLabel(k: EngineKind): string {
    return k === 'nfqws2' ? 'пакетов/с через NFQUEUE' : 'трафик туннеля';
  }

  // «Требует внимания»: everything that needs the user, from real state only
  type Issue = { sev: 'warn' | 'deg'; text: string; tab?: string };
  const issues = $derived.by(() => {
    const out: Issue[] = [];
    if (node.via === 'controller' && node.agent && !node.agent.reachable)
      out.push({ sev: 'warn', text: `Роутер не отвечает: ${node.agent.last_error ?? ''}`, tab: 'system' });
    if (node.info?.role === 'stand')
      out.push({ sev: 'deg', text: 'Это тестовый стенд на Pi, а не роутер: маршрутизация здесь не работает.', tab: 'system' });
    for (const e of engines) {
      const name = ENGINE_LABEL[e.kind] ?? e.kind;
      if (e.last_error) out.push({ sev: 'warn', text: `${name}: ${e.last_error}`, tab: e.kind });
      else if (e.want_run && !e.running) out.push({ sev: 'warn', text: `${name} должен работать, но остановлен — перезапуск с паузой`, tab: e.kind });
      else if (e.health === 'down' && e.want_run !== false) out.push({ sev: 'deg', text: `${name} остановлен`, tab: e.kind });
      if (e.running && e.probe && !e.probe.ok) out.push({ sev: 'deg', text: `${name}: проба не прошла (${e.probe.reason ?? 'нет ответа'})`, tab: e.kind });
    }
    if (plane) {
      if (plane.last_error) out.push({ sev: 'warn', text: `Маршрутизация: ${plane.last_error}`, tab: 'lists' });
      for (const w of plane.warnings ?? []) out.push({ sev: 'deg', text: w, tab: 'lists' });
      const c = (plane.conflicts ?? []).length;
      if (c) out.push({ sev: 'deg', text: `Доменов ещё в ваших старых списках Keenetic: ${c} — nuxk ждёт, пока вы их оттуда уберёте`, tab: 'lists' });
      const known = [...(plane.lists ?? []).flatMap((l) => l.domains), ...(plane.foreign ?? []).flatMap((f) => f.domains)];
      for (const l of plane.lists ?? []) {
        const n = findGlued(l.domains, known).length;
        if (n)
          out.push({
            sev: 'deg',
            text: `В списке «${l.name}» ${plural(n, 'запись похожа', 'записи похожи', 'записей похожи')} на два склеенных домена — их стоит разделить`,
            tab: 'lists',
          });
      }
      const p = (plane.pending ?? []).length;
      if (p && !plane.apply) out.push({ sev: 'deg', text: `Режим плана: изменений на роутере — ${p}, применятся при PLANE_APPLY=1`, tab: 'lists' });
    }
    return out;
  });
</script>

<div class="grid g4">
  <div class="card kpi">
    <span class="label">Движки</span>
    <span class="value">{okCount}/{engines.length}</span>
    <span class="sub">
      {#if drift}<span class="chip deg">{drift} расходится с намерением</span>{:else}{engines.length ? 'совпадают с намерением' : 'не подключены'}{/if}
    </span>
  </div>
  <div class="card kpi">
    <span class="label">Соединения</span>
    <span class="value">{h?.conntrack.length ? last(h.conntrack) : '—'}</span>
    <span class="sub">в таблице conntrack</span>
  </div>
  <div class="card kpi">
    <span class="label">WAN приём</span>
    <span class="value">{h?.wan_rx_bps.length ? fmtBps(last(h.wan_rx_bps)) : '—'}</span>
    <span class="sub">{h?.wan ? `отдача ${fmtBps(last(h.wan_tx_bps))} · ${h.wan}` : 'интерфейс по умолчанию'}</span>
  </div>
  <button class="card kpi link" onclick={() => go('lists')}>
    <span class="label">Списки</span>
    <span class="value">{plane ? listed : '—'}</span>
    <span class="sub">
      {#if plane}
        {#each perMode as m (m.mode)}<span class="mode {m.mode}">{m.title} {m.n}</span>&nbsp; {/each}
      {:else}маршрутизация выключена{/if}
    </span>
  </button>
</div>

<div class="grid g2">
  <section class="card">
    <div class="card-head"><h2>Трафик WAN</h2><span class="spacer"></span><span class="hint mono">{h?.wan ?? ''}</span></div>
    <Chart
      label="Трафик WAN, бит/с"
      {ts}
      fmt={fmtBpsShort}
      series={[
        { label: 'приём', color: 'var(--s1)', data: h?.wan_rx_bps ?? [], fill: true },
        { label: 'отдача', color: 'var(--s2)', data: h?.wan_tx_bps ?? [] },
      ]}
    />
  </section>
  <section class="card">
    <div class="card-head"><h2>Пакеты NFQUEUE</h2><span class="spacer"></span><span class="hint">nfqws2 · пак/с</span></div>
    <Chart label="Пакеты через NFQUEUE в секунду" {ts} fmt={fmtNum} bars min={5} series={[{ label: 'пакетов/с', color: 'var(--s1)', data: h?.nfq_pps ?? [] }]} />
  </section>
</div>

<div class="grid g3">
  {#each ['nfqws2', 'usque', 'xray'] as EngineKind[] as k (k)}
    {@const e = byKind.get(k) as EngineState | undefined}
    <section
      class="card rowlink eng"
      role="button"
      tabindex="0"
      onclick={() => go(k)}
      onkeydown={(ev) => ev.key === 'Enter' && go(k)}
    >
      <div class="row">
        <span class="dot {healthDot(e)}" class:pulse={e?.running}></span>
        <b>{ENGINE_LABEL[k]}</b>
        <span class="spacer"></span>
        {#if e}<span class="chip {healthChip(e)}">{HEALTH_LABEL[e.health]}</span>{:else}<span class="chip">не подключён</span>{/if}
      </div>
      <div class="row between">
        <div class="muted small">
          {#if !e}движок не установлен или выключен в nuxk.conf
          {:else}
            {e.running ? `аптайм ${e.uptime_sec ? fmtDur(e.uptime_sec) : '—'}` : e.want_run ? 'перезапуск с паузой' : 'остановлен'}<br />
            <span class:bad={e.probe && !e.probe.ok}>{probeText(e)}</span>
          {/if}
        </div>
        <Sparkline data={spark(k)} color={COLORS[k]} label={sparkLabel(k)} />
      </div>
      {#if e?.last_error}<div class="err-text clip">{e.last_error}</div>{/if}
    </section>
  {/each}
</div>

<div class="grid g2">
  <section class="card">
    <div class="card-head"><h2>Трафик по туннелям</h2></div>
    {#if tunnelSeries.length}
      <Chart label="Трафик туннелей, бит/с" {ts} fmt={fmtBpsShort} series={tunnelSeries} />
    {:else}
      <div class="empty">Туннелей пока нет: появятся, когда запустится WARP или VLESS.</div>
    {/if}
  </section>
  <section class="card">
    <div class="card-head">
      <h2>Требует внимания</h2>
      <span class="spacer"></span>
      <span class="chip {issues.some((i) => i.sev === 'warn') ? 'warn' : issues.length ? 'deg' : 'ok'}">{issues.length}</span>
    </div>
    {#if issues.length === 0}
      <div class="empty">Всё в порядке: движки работают, маршрутизация совпадает со списками.</div>
    {:else}
      <div class="stack">
        {#each issues as i, n (n)}
          <button class="issue" onclick={() => i.tab && go(i.tab)}>
            <span class="dot {i.sev}"></span><span>{i.text}</span>
          </button>
        {/each}
      </div>
    {/if}
  </section>
</div>

<style>
  .bad {
    color: var(--warn);
  }
  .link {
    text-align: left;
    color: inherit;
    font-weight: 400;
    white-space: normal;
    align-items: stretch;
    border-color: var(--line);
    background: var(--surface);
  }
  .link:hover {
    border-color: var(--accent);
    filter: none;
  }
  .eng {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .between {
    justify-content: space-between;
    align-items: flex-end;
    flex-wrap: nowrap;
  }
  .small {
    font-size: 12px;
  }
  .clip {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .issue {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    text-align: left;
    white-space: normal;
    background: none;
    border: 0;
    border-radius: 8px;
    padding: 6px 8px;
    color: var(--ink);
    font-weight: 400;
    font-size: 13px;
  }
  .issue:hover {
    background: var(--surface-2);
    filter: none;
  }
  .issue .dot {
    margin-top: 6px;
  }
</style>
