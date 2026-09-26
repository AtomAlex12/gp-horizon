<script lang="ts">
  // A card's small trend: the same time window as the big charts, newest on
  // the right, so it scrolls rather than squeezes.
  let { data, ts, span, color, label }: { data: number[]; ts: number[]; span: number; color: string; label: string } =
    $props();
  const W = 120,
    H = 30;
  const tEnd = $derived(ts.length ? ts[ts.length - 1] : 0);
  // data is aligned to the end of ts; keep the samples inside the window
  const pts = $derived.by(() => {
    const off = ts.length - data.length;
    const out: [number, number][] = [];
    for (let j = 0; j < data.length; j++) {
      const t = ts[j + off];
      if (t !== undefined && t >= tEnd - span) out.push([W - ((tEnd - t) / span) * W, data[j]]);
    }
    return out;
  });
  const max = $derived(Math.max(...pts.map((p) => p[1]), 1e-6));
  const line = $derived(pts.map(([x, v]) => `${x.toFixed(1)},${(H - 2 - (v / max) * (H - 4)).toFixed(1)}`));
</script>

{#if line.length > 1}
  <svg viewBox="0 0 {W} {H}" width={W} height={H} role="img" aria-label={label}>
    <path d="M{pts[0][0].toFixed(1)},{H} L{line.join(' L')} L{W},{H} Z" fill={color} opacity="0.14" />
    <polyline points={line.join(' ')} fill="none" stroke={color} stroke-width="1.5" />
  </svg>
{/if}
