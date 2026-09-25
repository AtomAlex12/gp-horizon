<script lang="ts">
  import { status, node, startPolling, pollNow } from './lib/status.svelte';
  import { setToken, type EngineKind } from './lib/api';
  import Icon from './lib/Icon.svelte';
  import Dashboard from './lib/views/Dashboard.svelte';
  import Connections from './lib/views/Connections.svelte';
  import EngineView from './lib/views/EngineView.svelte';
  import Lists from './lib/views/Lists.svelte';
  import Logs from './lib/views/Logs.svelte';
  import System from './lib/views/System.svelte';
  import Soon from './lib/views/Soon.svelte';
  import { planeOf, healthDot, roleLabel, last } from './lib/ui';
  import { history as hist } from './lib/status.svelte';

  startPolling();

  const TABS: Record<string, { label: string; icon: string }> = {
    dashboard: { label: 'Дашборд', icon: 'overview' },
    conns: { label: 'Соединения', icon: 'conns' },
    devices: { label: 'Устройства', icon: 'devices' },
    runs: { label: 'Прогоны', icon: 'runs' },
    results: { label: 'Результаты', icon: 'results' },
    strategies: { label: 'Стратегии', icon: 'strategies' },
    decisions: { label: 'Решения', icon: 'decisions' },
    lists: { label: 'Списки', icon: 'lists' },
    dns: { label: 'DNS', icon: 'dns' },
    nfqws2: { label: 'nfqws2', icon: 'nfqws2' },
    usque: { label: 'usque (WARP)', icon: 'usque' },
    xray: { label: 'xray (VLESS)', icon: 'xray' },
    logs: { label: 'Логи', icon: 'logs' },
    system: { label: 'Система', icon: 'system' },
  };
  const NAV = [
    { title: 'Статус', items: ['dashboard', 'conns', 'devices'] },
    { title: 'Подбор', items: ['runs', 'results', 'strategies', 'decisions'] },
    { title: 'Списки и данные', items: ['lists', 'dns'] },
    { title: 'Движки', items: ['nfqws2', 'usque', 'xray'] },
    { title: 'Управление', items: ['logs', 'system'] },
  ];
  // screens the design has but the agent doesn't feed yet — said plainly
  const SOON: Record<string, string> = {
    devices: 'Устройства LAN и что у каждого не открывается — появится вместе со списком соединений.',
    runs: 'Прогоны стратегий nfqws2 по вашим доменам: какая стратегия пробивает DPI вашего провайдера.',
    results: 'Результаты прогонов: какие стратегии сработали, с какой задержкой, для каких доменов.',
    strategies: 'Библиотека стратегий nfqws2 (zapret2) с описанием, откуда каждая и когда помогает.',
    decisions: 'Автоматический выбор пути для домена — десинк, туннель или напрямую — с объяснением, почему.',
    dns: 'DNS на роутере обслуживает сам Keenetic (списки работают через его маршрутизацию по доменам). Экран настроек DNS — позже.',
  };

  // #lists etc. — a reload keeps the page
  const fromHash = () => {
    const h = location.hash.slice(1);
    return TABS[h] ? h : h === 'overview' ? 'dashboard' : 'dashboard';
  };
  let tab = $state(fromHash());
  let menuOpen = $state(false);
  function go(t: string) {
    tab = t;
    menuOpen = false;
    if (location.hash.slice(1) !== t) history.replaceState(null, '', `#${t}`);
    window.scrollTo(0, 0);
  }
  $effect(() => {
    const on = () => (tab = fromHash());
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  });

  // theme: system by default; a manual pick is remembered in this browser only
  const THEME_KEY = 'nuxk-theme';
  let theme = $state<'light' | 'dark' | ''>('');
  try {
    const t = localStorage.getItem(THEME_KEY);
    if (t === 'light' || t === 'dark') theme = t;
  } catch {
    /* storage blocked: follow the system */
  }
  $effect(() => {
    if (theme) document.documentElement.dataset.theme = theme;
    else delete document.documentElement.dataset.theme;
  });
  function toggleTheme() {
    const dark = theme ? theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
    theme = dark ? 'light' : 'dark';
    try {
      localStorage.setItem(THEME_KEY, theme);
    } catch {
      /* fine: lasts for this tab */
    }
  }

  const engines = $derived(status.data?.engines ?? []);
  const byKind = $derived(new Map(engines.map((e) => [e.kind, e])));
  const plane = $derived(planeOf(status.data));
  const conflicts = $derived((plane?.conflicts ?? []).length);
  const conns = $derived(hist.h?.conntrack.length ? last(hist.h.conntrack) : null);
  const where = $derived(
    node.info?.role === 'router' && node.info.firmware ? `KeeneticOS ${node.info.firmware}` : node.info?.hostname ?? location.host,
  );

  let tokenInput = $state('');
  function login(e: SubmitEvent) {
    e.preventDefault();
    setToken(tokenInput);
    tokenInput = '';
    pollNow();
  }
</script>

<div class="app">
  {#if menuOpen}<button class="scrim" aria-label="Закрыть меню" onclick={() => (menuOpen = false)}></button>{/if}
  <aside class="side" class:open={menuOpen} aria-label="Разделы">
    <div class="brand">
      <svg class="brand-mark" viewBox="0 0 28 28" aria-hidden="true"
        ><circle cx="14" cy="14" r="12.5" fill="none" stroke="var(--accent)" stroke-width="2" /><path
          d="M3 17c4-3 8-3 11 0s7 3 11 0"
          fill="none"
          stroke="var(--accent)"
          stroke-width="2"
        /><circle cx="14" cy="10" r="3" fill="var(--accent)" /></svg
      >
      <div><b>nuxk Horizon</b><small>{roleLabel(node.info)} · {where}</small></div>
    </div>
    {#each NAV as g (g.title)}
      <div class="nav-group">
        <div class="nav-title">{g.title}</div>
        {#each g.items as k (k)}
          {@const e = byKind.get(k as EngineKind)}
          <button class="nav-item" class:active={tab === k} aria-current={tab === k ? 'page' : undefined} onclick={() => go(k)}>
            <Icon name={TABS[k].icon} />
            <span>{TABS[k].label}</span>
            {#if e}
              <span class="dot {healthDot(e)} end"></span>
            {:else if k === 'lists' && conflicts}
              <span class="count end warnc">{conflicts}</span>
            {:else if k === 'conns' && conns !== null}
              <span class="count end">{conns}</span>
            {/if}
          </button>
        {/each}
      </div>
    {/each}
  </aside>

  <div class="main">
    <header class="top">
      <button class="ghost sm menu-btn" onclick={() => (menuOpen = true)} aria-label="Меню"><Icon name="menu" size={16} /></button>
      <h1>{TABS[tab].label}</h1>
      <span class="spacer"></span>
      {#if node.info?.role === 'stand'}
        <span class="chip deg" title="Тестовый стенд в Docker на Pi — не роутер">стенд, не роутер</span>
      {/if}
      {#if status.data}
        <span class="chip" title={node.via === 'controller' ? 'Интерфейс открыт через nuxk-controller на Pi' : 'Интерфейс открыт напрямую с узла'}>
          <span class="dot {status.error || (node.agent && !node.agent.reachable) ? 'warn' : 'ok'}" class:pulse={status.live}></span>
          {node.via === 'controller' ? 'контроллер' : 'агент'}
        </span>
        <span class="muted mono ver">core {status.data.version}</span>
      {/if}
      <button class="ghost sm" onclick={toggleTheme} aria-label="Сменить тему"><Icon name={theme === 'dark' ? 'sun' : 'moon'} size={15} /></button>
    </header>

    <main class="content">
      {#if status.loading}
        <p class="muted">Подключение к nuxk-core…</p>
      {:else if status.needToken}
        <form class="card login" onsubmit={login}>
          <h2>Вход в nuxk</h2>
          <p class="hint">
            Введите API-токен. Его показал установщик; он же лежит в <span class="mono">/opt/etc/nuxk/nuxk.conf</span>
            (строка <span class="mono">API_TOKEN</span>).
          </p>
          <input type="password" id="api-token" bind:value={tokenInput} autocomplete="current-password" placeholder="токен" />
          <button type="submit" disabled={!tokenInput.trim()}>Войти</button>
        </form>
      {:else if status.error && !status.data}
        <p class="err-text">nuxk-core недоступен: {status.error}</p>
      {:else}
        {#if status.error}<div class="banner warn">Нет связи с nuxk-core: {status.error}. Показаны последние данные.</div>{/if}
        {#if tab === 'dashboard'}
          <Dashboard {go} />
        {:else if tab === 'conns'}
          <Connections />
        {:else if tab === 'lists'}
          <Lists />
        {:else if tab === 'logs'}
          <Logs />
        {:else if tab === 'system'}
          <System />
        {:else if tab === 'nfqws2' || tab === 'usque' || tab === 'xray'}
          <EngineView kind={tab as EngineKind} {go} />
        {:else if SOON[tab]}
          <Soon title={TABS[tab].label} what={SOON[tab]} />
        {/if}
      {/if}
    </main>
  </div>
</div>

<style>
  .app {
    display: grid;
    grid-template-columns: 232px 1fr;
    min-height: 100%;
  }
  .side {
    background: var(--surface);
    border-right: 1px solid var(--line);
    position: sticky;
    top: 0;
    height: 100vh;
    overflow-y: auto;
    padding: 16px 12px 24px;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 4px 8px 16px;
  }
  .brand-mark {
    width: 28px;
    height: 28px;
    flex: none;
  }
  .brand b {
    font-weight: 600;
    font-size: 15px;
    letter-spacing: 0.2px;
    display: block;
    line-height: 1.2;
  }
  .brand small {
    color: var(--muted);
    font-size: 11.5px;
  }
  .nav-group {
    margin-top: 14px;
  }
  .nav-title {
    font-size: 10.5px;
    text-transform: uppercase;
    letter-spacing: 0.09em;
    color: var(--muted);
    padding: 0 10px 6px;
    font-weight: 600;
  }
  .nav-item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    text-align: left;
    background: none;
    border: 0;
    padding: 7px 10px;
    border-radius: 8px;
    color: var(--ink-2);
    font-size: 13.5px;
    font-weight: 400;
  }
  .nav-item:hover {
    background: var(--surface-2);
    color: var(--ink);
    filter: none;
  }
  .nav-item.active {
    background: var(--accent-soft);
    color: var(--accent);
    font-weight: 500;
  }
  .end {
    margin-left: auto;
  }
  .count {
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--muted);
  }
  .count.warnc {
    color: var(--degraded);
  }
  .main {
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .top {
    position: sticky;
    top: 0;
    z-index: 5;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 24px;
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--line);
  }
  .top h1 {
    font-size: 18px;
    font-weight: 600;
  }
  .ver {
    font-size: 12px;
  }
  .menu-btn {
    display: none;
  }
  .content {
    padding: 20px 24px 40px;
    display: flex;
    flex-direction: column;
    gap: 16px;
    max-width: 1240px;
    width: 100%;
  }
  .login {
    display: flex;
    flex-direction: column;
    gap: 10px;
    max-width: 420px;
  }
  .login h2 {
    font-size: 16px;
  }
  .linkbtn {
    display: inline;
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    font-size: inherit;
    text-decoration: underline;
  }
  .scrim {
    display: none;
  }
  @media (max-width: 860px) {
    .app {
      grid-template-columns: 1fr;
    }
    .side {
      position: fixed;
      z-index: 30;
      left: 0;
      top: 0;
      bottom: 0;
      width: 260px;
      height: 100%;
      transform: translateX(-100%);
      transition: transform 0.2s;
    }
    .side.open {
      transform: none;
      box-shadow: var(--shadow);
    }
    .scrim {
      display: block;
      position: fixed;
      inset: 0;
      z-index: 20;
      background: rgba(20, 18, 14, 0.4);
      border: 0;
      border-radius: 0;
      padding: 0;
    }
    .menu-btn {
      display: inline-flex;
    }
    .top {
      padding: 10px 16px;
      gap: 8px;
    }
    .ver {
      display: none;
    }
    .content {
      padding: 16px 16px 32px;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .side {
      transition: none;
    }
  }
</style>
