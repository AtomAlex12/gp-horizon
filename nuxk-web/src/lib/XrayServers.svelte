<script lang="ts">
  // The servers to choose from, under the server card. Lite: the servers of
  // the router's one subscription — "Выбрать" switches xray to another one.
  // Full: the links and subscriptions kept on the Pi (VlessSources).
  import { api } from './api';
  import { node } from './status.svelte';
  import { xray, change } from './xray.svelte';
  import VlessSources from './VlessSources.svelte';

  const up = $derived(xray.up);
</script>

{#if !__LITE__ && node.via === 'controller'}
  <VlessSources />
{:else if up?.source === 'subscription'}
  <section class="card">
    <div class="card-head">
      <h2>Серверы подписки</h2>
      <span class="chip">{up.servers.length}</span>
      <span class="spacer"></span>
      <button class="ghost sm" onclick={() => change(() => api.refreshUpstream('xray'))} disabled={xray.busy}>
        {xray.busy ? 'Обновляю…' : 'Обновить подписку'}
      </button>
    </div>
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Сервер</th><th>Адрес</th><th>Защита / транспорт</th><th></th></tr></thead>
        <tbody>
          {#each up.servers as s, i (s.key)}
            <tr class:on={i === up.active}>
              <td><b>{s.name || '—'}</b></td>
              <td class="mono">{s.address}</td>
              <td class="mono muted">{s.security} / {s.network}{s.flow ? ` · ${s.flow}` : ''}</td>
              <td class="act">
                {#if i === up.active}<span class="chip ok">на роутере</span>
                {:else}<button class="ghost sm" onclick={() => change(() => api.pickServer('xray', s.key))} disabled={xray.busy}>Выбрать</button>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if up.skipped}<p class="hint">Ещё {up.skipped} ссылок в подписке не удалось разобрать — они пропущены.</p>{/if}
    <p class="hint">
      Выбор перезапускает xray с другим сервером (пара секунд); если с ним он не поднимется — вернётся прежний. Больше
      одной подписки — в полной версии, с контроллером на Pi.
    </p>
  </section>
{/if}

<style>
  tr.on td {
    background: color-mix(in srgb, var(--accent) 8%, transparent);
  }
  .act {
    text-align: right;
    white-space: nowrap;
  }
  .muted {
    color: var(--muted);
  }
</style>
