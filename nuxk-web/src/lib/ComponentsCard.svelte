<script lang="ts">
  // «Компоненты»: nfqws2, WARP, VLESS, SmartDNS — which are on the router and
  // a button for each one that isn't. The button runs the router's own
  // `nuxk warp|vless|dns|nfqws2 --yes` in the background, like an update: it
  // restarts the agent on the way, so a failed request while it runs is that
  // restart, not an error.
  import { api, type Components } from './api';
  import { pollNow } from './status.svelte';
  import { ago } from './ui';

  let c = $state<Components | null>(null);
  let err = $state('');
  let busy = $state('');
  let restarting = $state(false);
  let startedHere = $state(false);

  const run = $derived(c?.run ?? null);
  const going = $derived(run?.state === 'running');
  const runName = $derived(c?.items.find((i) => i.id === run?.task)?.name ?? run?.task ?? '');
  // one reason for all of them (no `nuxk` here, an install going): said once
  const common = $derived.by(() => {
    const why = (c?.items ?? []).filter((i) => !i.installed).map((i) => i.cannot ?? '');
    return why.length > 1 && why[0] && why.every((w) => w === why[0]) ? why[0] : '';
  });
  // the last run: while it goes, when it didn't go well, and for a day after
  const showRun = $derived(!!run && (run.state !== 'done' || Date.now() / 1000 - run.at < 86400));

  async function load() {
    try {
      c = await api.components();
      err = '';
      restarting = false;
      if (startedHere && c.run?.state !== 'running') {
        startedHere = false;
        pollNow(); // the new engine shows up in the menu and on its page
      }
    } catch (e) {
      if (going || startedHere) restarting = true;
      else err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    void load();
    let t: ReturnType<typeof setTimeout>;
    const tick = () => {
      t = setTimeout(
        async () => {
          await load();
          tick();
        },
        going || startedHere ? 2000 : 30_000,
      );
    };
    tick();
    return () => clearTimeout(t);
  });

  async function install(id: string, name: string, what: string) {
    if (!confirm(`Поставить ${name}?\n\n${what}`)) return;
    busy = id;
    err = '';
    try {
      c = await api.installComponent(id);
      startedHere = true;
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = '';
    }
  }

  const STATE: Record<string, { label: string; chip: string }> = {
    running: { label: 'идёт', chip: 'acc' },
    done: { label: 'готово', chip: 'ok' },
    failed: { label: 'не вышло', chip: 'warn' },
    rolled_back: { label: 'не вышло', chip: 'warn' },
    interrupted: { label: 'прервано', chip: 'warn' },
  };

  // the output shows its last lines: where it got to, or where it broke
  function toEnd(el: HTMLElement, _log: string[]) {
    const end = () => {
      el.scrollTop = el.scrollHeight;
    };
    end();
    return { update: end };
  }
</script>

<section class="card">
  <div class="card-head">
    <h2>Компоненты</h2>
    {#if going}<span class="chip acc">идёт установка</span>{/if}
  </div>
  {#if !c}
    <p class="muted">{err ? `Агент не ответил: ${err}` : 'Загрузка…'}</p>
  {:else}
    {#if common && !going}<p class="hint">{common}</p>{/if}
    <div class="items">
      {#each c.items as it (it.id)}
        <div class="item">
          <div class="what">
            <div class="row">
              <b>{it.name}</b>
              {#if it.installed}<span class="chip ok">установлен{it.version ? ` · ${it.version}` : ''}</span>
              {:else}<span class="chip">не установлен</span>{/if}
            </div>
            <p class="hint">{it.about}</p>
            {#if !it.installed && it.cannot && it.cannot !== common}<p class="hint">{it.cannot}</p>{/if}
          </div>
          {#if !it.installed}
            <button
              class="sm"
              onclick={() => install(it.id, it.name, it.confirm)}
              disabled={!it.can_install || !!busy || going}
            >
              {busy === it.id ? 'Запускаю…' : 'Установить'}
            </button>
          {/if}
        </div>
      {/each}
    </div>

    {#if showRun && run}
      {@const st = STATE[run.state] ?? { label: run.state, chip: '' }}
      <div class="run" class:bad={st.chip === 'warn'}>
        <div class="row">
          <b>Установка: {runName}</b>
          <span class="chip {st.chip}">{st.label}</span>
          <span class="spacer"></span>
          <span class="hint">{ago(run.at)}</span>
        </div>
        {#if going}
          <p class="hint">{restarting ? 'Агент перезапускается — панель вернётся через несколько секунд…' : run.message}</p>
        {:else if run.message}
          <p class="hint">{run.message}</p>
        {/if}
        {#if run.log.length}
          <details open={st.chip === 'warn'}>
            <summary class="hint">вывод установки</summary>
            <pre class="log mono" use:toEnd={run.log}>{run.log.join('\n')}</pre>
          </details>
        {/if}
      </div>
    {/if}
    {#if err}<p class="err-text">{err}</p>{/if}
    <p class="hint top">
      Кнопка запускает на роутере то же, что команда <span class="mono">nuxk warp</span>,
      <span class="mono">nuxk vless</span>, <span class="mono">nuxk dns</span> по SSH: файлы этой версии nuxk, сверенные с
      подписью релиза. Что именно изменится на роутере, панель скажет перед установкой.
    </p>
  {/if}
</section>

<style>
  .items {
    display: flex;
    flex-direction: column;
  }
  .item {
    display: flex;
    gap: 12px;
    align-items: center;
    padding: 10px 0;
    border-bottom: 1px solid var(--line);
  }
  .item:last-child {
    border-bottom: 0;
  }
  .what {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .run {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid var(--line);
    border-radius: 10px;
  }
  .run.bad {
    border-color: color-mix(in srgb, var(--warn) 45%, transparent);
  }
  .log {
    margin: 6px 0 0;
    max-height: 260px;
    overflow: auto;
    font-size: 12px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    color: var(--muted);
  }
  .top {
    margin-top: 12px;
  }
  .muted {
    color: var(--muted);
  }
</style>
