<script lang="ts">
  // «Плагины»: what runs next to the controller in its container, each with
  // only the rights it declares. Install / update / roll back / switch off.
  import { node } from '../status.svelte';
  import { plugins, PHASE_LABEL, type PluginInfo, type PluginOp, type PluginsState, type Release } from '../gp';
  import { ago } from '../ui';

  let st = $state<PluginsState | null>(null);
  let err = $state('');
  let busy = $state('');
  let releases = $state<Record<string, Release[] | string>>({});
  let pick = $state<Record<string, string>>({});
  let logOpen = $state<Record<string, 'run' | 'install' | ''>>({});
  let logText = $state<Record<string, string>>({});

  async function load() {
    try {
      st = await plugins.list();
      err = '';
      for (const p of st.plugins) if (logOpen[p.name]) void loadLog(p.name);
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    if (node.via !== 'controller') return;
    void load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  });

  async function act(p: PluginInfo, op: PluginOp, version?: string) {
    busy = p.name + op;
    try {
      await plugins.op(p.name, op, version);
      if (op === 'install') logOpen[p.name] = 'install';
      await load();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = '';
    }
  }

  async function checkReleases(p: PluginInfo) {
    try {
      const list = await plugins.releases(p.name);
      releases[p.name] = list;
      pick[p.name] = (list.find((r) => !r.prerelease) ?? list[0])?.tag ?? p.default_version;
    } catch (e) {
      releases[p.name] = e instanceof Error ? e.message : String(e);
    }
  }

  async function loadLog(name: string) {
    const which = logOpen[name];
    if (!which) return;
    try {
      logText[name] = (await plugins.log(name, which)).log || '(пусто)';
    } catch (e) {
      logText[name] = e instanceof Error ? e.message : String(e);
    }
  }
  function toggleLog(name: string, which: 'run' | 'install') {
    logOpen[name] = logOpen[name] === which ? '' : which;
    void loadLog(name);
  }

  /** v0.4.10 > v0.4.9; a pre-release sorts before its release. */
  function newer(a: string, b: string): boolean {
    const parse = (v: string) => {
      const [core, pre] = v.replace(/^v/, '').split('-', 2);
      return { n: core.split('.').map((x) => parseInt(x, 10) || 0), pre: pre ?? '' };
    };
    const x = parse(a),
      y = parse(b);
    for (let i = 0; i < Math.max(x.n.length, y.n.length); i++) {
      if ((x.n[i] ?? 0) !== (y.n[i] ?? 0)) return (x.n[i] ?? 0) > (y.n[i] ?? 0);
    }
    if (x.pre === y.pre) return false;
    return x.pre === '' || (y.pre !== '' && x.pre > y.pre);
  }
  const latest = (p: PluginInfo) => {
    const r = releases[p.name];
    return Array.isArray(r) ? r.find((x) => !x.prerelease) : undefined;
  };

  const CAP_HINT: Record<string, string> = {
    NET_ADMIN: 'правила сети внутри контейнера',
    NET_RAW: 'сырые сокеты',
    SETUID: 'сменить пользователя',
    SETGID: 'сменить группу',
    KILL: 'останавливать свои процессы',
    NET_BIND_SERVICE: 'порты ниже 1024',
  };
  const phaseChip = (ph: string) => (ph === 'running' ? 'ok' : ph === 'failed' ? 'warn' : ph === 'absent' || ph === 'stopped' ? '' : 'deg');
</script>

{#if node.via !== 'controller'}
  <section class="card empty">
    <h2>Плагины — в контроллере на Pi</h2>
    <p>На роутере плагинов нет: они работают рядом с контроллером на Raspberry Pi.</p>
  </section>
{:else if !st}
  <p class="muted">{err || 'Загрузка…'}</p>
{:else if !st.host}
  <section class="card empty">
    <h2>Хост плагинов не запущен</h2>
    <p>{st.reason}</p>
    <p class="hint">
      Плагины работают, когда контроллер стартует командой <span class="mono">supervise</span> — так его запускает образ
      Pi из <span class="mono">deploy/pi</span>.
    </p>
  </section>
{:else}
  {#if err}<div class="banner warn">{err}</div>{/if}
  <p class="hint intro">
    Плагин — отдельный процесс в контейнере контроллера. Он получает только права из своего описания и не может читать
    файлы контроллера (пароль администратора, ключ к роутеру). Код плагина скачивается при установке с его собственных
    релизов.
  </p>
  {#each st.plugins as p (p.name)}
    {@const l = latest(p)}
    <section class="card">
      <div class="card-head">
        <h2>{p.title}</h2>
        <span class="chip {phaseChip(p.phase)}">{PHASE_LABEL[p.phase]}</span>
        {#if p.version}<span class="chip mono">{p.version}</span>{/if}
        <span class="spacer"></span>
        {#if p.homepage}<a class="hint" href={p.homepage} target="_blank" rel="noopener">исходники</a>{/if}
      </div>
      <p class="hint">{p.description}</p>

      {#if p.notice}<div class="banner deg note">{p.notice}</div>{/if}
      {#if p.last_error && p.phase !== 'running'}<div class="err-text note">{p.last_error}</div>{/if}

      <dl class="kv">
        <dt>права</dt>
        <dd class="caps">
          {#each p.caps as c (c)}<span class="chip mono" title={CAP_HINT[c] ?? ''}>{c}</span>{:else}нет{/each}
        </dd>
        {#if p.since}<dt>{PHASE_LABEL[p.phase]}</dt><dd>{ago(p.since)}</dd>{/if}
        {#if p.restarts}<dt>перезапусков</dt><dd>{p.restarts}</dd>{/if}
        {#if p.previous}<dt>предыдущая версия</dt><dd class="mono">{p.previous}</dd>{/if}
        <dt>адрес</dt><dd class="mono">{p.listen} (только внутри контейнера)</dd>
      </dl>

      <div class="row actions">
        {#if p.phase === 'absent'}
          <button onclick={() => act(p, 'install', pick[p.name] || p.default_version)} disabled={!!busy}>
            Установить {pick[p.name] || p.default_version}
          </button>
        {:else if p.phase !== 'installing'}
          {#if l && p.version && newer(l.tag, p.version)}
            <button onclick={() => act(p, 'install', l.tag)} disabled={!!busy}>Обновить до {l.tag}</button>
          {/if}
          {#if p.enabled}
            <button class="ghost" onclick={() => act(p, 'restart')} disabled={!!busy}>Перезапустить</button>
            <button class="ghost" onclick={() => act(p, 'disable')} disabled={!!busy}>Выключить</button>
          {:else}
            <button onclick={() => act(p, 'enable')} disabled={!!busy}>Включить</button>
          {/if}
          {#if p.previous}
            <button class="ghost" onclick={() => act(p, 'rollback')} disabled={!!busy}>Вернуть {p.previous}</button>
          {/if}
        {/if}
        {#if p.releases}
          <button class="ghost" onclick={() => checkReleases(p)}>Версии</button>
        {/if}
      </div>

      {#if typeof releases[p.name] === 'string'}
        <p class="err-text">{releases[p.name]}</p>
      {:else if Array.isArray(releases[p.name])}
        {@const list = releases[p.name] as Release[]}
        <div class="row">
          <select bind:value={pick[p.name]} aria-label="Версия">
            {#each list as r (r.tag)}<option value={r.tag}>{r.tag}{r.prerelease ? ' (pre-release)' : ''}{r.tag === p.version ? ' — стоит' : ''}</option>{/each}
          </select>
          {#if p.phase !== 'absent' && p.phase !== 'installing' && pick[p.name] && pick[p.name] !== p.version}
            <button class="ghost" onclick={() => act(p, 'install', pick[p.name])} disabled={!!busy}>Поставить {pick[p.name]}</button>
          {/if}
          {#if l && p.version && !newer(l.tag, p.version)}<span class="hint">последняя версия уже стоит</span>{/if}
        </div>
      {/if}

      <div class="row logs">
        <button class="ghost sm" class:on={logOpen[p.name] === 'run'} onclick={() => toggleLog(p.name, 'run')}>Журнал работы</button>
        <button class="ghost sm" class:on={logOpen[p.name] === 'install'} onclick={() => toggleLog(p.name, 'install')}>Журнал установки</button>
      </div>
      {#if logOpen[p.name]}<pre class="log">{logText[p.name] ?? '…'}</pre>{/if}
    </section>
  {:else}
    <section class="card empty"><p>В этой сборке контроллера нет рецептов плагинов.</p></section>
  {/each}
{/if}

<style>
  h2 {
    font-size: 15px;
  }
  .intro {
    max-width: 760px;
  }
  .note {
    margin: 10px 0;
  }
  .kv {
    margin-top: 12px;
  }
  .caps {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
    justify-content: flex-end;
  }
  .actions,
  .logs {
    margin-top: 12px;
  }
  .logs button.on {
    background: var(--accent-soft);
    color: var(--accent);
  }
  .log {
    margin: 8px 0 0;
    max-height: 320px;
    overflow: auto;
    background: var(--surface-2);
    border-radius: 8px;
    padding: 10px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  select {
    max-width: 280px;
  }
  .empty h2 {
    color: var(--ink);
    margin-bottom: 8px;
  }
</style>
