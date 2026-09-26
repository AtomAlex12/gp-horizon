<script lang="ts">
  // Первичная настройка контроллера на Pi: 1) пароль администратора панели,
  // 2) подключение роутера (адрес + root от Entware — один раз, пароль не
  // хранится: роутер выдаёт контроллеру свой ключ доступа), 3) готово.
  import { api, HttpError } from '../api';
  import { setup, pollNow } from '../status.svelte';
  import { roleLabel } from '../ui';

  const STEPS = [
    { k: 'admin', t: 'Пароль' },
    { k: 'agent', t: 'Роутер' },
    { k: 'done', t: 'Готово' },
  ] as const;
  const at = $derived(STEPS.findIndex((s) => s.k === setup.step));

  let busy = $state(false);
  let err = $state('');

  function message(x: unknown): string {
    if (x instanceof HttpError && x.status === 401) return 'Сессия истекла — обновите страницу и войдите заново';
    return x instanceof Error ? x.message : String(x);
  }

  // --- step 1 ---
  const MIN = 8;
  let pw = $state('');
  let pw2 = $state('');
  const pwProblem = $derived(
    pw.length > 0 && [...pw].length < MIN ? `не короче ${MIN} символов` : pw2 && pw2 !== pw ? 'пароли не совпадают' : '',
  );

  async function saveAdmin(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      await api.setupAdmin(pw);
      pw = pw2 = '';
      const s = await api.setup();
      setup.state = s;
      if (s?.agent) {
        setup.step = null; // the router came from AGENT_URL / AGENT_TOKEN
        pollNow();
      } else {
        setup.step = 'agent';
      }
    } catch (x) {
      err = message(x);
    } finally {
      busy = false;
    }
  }

  // --- step 2 ---
  // a guess from the Pi's own address (x.y.z.10 → x.y.z.1); the person checks it
  function guess(): string {
    const saved = setup.state?.agent_url;
    if (saved) return saved.replace(/^http:\/\//, '');
    const m = /^(\d+\.\d+\.\d+)\.\d+$/.exec(location.hostname);
    return m ? `${m[1]}.1` : '';
  }
  let addr = $state(guess());
  let rootUser = $state('root');
  let rootPw = $state('');

  async function connect(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      setup.agent = await api.setupAgent(addr.trim(), rootUser.trim(), rootPw);
      rootPw = '';
      setup.step = 'done';
    } catch (x) {
      err = message(x);
    } finally {
      busy = false;
    }
  }

  function cancel() {
    setup.step = null;
    setup.reconnect = false;
    err = '';
  }

  function open() {
    setup.step = null;
    setup.reconnect = false;
    setup.agent = null;
    pollNow();
  }

  const info = $derived(setup.agent?.info);
</script>

<div class="card gate-card stack wizard">
  <ol class="steps" aria-label="Шаги настройки">
    {#each STEPS as s, i (s.k)}
      <li class:on={i === at} class:past={i < at} aria-current={i === at ? 'step' : undefined}>
        <span class="n">{i < at ? '✓' : i + 1}</span><span class="t">{s.t}</span>
      </li>
    {/each}
  </ol>

  {#if setup.step === 'admin'}
    <form class="stack" onsubmit={saveAdmin}>
      <div>
        <h2>Пароль для панели</h2>
        <p class="hint">
          Первый запуск контроллера. Задайте пароль, которым будете входить в панель, логин —
          <span class="mono">admin</span>. Пароль хранится на Pi только в виде хеша.
        </p>
      </div>
      <input type="text" value="admin" autocomplete="username" hidden readonly />
      <label class="field">
        Пароль
        <!-- svelte-ignore a11y_autofocus -->
        <input type="password" bind:value={pw} autocomplete="new-password" minlength={MIN} required autofocus />
      </label>
      <label class="field">
        Ещё раз
        <input type="password" bind:value={pw2} autocomplete="new-password" required />
      </label>
      {#if pwProblem}<p class="form-err">{pwProblem}</p>{/if}
      {#if err}<p class="form-err" role="alert">{err}</p>{/if}
      <button type="submit" disabled={busy || !pw || pw !== pw2 || !!pwProblem}>{busy ? 'Сохраняю…' : 'Задать пароль и продолжить'}</button>
    </form>
  {:else if setup.step === 'agent'}
    <form class="stack" onsubmit={connect}>
      <div>
        <h2>{setup.reconnect ? 'Сменить роутер' : 'Подключите роутер'}</h2>
        <p class="hint">
          Контроллер работает с nuxk-core на роутере. Логин и пароль <span class="mono">root</span> от Entware (те же, что
          для SSH) нужны один раз: роутер проверит их и выдаст контроллеру ключ доступа. Сам пароль нигде не сохраняется.
        </p>
      </div>
      <label class="field">
        Адрес роутера
        <input type="text" bind:value={addr} placeholder="192.168.1.1" autocapitalize="none" spellcheck="false" required />
        <span class="hint">IP роутера в домашней сети; порт nuxk-core — 4141 (подставится сам).</span>
      </label>
      <label class="field">
        Логин root
        <input type="text" bind:value={rootUser} autocomplete="off" autocapitalize="none" spellcheck="false" required />
      </label>
      <label class="field">
        Пароль root
        <input type="password" bind:value={rootPw} autocomplete="off" required />
      </label>
      {#if err}<p class="form-err" role="alert">{err}</p>{/if}
      <div class="row">
        <button type="submit" disabled={busy || !addr.trim() || !rootUser.trim() || !rootPw}>{busy ? 'Подключаю…' : 'Подключить'}</button>
        {#if setup.reconnect}<button type="button" class="ghost" onclick={cancel}>Отмена</button>{/if}
      </div>
      <p class="hint">
        nuxk ещё не стоит на роутере? Сначала поставьте его инсталлятором на этом Pi — порт
        <span class="mono">4300</span>.
      </p>
    </form>
  {:else if setup.step === 'done'}
    <div class="stack">
      <div>
        <h2>Роутер подключён</h2>
        <p class="hint">Контроллер начал собирать историю графиков — первые точки появятся через несколько секунд.</p>
      </div>
      <dl class="kv">
        <dt>узел</dt><dd>{roleLabel(info ?? null)}</dd>
        {#if info?.firmware}<dt>KeeneticOS</dt><dd class="mono">{info.firmware}</dd>{/if}
        {#if info?.version}<dt>nuxk-core</dt><dd class="mono">{info.version}</dd>{/if}
        <dt>адрес</dt><dd class="mono">{setup.agent?.url}</dd>
        <dt>связь</dt>
        <dd>{#if setup.agent?.reachable}<span class="chip ok">есть</span>{:else}<span class="chip warn">нет</span>{/if}</dd>
      </dl>
      {#if info?.role === 'stand'}
        <p class="banner deg">Это тестовый стенд на Pi, а не роутер: маршрутизация здесь не работает.</p>
      {/if}
      <button onclick={open}>Открыть панель</button>
    </div>
  {/if}
</div>

<style>
  .wizard {
    max-width: 480px;
  }
  .steps {
    list-style: none;
    margin: 0 0 4px;
    padding: 0 0 12px;
    display: flex;
    gap: 6px;
    border-bottom: 1px solid var(--line);
  }
  .steps li {
    flex: 1;
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--muted);
    min-width: 0;
  }
  .steps .n {
    flex: none;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    display: grid;
    place-items: center;
    font-size: 11.5px;
    font-weight: 600;
    background: var(--surface-2);
    border: 1px solid var(--line);
  }
  .steps .t {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .steps li.on {
    color: var(--ink);
    font-weight: 500;
  }
  .steps li.on .n {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--on-accent);
  }
  .steps li.past .n {
    background: var(--accent-soft);
    border-color: transparent;
    color: var(--accent);
  }
  h2 {
    font-size: 16px;
    margin-bottom: 4px;
  }
  .form-err {
    margin: 0;
    color: var(--warn);
    font-size: 12.5px;
  }
  @media (max-width: 480px) {
    .steps li:not(.on) .t {
      display: none;
    }
  }
</style>
