<script lang="ts">
  let { data, color, label }: { data: number[]; color: string; label: string } = $props();
  const W = 120,
    H = 30;
  const max = $derived(Math.max(...data, 1e-6));
  const pts = $derived(
    data.map((v, i) => `${((i / Math.max(1, data.length - 1)) * W).toFixed(1)},${(H - 2 - (v / max) * (H - 4)).toFixed(1)}`),
  );
</script>

{#if data.length > 1}
  <svg viewBox="0 0 {W} {H}" width={W} height={H} role="img" aria-label={label}>
    <path d="M0,{H} L{pts.join(' L')} L{W},{H} Z" fill={color} opacity="0.14" />
    <polyline points={pts.join(' ')} fill="none" stroke={color} stroke-width="1.5" />
  </svg>
{/if}
