<script lang="ts">
  // Time-series chart in the console's style: recessive grid, 2px lines with a
  // light fill (or thin bars), a crosshair that snaps to the nearest sample
  // and one tooltip listing every series. The legend keys each series; values
  // stay in text ink, never in the series colour.
  //
  // The x axis is time over a fixed window (`span`) ending at the newest
  // sample: the line enters on the right and scrolls left, it never squeezes
  // as history grows. A pause in the samples (agent unreachable, tab asleep)
  // is a gap, not a straight line across it.
  type Series = { label: string; color: string; data: number[]; fill?: boolean };
  let {
    series,
    ts,
    fmt,
    label,
    span = 10 * 60_000,
    bars = false,
    min = 1,
  }: {
    series: Series[];
    ts: number[];
    fmt: (v: number) => string;
    label: string;
    span?: number; // visible window, ms
    bars?: boolean;
    min?: number;
  } = $props();

  const W = 640,
    H = 200,
    pl = 52,
    pr = 10,
    pt = 10,
    pb = 22;
  const PW = W - pl - pr;
  const MAX_BARS = 120;

  const tEnd = $derived(ts.length ? ts[ts.length - 1] : 0);
  const t0 = $derived(tEnd - span);
  // first sample inside the window; one before it too, so a line reaches the edge
  const first = $derived.by(() => {
    let i = 0;
    while (i < ts.length && ts[i] < t0) i++;
    return i;
  });
  const from = $derived(Math.max(0, first - 1));
  // typical sampling step: median of the gaps (5 s for both agent and controller)
  const stepMs = $derived.by(() => {
    const d: number[] = [];
    for (let i = Math.max(1, ts.length - 60); i < ts.length; i++) d.push(ts[i] - ts[i - 1]);
    d.sort((a, b) => a - b);
    return d.length ? Math.max(1000, d[d.length >> 1]) : 5000;
  });
  const gapMs = $derived(Math.max(3 * stepMs, 20_000));

  // a series is aligned to the END of ts (a tunnel may appear later than the WAN)
  const at = (s: Series, i: number) => s.data[i - (ts.length - s.data.length)];

  const visibleMax = $derived.by(() => {
    let m = min;
    for (const s of series) for (let i = first; i < ts.length; i++) m = Math.max(m, at(s, i) ?? 0);
    return m;
  });
  const step = $derived(niceStep(visibleMax / 4));
  const max = $derived(Math.ceil(visibleMax / step) * step);
  const x = (t: number) => pl + ((t - t0) / span) * PW;
  const y = (v: number) => pt + (1 - v / max) * (H - pt - pb);
  const ticks = $derived(Array.from({ length: Math.round(max / step) + 1 }, (_, i) => i * step));

  function niceStep(raw: number) {
    const p = 10 ** Math.floor(Math.log10(raw || 1));
    const f = raw / p;
    return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * p;
  }
  function ago(ms: number) {
    const s = Math.round(ms / 1000);
    if (s <= 0) return 'сейчас';
    if (s < 90) return `−${s} с`;
    if (s < 3600) return `−${Math.round(s / 60)} мин`;
    return `−${+(s / 3600).toFixed(1)} ч`;
  }
  const clock = (t: number) => (t ? new Date(t).toTimeString().slice(0, 8) : '');

  // runs of samples without a pause, per series: [from, to] index ranges
  const runs = $derived.by(() => {
    const out: [number, number][] = [];
    let a = from;
    for (let i = from + 1; i <= ts.length; i++) {
      if (i === ts.length || ts[i] - ts[i - 1] > gapMs) {
        out.push([a, i - 1]);
        a = i;
      }
    }
    return out;
  });
  function path(s: Series, [a, b]: [number, number]) {
    const pts: string[] = [];
    for (let i = a; i <= b; i++) {
      const v = at(s, i);
      if (v !== undefined) pts.push(`${x(ts[i]).toFixed(1)},${y(v).toFixed(1)}`);
    }
    return pts;
  }

  // bars: samples averaged into time buckets so a long window stays readable.
  // Buckets sit on absolute time, so a bar keeps its value as the window moves.
  const bucketMs = $derived(Math.max(stepMs, Math.ceil(span / stepMs / MAX_BARS) * stepMs));
  type Bucket = { t: number; v: number[] };
  const buckets = $derived.by(() => {
    if (!bars) return [] as Bucket[];
    const m = new Map<number, { t: number; sum: number[]; n: number }>();
    for (let i = from; i < ts.length; i++) {
      const k = Math.floor(ts[i] / bucketMs);
      const b = m.get(k) ?? { t: 0, sum: series.map(() => 0), n: 0 };
      series.forEach((s, j) => (b.sum[j] += at(s, i) ?? 0));
      b.n++;
      b.t = ts[i]; // a bar ends at its newest sample: the rate over the time before it
      m.set(k, b);
    }
    return [...m.values()].map((b) => ({ t: b.t, v: b.sum.map((x) => x / b.n) }));
  });
  const bw = $derived(Math.max(1, (bucketMs / span) * PW - 2));

  // hover: a sample index (lines) or a bucket index (bars)
  let hover = $state<number | null>(null);
  let svg = $state<SVGSVGElement>();
  function nearest(t: number): number | null {
    if (bars) {
      let best: number | null = null;
      buckets.forEach((b, k) => {
        if (best === null || Math.abs(b.t - t) < Math.abs(buckets[best].t - t)) best = k;
      });
      return best;
    }
    let best: number | null = null;
    for (let i = first; i < ts.length; i++) if (best === null || Math.abs(ts[i] - t) < Math.abs(ts[best] - t)) best = i;
    return best;
  }
  function move(e: PointerEvent) {
    if (!svg) return;
    const r = svg.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    hover = px < pl || px > W - pr ? null : nearest(t0 + ((px - pl) / PW) * span);
  }
  function key(e: KeyboardEvent) {
    const lo = bars ? 0 : first,
      hi = bars ? buckets.length - 1 : ts.length - 1;
    if (e.key === 'ArrowLeft') hover = Math.max(lo, (hover ?? hi + 1) - 1);
    else if (e.key === 'ArrowRight') hover = Math.min(hi, (hover ?? lo - 1) + 1);
    else if (e.key === 'Escape') hover = null;
  }
  const hoverT = $derived(hover === null ? 0 : bars ? (buckets[hover]?.t ?? 0) : ts[hover]);
  const hoverV = (j: number) => (hover === null ? undefined : bars ? buckets[hover]?.v[j] : at(series[j], hover));

  const empty = $derived(series.every((s) => s.data.length < 2) || ts.length - first < 2);
  const clipId = `c${Math.random().toString(36).slice(2, 8)}`;
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
        <defs><clipPath id={clipId}><rect x={pl} y="0" width={PW} height={H} /></clipPath></defs>
        {#each ticks as v (v)}
          <line x1={pl} x2={W - pr} y1={y(v)} y2={y(v)} class="grid" />
          <text x={pl - 6} y={y(v) + 3.5} text-anchor="end" class="axis">{fmt(v)}</text>
        {/each}
        {#each [0, 0.5, 1] as f, k (k)}
          <text x={pl + f * PW} y={H - 6} text-anchor={k === 0 ? 'start' : k === 2 ? 'end' : 'middle'} class="axis">{ago((1 - f) * span)}</text>
        {/each}
        <g clip-path="url(#{clipId})">
          {#if bars}
            {#each buckets as b, k (b.t)}
              {#each series as s, j (s.label)}
                <rect x={x(b.t) - bw - 1} y={y(b.v[j])} width={bw} height={Math.max(0, y(0) - y(b.v[j]))} fill={s.color} rx="1" opacity={hover === k ? 1 : 0.8} />
              {/each}
            {/each}
          {:else}
            {#each series as s (s.label)}
              {#each runs as r (r[0])}
                {@const pts = path(s, r)}
                {#if pts.length > 1}
                  {#if s.fill}
                    <path d="M{pts[0].split(',')[0]},{y(0)} L{pts.join(' L')} L{pts[pts.length - 1].split(',')[0]},{y(0)} Z" fill={s.color} opacity="0.13" />
                  {/if}
                  <polyline points={pts.join(' ')} fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" />
                {/if}
              {/each}
              {#if at(s, ts.length - 1) !== undefined}
                <circle cx={x(tEnd)} cy={y(at(s, ts.length - 1))} r="3.5" fill={s.color} stroke="var(--surface)" stroke-width="2" />
              {/if}
            {/each}
          {/if}
        </g>
        {#if hover !== null}
          <line x1={x(hoverT)} x2={x(hoverT)} y1={pt} y2={H - pb} class="cross" />
          {#if !bars}
            {#each series as s, j (s.label)}
              {#if hoverV(j) !== undefined}
                <circle cx={x(hoverT)} cy={y(hoverV(j) ?? 0)} r="4" fill={s.color} stroke="var(--surface)" stroke-width="2" />
              {/if}
            {/each}
          {/if}
        {/if}
      </svg>
      {#if hover !== null}
        <div class="tip" style="left:{(x(hoverT) / W) * 100}%" class:flip={x(hoverT) > W * 0.6}>
          <div class="t">{clock(hoverT)}{bars && bucketMs > stepMs ? ` · среднее за ${ago(bucketMs).slice(1)}` : ''}</div>
          {#each series as s, j (s.label)}
            <div class="r"><i style="background:{s.color}"></i><b>{fmt(hoverV(j) ?? 0)}</b><span>{s.label}</span></div>
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
