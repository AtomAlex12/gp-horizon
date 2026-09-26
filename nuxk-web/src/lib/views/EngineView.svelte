<script lang="ts">
  // One engine's page, laid out like the console: state card + actions on
  // the left, what the engine carries on the right, raw details below.
  import { api, type EngineKind, type ListMode } from '../api';
  import { status, refresh } from '../status.svelte';
  import NfqwsRouting from '../NfqwsRouting.svelte';
  import XrayConfig from '../XrayConfig.svelte';
  import { ENGINE_LABEL, HEALTH_LABEL, fmtDur, healthChip, healthDot, planeOf } from '../ui';

  let { kind, go }: { kind: EngineKind; go: (tab: string) => void } = $props();

  const e = $derived(status.data?.engines.find((x) => x.kind === kind));
  const all = $derived(status.data?.engines ?? []);
  const plane = $derived(planeOf(status.data));
  const MODE: Record<EngineKind, ListMode> = { nfqws2: 'desync', usque: 'warp', xray: 'vless' };
  const domains = $derived(
    kind === 'nfqws2'
      ? (plane?.desync ?? [])
      : (plane?.lists ?? []).filter((l) => l.mode === MODE[kind]).flatMap((l) => l.domains),
  );
  const autoEps = $derived(all.filter((x) => x.kind !== 'nfqws2' && x.endpoint).map((x) => `${x.endpoint} · ${x.kind}`));

  let busy = $state(false);
  let msg = $state<{ ok: boolean; text: string } | null>(null);

  async function act(a: 'start' | 'stop' | 'restart') {
    busy = true;
    msg = null;
    try {
      await api.engineAction(kind, a);
    } catch (err) {
      msg = { ok: false, text: err instanceof Error ? err.message : String(err) };
    } finally {
      await refresh();
      busy = false;
    }
  }
  async function probe() {
    busy = true;
    msg = null;
    try {
      const p = await api.engineProbe(kind);
      msg = p.ok
        ? { ok: true, text: `проба ok${p.rtt_ms ? ` · ${Math.round(p.rtt_ms)} мс` : ''}${p.egress_ip ? ` · выход ${p.egress_ip}` : ''}` }
        : { ok: false, text: `проба не прошла: ${p.reason ?? 'нет ответа'}` };
    } catch (err) {
      msg = { ok: false, text: err instanceof Error ? err.message : String(err) };
    } finally {
      await refresh();
      busy = false;
    }
  }
  const intent = $derived(
    e?.want_run === true ? 'держать запущенным' : e?.want_run === false ? 'держать остановленным' : 'не управляется',
  );
  function bytes(v?: string) {
    const n = Number(v ?? 0);
    if (!n) return '0 Б';
    const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
    let i = 0,
      x = n;
    while (x >= 1024 && i < u.length - 1) (x /= 1024), i++;
    return `${x.toFixed(x < 10 && i ? 1 : 0)} ${u[i]}`;
  }
</script>

{#if !e}
  <section class="card empty">
    <h2>{ENGINE_LABEL[kind]} не подключён</h2>
    <p>
      Движок не установлен на этом узле или выключен в <span class="mono">nuxk.conf</span>
      ({kind === 'nfqws2' ? 'ENGINE_NFQWS2' : kind === 'usque' ? 'ENGINE_USQUE' : 'ENGINE_XRAY'}).
      {#if kind === 'usque'}WARP ставится инсталлятором — шаг «usque (WARP)».{/if}
      {#if kind === 'xray'}VLESS на роутере появится в следующих версиях.{/if}
    </p>
  </section>
{:else}
  <div class="grid g2">
    <section class="card engine">
      <div class="row head">
        <span class="dot {healthDot(e)}" class:pulse={e.running}></span>
        <h2>{ENGINE_LABEL[kind]}</h2>
        <span class="chip {healthChip(e)}">{HEALTH_LABEL[e.health]}</span>
        <span class="spacer"></span>
        <span class="mono muted small">{e.version ?? ''}</span>
      </div>
      <dl class="kv">
        <dt>аптайм</dt><dd>{e.running && e.uptime_sec ? fmtDur(e.uptime_sec) : '—'}</dd>
        <dt>PID</dt><dd class="mono">{e.running && e.pid ? e.pid : '—'}</dd>
        <dt>{kind === 'nfqws2' ? 'WAN-интерфейс' : 'интерфейс'}</dt><dd class="mono">{e.iface || '—'}</dd>
        {#if kind !== 'nfqws2'}<dt>апстрим</dt><dd class="mono">{e.endpoint || '—'}</dd>{/if}
        <dt>автозапуск</dt><dd>{intent}</dd>
        <dt>проба</dt>
        <dd>
          {#if e.probe}
            {#if e.probe.ok}<span class="chip ok">ok</span>
              <span class="mono">{e.probe.rtt_ms ? `${Math.round(e.probe.rtt_ms)} мс` : ''}{e.probe.egress_ip ? ` · ${e.probe.egress_ip}` : ''}</span>
            {:else}<span class="chip warn">не прошла</span> <span class="mono err-text">{e.probe.reason ?? 'нет ответа'}</span>{/if}
          {:else}—{/if}
        </dd>
      </dl>
      {#if e.last_error}<div class="err-text">{e.last_error}</div>{/if}
      <div class="row">
        <button onclick={() => act('start')} disabled={busy || e.running}>Запуск</button>
        <button class="warn" onclick={() => act('stop')} disabled={busy || (!e.running && e.want_run === false)}>Стоп</button>
        <button class="ghost" onclick={() => act('restart')} disabled={busy || !e.running}>Рестарт</button>
        <button class="ghost" onclick={probe} disabled={busy}>Проба</button>
      </div>
      {#if msg}<p class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</p>{/if}
    </section>

    {#if kind === 'nfqws2'}
      <section class="card">
        <div class="card-head">
          <h2>Список DPI</h2>
          <span class="chip">{e.detail?.desync_count ?? domains.length} в nfqws2</span>
          <span class="spacer"></span>
          {#if plane}<button class="ghost sm" onclick={() => go('lists')}>Списки →</button>{/if}
        </div>
        {#if plane}
          {#if !plane.desync_managed}
            <p class="hint">nuxk пока не трогает список nfqws2 — появится, когда вы создадите список «DPI» в «Списках».</p>
          {:else if domains.length}
            <div class="row chips">{#each domains.slice(0, 60) as d (d)}<span class="chip mono">{d}</span>{/each}
              {#if domains.length > 60}<span class="muted small">и ещё {domains.length - 60}</span>{/if}</div>
          {:else}<div class="empty">пусто</div>{/if}
        {:else}
          <NfqwsRouting engine={e} />
        {/if}
        <div class="divider"></div>
        <div class="card-head sub"><h2>Endpoints туннелей</h2><span class="hint">их рукопожатия тоже идут через десинк</span></div>
        <div class="row chips">
          {#each autoEps as ep (ep)}<span class="chip acc mono">{ep}</span>{:else}<span class="muted small">туннелей нет</span>{/each}
        </div>
      </section>
    {:else if kind === 'usque'}
      <section class="card">
        <div class="card-head"><h2>WARP</h2></div>
        <dl class="kv">
          <dt>туннель</dt><dd>{e.detail?.tunnel_state ?? '—'}</dd>
          <dt>SNI</dt><dd class="mono">{e.detail?.sni || '—'}</dd>
          <dt>HTTP/2</dt><dd>{e.detail?.http2 === '1' ? 'вкл.' : 'выкл.'}</dd>
          <dt>колокация</dt><dd class="mono">{e.probe?.detail?.colo || '—'}</dd>
          <dt>выходной IP</dt><dd class="mono">{e.probe?.egress_ip || '—'}</dd>
          <dt>принято / отдано</dt><dd class="mono">{bytes(e.detail?.rx_bytes)} / {bytes(e.detail?.tx_bytes)}</dd>
        </dl>
      </section>
    {:else}
      <XrayConfig engine={e} />
    {/if}
  </div>

  {#if kind !== 'nfqws2'}
    <section class="card">
      <div class="card-head">
        <h2>Домены через {kind === 'usque' ? 'WARP' : 'VLESS'}</h2>
        <span class="chip">{domains.length}</span>
        <span class="spacer"></span>
        {#if plane}<button class="ghost sm" onclick={() => go('lists')}>Списки →</button>{/if}
      </div>
      {#if !plane}<p class="hint">Маршрутизация выключена на этом узле.</p>
      {:else if domains.length}
        <div class="row chips">{#each domains.slice(0, 80) as d (d)}<span class="chip mono">{d}</span>{/each}</div>
      {:else}<div class="empty">пусто — добавьте домены в «Списки»</div>{/if}
    </section>
  {/if}

  {#if e.detail && Object.keys(e.detail).length}
    <details class="card raw">
      <summary>Сведения движка</summary>
      <dl class="kv">
        {#each Object.entries(e.detail).filter(([k]) => k !== 'items') as [k, v] (k)}<dt class="mono">{k}</dt><dd class="mono">{v || '—'}</dd>{/each}
      </dl>
    </details>
  {/if}
{/if}

<style>
  .engine {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .head h2 {
    font-size: 15px;
    font-weight: 600;
  }
  .small {
    font-size: 12px;
  }
  .chips {
    gap: 6px;
  }
  .sub {
    margin: 10px 0 8px;
  }
  .raw summary {
    cursor: pointer;
    font-weight: 600;
    font-size: 14px;
  }
  .raw dl {
    margin-top: 10px;
  }
  .empty h2 {
    font-size: 15px;
    color: var(--ink);
    margin-bottom: 8px;
  }
  .empty p {
    max-width: 560px;
    margin: 0 auto;
  }
</style>
