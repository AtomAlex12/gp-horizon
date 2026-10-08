<script lang="ts">
  // «Логи»: the router's agent live (SSE), and through the Pi also the
  // controller's own log — its web/API and the plugin host — in one stream.
  // «Отладка» turns on every step for a while (the router included, from the
  // Pi): engine scripts, Keenetic requests, commands, DNS failures, plugins.
  import { api, type DebugAll, type LogDebug, type LogEntry } from '../api';
  import { logs, logKey, status, node } from '../status.svelte';

  let { go }: { go: (tab: string) => void } = $props();

  const viaCtl = $derived(!__LITE__ && node.via === 'controller');

  type Src = 'router' | 'controller' | 'plugins';
  type Line = LogEntry & { src: Src; key: string };
  const SRC_LABEL: Record<Src, string> = { router: 'роутер', controller: 'Pi', plugins: 'плагины' };

  const LEVELS = ['all', 'warn', 'error'] as const;
  const SOURCES = ['all', 'router', 'pi'] as const;
  let level = $state<(typeof LEVELS)[number]>('all');
  let source = $state<(typeof SOURCES)[number]>('all');
  let q = $state('');
  let paused = $state(false);
  let frozen = $state<Line[]>([]);
  let box = $state<HTMLDivElement>();

  // the controller's log: polled while this page is open
  let ctl = $state<Line[]>([]);
  let ctlErr = $state('');
  let boot = 0; // the controller's start: after a restart (an update) its seq starts over
  async function pullCtl() {
    try {
      let r = await api.ctlLogs(ctl.length ? ctl[ctl.length - 1].seq : 0);
      if (boot && r.boot !== boot) r = await api.ctlLogs(0);
      if (r.boot !== boot) ctl = ctl.map((e) => ({ ...e, key: `${boot}:${e.key}` })); // the old life keeps its lines
      boot = r.boot;
      if (r.items.length) {
        const all = ctl.concat(r.items.map((e) => ({ ...e, key: `c${e.seq}` })));
        ctl = all.length > 3000 ? all.slice(all.length - 3000) : all;
      }
      ctlErr = '';
    } catch (e) {
      ctlErr = e instanceof Error ? e.message : String(e);
    }
  }

  // the debug switch: the controller's (all three) or the agent's own
  let dbg = $state<DebugAll | null>(null);
  let dbgErr = $state('');
  let minutes = $state(30);
  let switching = $state(false);
  const on = $derived(!!dbg && (dbg.controller.on || !!dbg.agent?.on));
  const until = $derived(dbg ? Math.max(dbg.controller.until ?? 0, dbg.agent?.until ?? 0) : 0);
  const forced = $derived(!!dbg?.agent?.forced);
  const asAll = (a: LogDebug): DebugAll => ({ controller: { on: false }, agent: a });

  async function readDebug() {
    try {
      dbg = viaCtl ? await api.debugAll() : asAll(await api.logsDebug());
      dbgErr = '';
    } catch (e) {
      dbgErr = e instanceof Error ? e.message : String(e);
    }
  }
  async function setDebug(want: boolean) {
    switching = true;
    try {
      dbg = viaCtl ? await api.setDebugAll(want, minutes) : asAll(await api.setLogsDebug(want, minutes));
      dbgErr = '';
      if (viaCtl) void pullCtl();
    } catch (e) {
      dbgErr = e instanceof Error ? e.message : String(e);
    } finally {
      switching = false;
    }
  }

  $effect(() => {
    void readDebug();
    if (!viaCtl) {
      const t = setInterval(readDebug, 15000);
      return () => clearInterval(t);
    }
    void pullCtl();
    const t = setInterval(pullCtl, 3000);
    const d = setInterval(readDebug, 15000); // it turns itself off
    return () => {
      clearInterval(t);
      clearInterval(d);
    };
  });

  const merged = $derived.by((): Line[] => {
    const router: Line[] = logs.items.map((e) => ({ ...e, src: 'router' as const, key: `r${logKey(e)}` }));
    if (!viaCtl) return router;
    return router.concat(ctl).sort((a, b) => a.ts - b.ts);
  });
  const src = $derived(paused ? frozen : merged);
  const shown = $derived(
    src
      .filter((e) => source === 'all' || (source === 'router' ? e.src === 'router' : e.src !== 'router'))
      .filter((e) => level === 'all' || (level === 'warn' ? e.level === 'warn' || e.level === 'error' : e.level === 'error'))
      .filter((e) => !q || `${e.msg} ${e.attrs ?? ''}`.toLowerCase().includes(q.toLowerCase()))
      .slice(-1000),
  );

  function toggle() {
    paused = !paused;
    if (paused) frozen = merged.slice();
  }
  // follow the tail unless the reader scrolled up
  $effect(() => {
    void shown.length;
    if (!box) return;
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    if (atBottom) queueMicrotask(() => box && (box.scrollTop = box.scrollHeight));
  });
  const clock = (ms: number) => new Date(ms).toTimeString().slice(0, 8);
  const hm = (ms: number) => new Date(ms).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });

  // everything collected, both sides, every level — a file to look at or send
  function download() {
    const pad = (n: number, w = 2) => String(n).padStart(w, '0');
    const stamp = (ms: number) => {
      const d = new Date(ms);
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
    };
    const text = merged
      .map((e) => `${stamp(e.ts)} ${e.level.toUpperCase().padEnd(5)} [${SRC_LABEL[e.src]}] ${e.msg}${e.attrs ? ' ' + e.attrs : ''}`)
      .join('\n');
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text + '\n'], { type: 'text/plain;charset=utf-8' }));
    const now = new Date();
    a.download = `gp-horizon-logs-${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}.txt`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  }
</script>

<section class="card">
  <div class="card-head">
    <h2>Логи</h2>
    <span class="chip {status.live ? 'ok' : ''}">{status.live ? 'в реальном времени' : 'обновление раз в 5 с'}</span>
    <span class="spacer"></span>
    <input type="text" bind:value={q} placeholder="поиск" aria-label="Поиск по логу" class="search" />
    {#if viaCtl}
      <div class="seg" role="group" aria-label="Источник">
        {#each SOURCES as s (s)}<button class:on={source === s} onclick={() => (source = s)}>{s === 'all' ? 'всё' : s === 'router' ? 'роутер' : 'Pi'}</button>{/each}
      </div>
    {/if}
    <div class="seg" role="group" aria-label="Уровень">
      {#each LEVELS as l (l)}<button class:on={level === l} onclick={() => (level = l)}>{l === 'all' ? 'все' : l === 'warn' ? 'предупреждения' : 'ошибки'}</button>{/each}
    </div>
    <button class="ghost sm" onclick={toggle}>{paused ? '▶ Продолжить' : '❚❚ Пауза'}</button>
    <button class="ghost sm" onclick={download} disabled={!merged.length} title="Все собранные записи — роутер и Pi, все уровни — одним файлом">Скачать</button>
  </div>

  <div class="debugbar" class:on>
    <div class="what">
      <span class="dot {on ? 'deg' : ''}"></span>
      <b>Отладка</b>
      {#if forced}
        <span class="hint">включена на роутере флагом <span class="mono">-debug</span> — до его перезапуска</span>
      {:else if on}
        <span>{until ? `включена до ${hm(until)} — выключится сама` : 'включена'}</span>
      {:else}
        <span class="hint">
          каждый шаг: вызовы движков, запросы к Keenetic, команды, сбои DNS{viaCtl ? ', а на Pi — запросы панели, плагины и вывод GP' : ''}
        </span>
      {/if}
    </div>
    {#if !forced}
      <div class="acts">
        <select bind:value={minutes} aria-label="На сколько включить">
          {#each [15, 30, 60, 120, 240] as m (m)}<option value={m}>{m < 60 ? `${m} мин` : `${m / 60} ч`}</option>{/each}
        </select>
        {#if on}
          <button class="ghost sm" onclick={() => setDebug(true)} disabled={switching}>Продлить</button>
          <button class="sm" onclick={() => setDebug(false)} disabled={switching}>Выключить</button>
        {:else}
          <button class="sm" onclick={() => setDebug(true)} disabled={switching || !dbg}>Включить</button>
        {/if}
      </div>
    {/if}
  </div>
  {#if dbgErr}<p class="err-text">{dbgErr}</p>{/if}
  {#if dbg?.errors?.agent}<p class="err-text">роутер: {dbg.errors.agent}</p>{/if}
  {#if dbg?.errors?.plugins}<p class="err-text">плагины: {dbg.errors.plugins}</p>{/if}
  {#if ctlErr}<p class="err-text">журнал Pi: {ctlErr}</p>{/if}

  <!-- a scrollable region must be reachable by keyboard -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="log" class:srcs={viaCtl} bind:this={box} tabindex="0" role="log" aria-label="Журнал">
    {#each shown as e (e.key)}
      <div class="line">
        <span class="muted">{clock(e.ts)}</span>
        <span class="lvl {e.level}">{e.level.toUpperCase()}</span>
        {#if viaCtl}<span class="src {e.src}">{SRC_LABEL[e.src]}</span>{/if}
        <span>{e.msg}{#if e.attrs}<span class="attrs">{e.attrs}</span>{/if}</span>
      </div>
    {:else}
      <div class="muted">Записей пока нет.</div>
    {/each}
  </div>
  <p class="hint">
    В памяти — последние 2000 записей у роутера и столько же на Pi. Отладочные — только в памяти: файл на роутере
    (<span class="mono">/opt/var/log/nuxk-core.log</span>) хранит info и выше. «Скачать» сохраняет всё собранное здесь одним
    файлом.
  </p>
  {#if viaCtl}
    <p class="hint">
      Почему завершился прогон подбора — в <button class="linkbtn" onclick={() => go('results')}>«Результатах»</button>
      (раскройте строку прогона), прошлый вывод GP — в <button class="linkbtn" onclick={() => go('plugins')}>«Плагинах»</button>.
    </p>
  {/if}
</section>

<style>
  .search {
    width: 180px;
  }
  .debugbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 16px;
    padding: 8px 12px;
    margin-bottom: 10px;
    border: 1px solid var(--line);
    border-radius: 10px;
    background: var(--surface-2);
    font-size: 13px;
  }
  .debugbar.on {
    background: var(--degraded-soft);
    border-color: color-mix(in srgb, var(--degraded) 35%, transparent);
  }
  .debugbar .what {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 10px;
    flex: 1 1 320px;
  }
  .debugbar .acts {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .debugbar select {
    width: auto;
  }
  .log {
    font-family: var(--font-mono);
    font-size: 12px;
    background: var(--surface-2);
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 8px 10px;
    height: 480px;
    overflow-y: auto;
    margin-bottom: 8px;
  }
  .line {
    display: grid;
    grid-template-columns: 64px 52px 1fr;
    gap: 8px;
    padding: 1px 0;
    word-break: break-word;
  }
  .srcs .line {
    grid-template-columns: 64px 52px 62px 1fr;
  }
  .lvl.info {
    color: var(--ok);
  }
  .lvl.warn {
    color: var(--degraded);
  }
  .lvl.error {
    color: var(--warn);
  }
  .lvl.debug {
    color: var(--muted);
  }
  .src {
    color: var(--muted);
  }
  .src.router {
    color: var(--accent);
  }
  .attrs {
    color: var(--muted);
    margin-left: 0.6em;
  }
  @media (max-width: 480px) {
    .line,
    .srcs .line {
      grid-template-columns: 56px 1fr;
    }
    .lvl,
    .src {
      display: none;
    }
    .search {
      width: 100%;
    }
  }
</style>
