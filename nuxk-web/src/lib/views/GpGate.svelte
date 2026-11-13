<script lang="ts">
  // The GP tabs (Прогоны / Результаты / Стратегии) open only where GP can
  // run: in the controller on the Pi, with the plugin installed and up.
  // Otherwise: say plainly why, and where to go.
  import type { Snippet } from 'svelte';
  import { node } from '../status.svelte';
  import { plugins, PHASE_LABEL, type PluginInfo, type PluginsState } from '../gp';

  import { gp2, type GP } from '../gp2';
  import { HttpError } from '../api';
  let { children, api2, go }: { children: Snippet; api2: Snippet; go: (tab: string) => void } = $props();

  let st = $state<PluginsState | null>(null);
  let loading = false;
  let checkedVersion: string | undefined;
  let currentAPI = $state<string>();
  let err = $state('');
  const gpInfo = $derived<PluginInfo | undefined>(st?.plugins.find((p) => p.name === 'gp'));

  async function load() {
    if (loading) return;
    loading = true;
    try {
      st = await plugins.list();
      const info = st.plugins.find(item => item.name === 'gp');
      if (info?.phase !== 'running') {
        currentAPI = undefined;
        checkedVersion = undefined;
      } else if (!currentAPI || checkedVersion !== info.version) {
        currentAPI = undefined;
        if (info.version === 'v0.5.0-alpha.4' || info.version === '8748e6c39248ddda5ee8ce7f32d3b5049f2432b8') {
          currentAPI = '2';
        } else {
          try {
            currentAPI = (await gp2<GP['CoreStatus']>('service/status')).api_version;
          } catch (reason) {
            if (!(reason instanceof HttpError) || reason.code !== 'gp_api_version') throw reason;
            currentAPI = '1';
          }
        }
        checkedVersion = info.version;
      }
      err = '';
    } catch (reason) {
      err = reason instanceof Error ? reason.message : String(reason);
    } finally {
      loading = false;
    }
  }

  $effect(() => {
    if (node.via !== 'controller') return;
    void load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  });
</script>

{#if node.via !== 'controller'}
  <section class="card empty">
    <h2>Работает в контроллере на Pi</h2>
    <p>
      Подбор стратегий (плагин GP) идёт на Raspberry Pi: откройте панель контроллера, а не страницу роутера.
      Найденную стратегию оттуда же можно применить к nfqws2 на роутере.
    </p>
  </section>
{:else if !st}
  <p class="muted">{err || 'Загрузка…'}</p>
{:else if !st.host}
  <section class="card empty">
    <h2>Плагины недоступны</h2>
    <p>{st.reason ?? 'Контроллер запущен без хоста плагинов.'}</p>
  </section>
{:else if gpInfo?.phase === 'running'}
  {#if currentAPI === '2'}{@render api2()}{:else if currentAPI === '1'}{@render children()}{:else}<p class="hint">{err || 'Проверяем версию API…'}</p>{/if}
{:else}
  <section class="card empty">
    <h2>Нужен плагин GP</h2>
    <p>
      {#if !gpInfo}
        В этой сборке контроллера нет рецепта плагина GP.
      {:else if gpInfo.phase === 'absent'}
        Подбор стратегий делает плагин GP (blockcheck2 из zapret2). Он ещё не установлен.
      {:else}
        Плагин GP сейчас: {PHASE_LABEL[gpInfo.phase]}.{gpInfo.last_error ? ` ${gpInfo.last_error}` : ''}
      {/if}
    </p>
    {#if gpInfo}<button onclick={() => go('plugins')}>Открыть «Плагины»</button>{/if}
  </section>
{/if}

<style>
  h2 {
    font-size: 15px;
    color: var(--ink);
    margin-bottom: 8px;
  }
  p {
    max-width: 560px;
    margin: 0 auto 12px;
  }
</style>
