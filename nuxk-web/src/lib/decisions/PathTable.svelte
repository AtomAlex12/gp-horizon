<script lang="ts">
  // One domain's ways out as measured: how often it opened, the first byte,
  // the speed, the ~16 KB freeze and whether that is good enough by the
  // settings. Nothing is filled in that wasn't measured.
  import type { SuggestPath } from '../api';
  import { VIA_TITLE, mbit, msText } from './text';

  let { paths }: { paths: SuggestPath[] } = $props();
</script>

<div class="tbl-wrap">
  <table>
    <thead>
      <tr>
        <th>путь</th>
        <th>результат</th>
        <th>открылось</th>
        <th>первый байт</th>
        <th>скорость</th>
        <th>замирание</th>
        <th>итог</th>
      </tr>
    </thead>
    <tbody>
      {#each paths as p, i (i)}
        {@const measured = p.tries > 0}
        <tr>
          <td class="via"><span class="mode {p.via === 'dns' ? 'direct' : p.via}">{VIA_TITLE[p.via] ?? p.via}</span></td>
          <td class="res">
            {p.result}
            {#if p.ms}<small>попытка ≈ {msText(p.ms)}</small>{/if}
          </td>
          <td class="num nowrap">{measured ? `${p.opened} из ${p.tries}` : '—'}</td>
          <td class="num nowrap">{msText(p.ttfb_ms)}</td>
          <td class="num nowrap" class:muted={!p.kbps}>{measured ? mbit(p.kbps) : '—'}</td>
          <td class="nowrap">
            {#if p.frozen}
              <span class="chip warn">замирает</span>
            {:else if p.opened > 0}
              <span class="muted">нет</span>
            {:else}
              <span class="muted">—</span>
            {/if}
          </td>
          <td class="nowrap">
            {#if !measured}
              <span class="muted">не измерен</span>
            {:else if p.acceptable}
              <span class="chip ok">подходит</span>
            {:else}
              <span class="chip">не подходит</span>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  table {
    font-size: 12.5px;
  }
  td {
    vertical-align: top;
  }
  .res {
    min-width: 180px;
    max-width: 360px;
    color: var(--ink-2);
  }
  .res small {
    display: block;
    color: var(--muted);
    font-size: 11.5px;
  }
  .nowrap,
  .via {
    white-space: nowrap;
  }
  .muted {
    color: var(--muted);
  }
</style>
