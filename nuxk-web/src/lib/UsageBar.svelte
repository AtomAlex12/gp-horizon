<script lang="ts">
  // A subscription's traffic and term, as its panel reports them (3x-ui's
  // subscription-userinfo): a bar when there's a limit, the date it ends.
  import { fmtBytes } from './ui';

  let { usage }: { usage: { upload: number; download: number; total: number; expire: number } } = $props();

  const used = $derived(usage.upload + usage.download);
  const pct = $derived(usage.total > 0 ? Math.min(100, (used / usage.total) * 100) : 0);
  const days = $derived(usage.expire > 0 ? Math.floor((usage.expire * 1000 - Date.now()) / 86_400_000) : null);
  const date = (unix: number) => new Date(unix * 1000).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' });
</script>

<div class="usage">
  <div class="line">
    <span>Трафик</span>
    <b>{fmtBytes(used)}</b>
    <span class="muted">{usage.total > 0 ? `из ${fmtBytes(usage.total)}` : 'без лимита'}</span>
  </div>
  {#if usage.total > 0}
    <div class="bar" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(pct)} aria-label="Израсходовано">
      <i class:hot={pct >= 90} style="width:{pct}%"></i>
    </div>
  {/if}
  <div class="line">
    <span>Срок</span>
    {#if days === null}<b>бессрочно</b>
    {:else if days < 0}<b class="err">истекла {date(usage.expire)}</b>
    {:else}<b class:warnc={days <= 7}>до {date(usage.expire)}</b><span class="muted">· ещё {days} дн.</span>{/if}
  </div>
</div>

<style>
  .usage {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 13px;
  }
  .line {
    display: flex;
    gap: 6px;
    align-items: baseline;
    flex-wrap: wrap;
  }
  .line > span:first-child {
    color: var(--muted);
    min-width: 52px;
  }
  .muted {
    color: var(--muted);
  }
  .bar {
    height: 6px;
    border-radius: 3px;
    background: var(--line);
    overflow: hidden;
  }
  .bar i {
    display: block;
    height: 100%;
    background: var(--accent);
    border-radius: 3px;
  }
  .bar i.hot {
    background: var(--warn);
  }
  .err {
    color: var(--warn);
  }
  .warnc {
    color: var(--degraded);
  }
</style>
