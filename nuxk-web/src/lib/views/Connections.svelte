<script lang="ts">
  // What the agent can say about connections today: the conntrack table size
  // over time. Per-connection rows (who, where, which way) come with the
  // conntrack reader in a later version.
  import Chart from '../Chart.svelte';
  import { history, chartSpan } from '../status.svelte';
  import { fmtNum, last } from '../ui';
  const h = $derived(history.h);
</script>

<div class="grid g2">
  <section class="card kpi">
    <span class="label">Соединений сейчас</span>
    <span class="value">{h?.conntrack.length ? last(h.conntrack) : '—'}</span>
    <span class="sub">записей в таблице conntrack роутера</span>
  </section>
  <section class="card kpi">
    <span class="label">Нагрузка</span>
    <span class="value">{h?.load1.length ? last(h.load1).toFixed(2) : '—'}</span>
    <span class="sub">load average за минуту</span>
  </section>
</div>
<section class="card">
  <div class="card-head"><h2>Таблица conntrack</h2></div>
  <Chart label="Число соединений" ts={h?.ts ?? []} span={chartSpan()} fmt={fmtNum} min={10} series={[{ label: 'соединений', color: 'var(--s1)', data: h?.conntrack ?? [], fill: true }]} />
</section>
<section class="card empty">
  <h2>Список соединений</h2>
  <p>Кто, куда и каким путём (DPI / WARP / VLESS / напрямую) — появится в следующей версии: nuxk-core научится читать conntrack построчно.</p>
</section>

<style>
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
