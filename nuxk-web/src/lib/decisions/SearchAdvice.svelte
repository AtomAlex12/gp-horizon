<script lang="ts">
  // DPI with the known strategies didn't help: a strategy search on the Pi is
  // the next step, and — when one was measured good enough — a tunnel to use
  // meanwhile. The search itself lives in the Pi's build only.
  import { modeTitle } from '../ui';
  import ToRuns from './ToRuns.svelte';

  let { domains, interim }: { domains: string[]; interim?: string } = $props();
</script>

<div class="banner deg search">
  <div class="stack">
    <b>DPI с известными стратегиями не помог — подберите стратегию.</b>
    {#if interim}
      <span>
        Пока идёт подбор — можно через {modeTitle(interim)}: перенесите сайт в {modeTitle(interim)} в
        <a href="#lists">«Списках»</a>.
      </span>
    {/if}
    {#if !__LITE__}
      <div class="row">
        <ToRuns {domains} />
        <span class="hint">домены подставятся в форму нового прогона</span>
      </div>
    {:else}
      <span class="hint">
        Подбор стратегии делает GP на Raspberry Pi — он есть в интерфейсе контроллера на Pi (раздел «Прогоны»), в версии
        для роутера его нет.
      </span>
    {/if}
  </div>
</div>

<style>
  .search .stack {
    gap: 6px;
  }
  .search a {
    color: var(--accent);
  }
  .search .hint {
    color: var(--ink-2);
  }
</style>
