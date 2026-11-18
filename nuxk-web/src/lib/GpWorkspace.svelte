<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  let { active, go, children }: { active: string; go: (tab: string) => void; children: Snippet } = $props();
  const links = [
    { id: 'runs', label: 'Поиск', icon: 'runs' },
    { id: 'strategies', label: 'Результаты', icon: 'strategies' },
    { id: 'results', label: 'История поисков', icon: 'logs' },
    { id: 'gpdata', label: 'Списки доменов', icon: 'lists' },
    { id: 'gpservice', label: 'Обслуживание', icon: 'system' },
  ];
</script>

<section class="gp-workspace" aria-label="GP Plane">
  <header class="gp-header"><span class="gp-mark">gp</span><strong>GP Plane</strong><span class="hint">Поиск стратегий</span><span class="spacer"></span><span class="hint">Core API 2</span></header>
  <nav aria-label="Разделы GP Plane">
    {#each links as link}<button class:active={active === link.id} aria-current={active === link.id ? 'page' : undefined} onclick={() => go(link.id)}><Icon name={link.icon} size={16} />{link.label}</button>{/each}
  </nav>
  <div class="gp-content">{@render children()}</div>
</section>

<style>
  .gp-workspace { width: 100%; max-width: 1180px; margin-inline: auto; min-width: 0; border: 1px solid var(--line); border-radius: 12px; overflow: hidden; container-type: inline-size; }
  .gp-header { display: flex; align-items: center; gap: 10px; padding: 18px 24px; border-bottom: 1px solid var(--line); background: var(--surface); }
  .gp-mark { display: grid; place-items: center; width: 34px; height: 34px; border-radius: 9px; background: var(--surface-2); color: var(--accent); font-size: 19px; font-weight: 700; letter-spacing: -1px; }
  .gp-header strong { font-size: 17px; }
  nav { display: flex; gap: 8px; overflow-x: auto; padding: 0 24px; border-bottom: 1px solid var(--line); background: var(--surface); }
  nav button { background: none; color: var(--ink-2); border: 0; border-bottom: 2px solid transparent; border-radius: 0; padding: 14px 12px; font-size: 13px; }
  nav button.active { color: var(--accent); border-bottom-color: var(--accent); }
  .gp-content { padding: 28px 26px; min-height: 568px; }
  .gp-content :global(.card) { padding: 24px; border-radius: 8px; box-shadow: none; }
  .gp-content :global(.card-head h2) { font-size: 17px; }
  .gp-content :global(input[type=text]), .gp-content :global(select), .gp-content :global(input[type=number]) { min-height: 42px; padding: 10px 11px; border: 1px solid var(--line); border-radius: 6px; background: var(--surface); color: var(--ink); width: 100%; }
  .gp-content :global(button) { min-height: 38px; white-space: normal; }
  .gp-content :global(.hint) { color: var(--ink-2); }
  @container (max-width: 690px) { .gp-content { padding: 20px 16px; } .gp-content :global(.card) { padding: 18px; } .gp-header { padding: 14px 16px; } nav { padding-inline: 12px; } .gp-header > .hint:first-of-type { display: none; } }
</style>
