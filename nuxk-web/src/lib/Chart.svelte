<script lang="ts">
  // Time-series chart in the console's style: recessive grid, 2px lines with a
  // light fill (or thin bars), a crosshair that snaps to the nearest sample
  // and one tooltip listing every series. The legend keys each series; values
  // stay in text ink, never in the series colour.
  type Series = { label: string; color: string; data: number[]; fill?: boolean };
  let {
    series,
    ts,
    fmt,
    label,
    bars = false,
    min = 1,
  }: { series: Series[]; ts: number[]; fmt: (v: number) => string; label: string; bars?: boolean; min?: number } =
    $props();

  const W = 640,
    H = 200,
    pl = 52,
    pr = 10,
    pt = 10,
    pb = 22;
  const n = $derived(Math.max(2, ...series.map((s) => s.data.length)));
  const maxRaw = $derived(Math.max(min, ...series.flatMap((s) => s.data)));
  const step = $derived(niceStep(maxRaw / 4));
  const max = $derived(Math.ceil(maxRaw / step) * step);
  const x = (i: number) => pl + (i / (n - 1)) * (W - pl - pr);
  const y = (v: number) => pt + (1 - v / max) * (H - pt - pb);
  const ticks = $derived(Array.from({ length: Math.round(max / step) + 1 }, (_, i) => i * step));

  function niceStep(raw: number) {
    const p = 10 ** Math.floor(Math.log10(raw || 1));
    const f = raw / p;
    return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
  }
  function ago(i: number) {
    if (!ts.length) return '';
    const s = Math.round((ts[ts.length - 1] - ts[Math.min(i, ts.length - 1)]) / 1000);
    return s <= 0 ? 'сейчас' : s < 90 ? `−${s} с` : `−${Math.round(s / 60)} мин`;
  }
  function clock(i: number) {
    return ts[i] ? new Date(ts[i]).toTimeString().slice(0, 8) : '';
  }

  let hover = $state<number | null>(null);
  let svg = $state<SVGSVGElement>();
  function move(e: PointerEvent) {
    if (!svg) return;
    const r = svg.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    const i = Math.round(((px - pl) / (W - pl - pr)) * (n - 1));
    hover = i >= 0 && i < n && series.some((s) => s.data.length > i) ? i : null;
  }
  function key(e: KeyboardEvent) {
    if (e.key === 'ArrowLeft') hover = Math.max(0, (hover ?? n) - 1);
    else if (e.key === 'ArrowRight') hover = Math.min(n - 1, (hover ?? -1) + 1);
    else if (e.key === 'Escape') hover = null;
  }
  const empty = $derived(series.every((s) => s.data.length < 2));
</script>

<div class="chart">
  {#if series.length > 1}
    <div class="legend">
      {#each series as s (s.label)}<span><i style="background:{s.color}"></i>{s.label}</span>{/each}
    </div>
  {/if}
  {#if empty}
    <div class="wait">Копим данные — график появится через несколько секунд.</div>
  {:else}
    <div class="plot">
      <!-- focusable so the crosshair also works from the keyboard (← →) -->
      <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
      <svg
        bind:this={svg}
        viewBox="0 0 {W} {H}"
        role="img"
        aria-label={label}
        tabindex="0"
        onpointermove={move}
        onpointerleave={() => (hover = null)}
        onkeydown={key}
        onblur={() => (hover = null)}
      >
        {#each ticks as v (v)}
          <line x1={pl} x2={W - pr} y1={y(v)} y2={y(v)} class="grid" />
          <text x={pl - 6} y={y(v) + 3.5} text-anchor="end" class="axis">{fmt(v)}</text>
        {/each}
        {#each [0, Math.round((n - 1) / 2), n - 1] as i, k (k)}
          <text x={x(i)} y={H - 6} text-anchor={k === 0 ? 'start' : k === 2 ? 'end' : 'middle'} class="axis">{ago(i)}</text>
        {/each}
        {#each series as s (s.label)}
          {#if s.data.length}
            {#if bars}
              {@const bw = Math.max(2, (W - pl - pr) / n - 2)}
              {#each s.data as v, i (i)}
                <rect x={x(i) - bw / 2} y={y(v)} width={bw} height={Math.max(0, y(0) - y(v))} fill={s.color} rx="1" opacity={hover === i ? 1 : 0.8} />
              {/each}
            {:else}
              {@const pts = s.data.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)}
              {#if s.fill}
                <path d="M{x(0)},{y(0)} L{pts.join(' L')} L{x(s.data.length - 1)},{y(0)} Z" fill={s.color} opacity="0.13" />
              {/if}
              <polyline points={pts.join(' ')} fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" />
              <circle cx={x(s.data.length - 1)} cy={y(s.data[s.data.length - 1])} r="3.5" fill={s.color} stroke="var(--surface)" stroke-width="2" />
            {/if}
          {/if}
        {/each}
        {#if hover !== null}
          <line x1={x(hover)} x2={x(hover)} y1={pt} y2={H - pb} class="cross" />
          {#if !bars}
            {#each series as s (s.label)}
              {#if s.data[hover] !== undefined}
                <circle cx={x(hover)} cy={y(s.data[hover])} r="4" fill={s.color} stroke="var(--surface)" stroke-width="2" />
              {/if}
            {/each}
          {/if}
        {/if}
      </svg>
      {#if hover !== null}
        <div class="tip" style="left:{(x(hover) / W) * 100}%" class:flip={x(hover) > W * 0.6}>
          <div class="t">{clock(hover)}</div>
          {#each series as s (s.label)}
            <div class="r"><i style="background:{s.color}"></i><b>{fmt(s.data[hover] ?? 0)}</b><span>{s.label}</span></div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .chart {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .legend {
    display: flex;
    gap: 14px;
    flex-wrap: wrap;
    font-size: 12px;
    color: var(--muted);
  }
  .legend span {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .legend i,
  .tip i {
    width: 12px;
    height: 2px;
    border-radius: 2px;
    display: inline-block;
  }
  .plot {
    position: relative;
  }
  svg {
    display: block;
    width: 100%;
    height: auto;
    touch-action: pan-y;
  }
  svg:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
    border-radius: 6px;
  }
  .grid {
    stroke: var(--grid);
    stroke-width: 1;
  }
  .axis {
    font-size: 10px;
    fill: var(--muted);
    font-family: var(--font-mono);
  }
  .cross {
    stroke: var(--muted);
    stroke-width: 1;
    stroke-dasharray: 3 3;
  }
  .tip {
    position: absolute;
    top: 4px;
    transform: translateX(10px);
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    box-shadow: var(--shadow);
    padding: 6px 10px;
    font-size: 12px;
    pointer-events: none;
    white-space: nowrap;
    z-index: 2;
  }
  .tip.flip {
    transform: translateX(calc(-100% - 10px));
  }
  .tip .t {
    color: var(--muted);
    font-family: var(--font-mono);
    font-size: 11px;
    margin-bottom: 2px;
  }
  .tip .r {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .tip b {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }
  .tip span {
    color: var(--muted);
  }
  .wait {
    padding: 42px 12px;
    text-align: center;
    color: var(--muted);
    font-size: 13px;
  }
</style>
