<script lang="ts">
  // «DNS»: protected DNS through the tunnels, and the check whether answers
  // are substituted. Devices keep asking the router; with protection on, the
  // router's DNS proxy asks nuxk, and nuxk asks the resolvers over DoH through
  // VLESS or WARP — the provider sees nothing to substitute or block. What
  // answers there is nuxk's own forwarder or SmartDNS (beta); the overview
  // charts the last hour of answers either way.
  import { api, HttpError, type DNSCheck, type DNSQuery, type DNSStats, type DNSStatus, type DNSSettings } from '../api';
  import Chart from '../Chart.svelte';
  import { ago, fmtBytes, fmtNum, plural } from '../ui';

  const TABS = [
    ['overview', 'Обзор'],
    ['log', 'Журнал запросов'],
    ['settings', 'Настройки'],
    ['check', 'Проверка подмены'],
  ] as const;
  type Tab = (typeof TABS)[number][0];
  let tab = $state<Tab>('overview');

  let s = $state<DNSStatus | null>(null);
  let err = $state('');
  let busy = $state<'' | 'toggle' | 'settings' | 'cache' | 'flush' | 'check' | 'engine'>('');
  let stats = $state<DNSStats | null>(null);
  let log = $state<DNSQuery[]>([]);
  let logQ = $state('');
  let logSrc = $state('');
  let check = $state<DNSCheck | null>(null);
  let extra = $state('');

  async function load() {
    try {
      s = await api.dns();
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    void load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  });

  // the overview's numbers and charts: every 10 s while it's open
  let statsErr = $state('');
  async function loadStats() {
    try {
      stats = await api.dnsStats();
      statsErr = '';
    } catch (e) {
      stats = null;
      // an agent older than 0.5.0-beta.4 has no /dns/stats
      statsErr = e instanceof HttpError && e.status === 404 ? 'old' : e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    if (tab !== 'overview') return;
    void loadStats();
    const t = setInterval(loadStats, 10_000);
    return () => clearInterval(t);
  });

  async function loadLog() {
    try {
      log = (await api.dnsLog(logQ.trim())).items;
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    if (tab !== 'log') return;
    void logQ;
    void loadLog();
    const t = setInterval(loadLog, 5_000);
    return () => clearInterval(t);
  });
  const shownLog = $derived(logSrc ? log.filter((q) => q.source === logSrc) : log);

  const smart = $derived(s?.settings.engine === 'smartdns');
  const sd = $derived(s?.smartdns);

  function chooseEngine(e: 'nuxk' | 'smartdns') {
    if (!s || s.settings.engine === e) return;
    if (s.settings.enabled) {
      const ok = confirm(
        (e === 'smartdns'
          ? 'Отвечать роутеру будет SmartDNS (бета): вопросы — через WARP, а если WARP не ответил — напрямую.'
          : 'Отвечать роутеру снова будет встроенный DNS nuxk.') +
          '\n\nАдрес в DNS-прокси Keenetic тот же — настройки роутера не меняются. На секунду-две DNS роутера ' +
          'обойдётся без nuxk. Если новый не ответит — вернётся прежний.',
      );
      if (!ok) return;
    }
    void change('engine', { engine: e });
  }

  async function change(what: typeof busy, patch: Partial<DNSSettings>) {
    busy = what;
    err = '';
    try {
      s = await api.setDns(patch);
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
      await load();
    } finally {
      busy = '';
    }
  }

  function toggle() {
    if (!s) return;
    if (s.settings.enabled) {
      void change('toggle', { enabled: false });
      return;
    }
    const ok = confirm(
      'Это изменит настройки DNS роутера: его DNS-прокси получит ещё один сервер — nuxk (' +
        s.listen +
        ').\n\n' +
        'Устройства спрашивают роутер, как и раньше; маршрутизация по доменам не меняется. ' +
        'Настройка не сохраняется в конфигурацию роутера: после перезагрузки nuxk добавит её сам, при удалении nuxk — уберёт.\n\n' +
        'Сначала nuxk проверит, что серверы DoH отвечают, а после включения — что роутер по-прежнему отвечает на DNS. Если нет — сразу вернёт всё как было.',
    );
    if (ok) void change('toggle', { enabled: true });
  }

  // SmartDNS goes through WARP or straight: VLESS is too far away for DNS
  const VIA_SMART: { id: DNSSettings['via']; label: string; hint: string }[] = [
    { id: 'warp', label: 'Через WARP', hint: 'серверы Cloudflare рядом, подменить нельзя; WARP не ответил — напрямую' },
    { id: 'direct', label: 'Напрямую', hint: 'без туннеля; DoH всё равно зашифрован, но его могут заблокировать' },
  ];
  const viaSmart = $derived(s?.settings.via === 'direct' ? 'direct' : 'warp');
  const VIA: { id: DNSSettings['via']; label: string; hint: string }[] = [
    { id: 'auto', label: 'Автоматически', hint: 'VLESS, если он работает, потом WARP, потом напрямую' },
    { id: 'vless', label: 'Через VLESS', hint: 'пока VLESS недоступен — напрямую' },
    { id: 'warp', label: 'Через WARP', hint: 'пока WARP недоступен — напрямую' },
    { id: 'direct', label: 'Напрямую', hint: 'без туннеля; DoH всё равно зашифрован, но его могут заблокировать' },
  ];
  const PATH: Record<string, string> = { vless: 'VLESS', warp: 'WARP', direct: 'Напрямую' };

  function toggleResolver(id: string) {
    if (!s) return;
    const cur = s.settings.resolvers;
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id];
    if (next.length) void change('settings', { resolvers: next });
  }

  async function flush() {
    busy = 'flush';
    err = '';
    try {
      s = await api.flushDnsCache();
      void loadStats();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = '';
    }
  }

  // how many questions the cache answered, of those that came to nuxk
  const hitRate = $derived.by(() => {
    const c = s?.cache;
    if (!c || c.hits + c.misses === 0) return '';
    const all = c.hits + c.misses;
    return `${Math.round((c.hits / all) * 100)} % · ${c.hits} из ${all}`;
  });

  async function runCheck() {
    busy = 'check';
    err = '';
    try {
      const doms = extra
        .split(/[\s,]+/)
        .map((d) => d.trim().toLowerCase())
        .filter(Boolean)
        .slice(0, 20);
      check = await api.checkDns(doms);
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = '';
    }
  }

  const VERDICT: Record<string, { label: string; chip: string }> = {
    ok: { label: 'честно', chip: 'ok' },
    spoofed: { label: 'подмена', chip: 'warn' },
    differs: { label: 'другие адреса', chip: 'deg' },
    error: { label: 'нет ответа', chip: '' },
  };
  // the overview
  const ts = $derived((stats?.minutes ?? []).map((m) => m.t * 1000));
  const col = (k: 'cache' | 'upstream' | 'stale' | 'failed' | 'avg_ms' | 'p95_ms') =>
    (stats?.minutes ?? []).map((m) => m[k] ?? 0);
  const answered = $derived(stats ? stats.queries : 0);
  const pct = (n: number, all: number) => (all ? `${Math.round((n / all) * 1000) / 10} %`.replace('.', ',') : '—');
  const fmtMs = (v: number) => `${Math.round(v)}`;
  const topMax = $derived(Math.max(1, ...(stats?.top ?? []).map((c) => c.count)));
  const typeMax = $derived(Math.max(1, ...(stats?.types ?? []).map((c) => c.count)));
  const SRC: Record<string, { label: string; cls: string }> = {
    cache: { label: 'кэш', cls: 'cache' },
    upstream: { label: 'сервер', cls: 'up' },
    stale: { label: 'устаревший', cls: 'stale' },
    failed: { label: 'нет ответа', cls: 'fail' },
  };
  const LOG_SRC = [
    ['', 'все'],
    ['cache', 'кэш'],
    ['upstream', 'сервер'],
    ['stale', 'устаревшие'],
    ['failed', 'нет ответа'],
  ];
  const clock = (t: number) => new Date(t * 1000).toTimeString().slice(0, 8);
  const answerText = (a?: string) => (a === 'empty' ? 'пусто' : a === 'nxdomain' ? 'нет такого домена' : a || '—');

  const list = (a: string[]) => (a.length ? a.slice(0, 3).join(', ') + (a.length > 3 ? ' …' : '') : '—');
</script>

<div class="seg tabs" role="tablist" aria-label="Разделы DNS">
  {#each TABS as [id, label] (id)}
    <button role="tab" aria-selected={tab === id} class:on={tab === id} onclick={() => (tab = id)}>{label}</button>
  {/each}
</div>

{#if tab === 'overview'}
<div class="grid g2">
  <section class="card">
    <div class="card-head">
      <h2>Защищённый DNS</h2>
      {#if s}
        {#if smart}<span class="chip deg">SmartDNS · бета</span>{:else}<span class="chip">встроенный DNS nuxk</span>{/if}
      {/if}
      {#if s}
        {#if s.settings.enabled && s.attached}<span class="chip ok">включён</span>
        {:else if s.settings.enabled && s.suspended}<span class="chip warn">приостановлен</span>
        {:else if s.settings.enabled}<span class="chip deg">включён, роутер пока не спрашивает</span>
        {:else}<span class="chip">выключен</span>{/if}
      {/if}
      <span class="spacer"></span>
      {#if s?.can_attach}
        <button class={s.settings.enabled ? 'ghost sm' : 'sm'} onclick={toggle} disabled={!!busy}>
          {busy === 'toggle' ? 'Проверяю…' : s.settings.enabled ? 'Выключить' : 'Включить'}
        </button>
      {/if}
    </div>
    <p class="hint">
      Провайдер подменяет ответы обычного DNS на заглушки — даже к 8.8.8.8. С защитой роутер спрашивает nuxk, а nuxk —
      серверы DoH через туннель: провайдер не видит ни вопроса, ни ответа. Устройства ничего не настраивают.
    </p>
    {#if s}
      <div class="chain top" aria-label="Путь вопроса">
        <span class="node">устройства</span><span class="arr">→</span>
        <span class="node">DNS-прокси Keenetic<small>маршруты по доменам</small></span><span class="arr">→</span>
        <span class="node me">{smart ? 'SmartDNS' : 'nuxk'}<small class="mono">{s.listen}</small></span><span class="arr">→</span>
        <span class="node">
          {#if smart}{sd?.iface ? 'WARP' : 'напрямую'}{:else}{VIA.find((v) => v.id === s?.settings.via)?.label ?? ''}{/if}
          <small>DoH{smart && sd?.fallback ? ', запасной путь напрямую' : ''}</small>
        </span>
      </div>
    {/if}
    {#if !s}
      <p class="muted">{err || 'Загрузка…'}</p>
    {:else}
      {#if !s.can_attach && s.cannot}<p class="banner deg note">{s.cannot}</p>{/if}
      {#if s.error}<p class="err-text">{s.error}</p>{/if}
      <dl class="kv top">
        <dt>роутер спрашивает nuxk</dt>
        <dd>
          {#if !s.settings.enabled}—
          {:else if s.attached}да{s.consulted ? '' : ' (контрольный вопрос не дошёл — возможно, роутер пока опрашивает другие серверы)'}
          {:else}нет{/if}
        </dd>
        {#if !smart}
          <dt>вопросов</dt><dd>{s.queries}{s.failed ? ` · без ответа ${s.failed}` : ''}</dd>
          <dt>последний</dt><dd>{s.last_query ? ago(s.last_query) : 'ещё не было'}</dd>
        {/if}
        {#if s.last_path && !smart}<dt>ответил</dt><dd>{s.resolver || '—'} · {PATH[s.last_path] ?? s.last_path}</dd>{/if}
        <dt>адрес nuxk</dt><dd class="mono">{s.listen}</dd>
        {#if smart && sd}
          <dt>SmartDNS</dt>
          <dd>{sd.running ? 'отвечает' : s.settings.enabled ? 'не отвечает' : 'остановлен'}{sd.version ? ` · ${sd.version}` : ''}</dd>
          {#if sd.error}<dt>ошибка</dt><dd class="err-text">{sd.error}</dd>{/if}
        {/if}
      </dl>
    {/if}
  </section>

  <section class="card">
    <div class="card-head"><h2>Последний час</h2><span class="spacer"></span>{#if stats?.since}<span class="hint">считаем с {clock(stats.since)}</span>{/if}</div>
    {#if !stats && statsErr === 'old'}
      <p class="hint">Графики DNS появятся, когда агент на роутере обновится до 0.5.0-beta.4 или новее («Система» → обновление).</p>
    {:else if !stats}
      <p class="muted">{statsErr || 'Загрузка…'}</p>
    {:else}
      <div class="kpis">
        <div class="kpi"><span class="label">Вопросов</span><span class="value">{answered}</span><span class="sub">≈ {Math.round(answered / 60)} в минуту</span></div>
        <div class="kpi"><span class="label">Из кэша</span><span class="value">{pct(stats.cache, answered)}</span><span class="sub">без обращения к серверу</span></div>
        <div class="kpi"><span class="label">Ответ сервера</span><span class="value">{stats.avg_ms ? `${Math.round(stats.avg_ms)} мс` : '—'}</span><span class="sub">в среднем</span></div>
        {#if stats.stale_known}
          <div class="kpi"><span class="label">Выручил кэш</span><span class="value">{stats.stale}</span><span class="sub">устаревший ответ при сбое</span></div>
        {/if}
        <div class="kpi"><span class="label">Без ответа</span><span class="value">{stats.failed}</span><span class="sub">{pct(stats.failed, answered)}</span></div>
      </div>
      {#if stats.note}<p class="hint top">{stats.note}</p>{/if}
      {#if !answered}
        <p class="hint top">Вопросов за этот час ещё не было{s?.settings.enabled ? '' : ' — защищённый DNS выключен'}.</p>
      {/if}
    {/if}
  </section>
</div>

{#if stats}
  <div class="grid g2">
    <section class="card">
      <div class="card-head"><h2>Вопросы в минуту</h2></div>
      <Chart
        label="Вопросы к DNS в минуту за последний час"
        {ts}
        span={60 * 60_000}
        fmt={fmtNum}
        min={5}
        series={[
          { label: 'из кэша', color: 'var(--s1)', data: col('cache'), fill: true },
          { label: 'от сервера', color: 'var(--s2)', data: col('upstream') },
          ...(stats.stale_known ? [{ label: 'устаревшие при сбое', color: 'var(--s3)', data: col('stale') }] : []),
        ]}
      />
    </section>
    <section class="card">
      <div class="card-head"><h2>Время ответа сервера, мс</h2></div>
      <Chart
        label="Время ответа серверов DoH за последний час"
        {ts}
        span={60 * 60_000}
        fmt={fmtMs}
        min={50}
        series={[
          { label: 'в среднем', color: 'var(--s2)', data: col('avg_ms'), fill: true },
          { label: '95 % быстрее', color: 'var(--s3)', data: col('p95_ms') },
        ]}
      />
      <p class="hint">Только вопросы, ушедшие на сервер; ответ из кэша — меньше миллисекунды.</p>
    </section>
  </div>
  <div class="grid g2">
    <section class="card">
      <div class="card-head"><h2>Частые домены</h2></div>
      {#if stats.top.length}
        <ul class="toplist">
          {#each stats.top as c (c.name)}
            <li><span class="mono">{c.name}</span><span class="num muted">{c.count}</span><div class="bar"><div class="bar-fill" style="width:{(c.count / topMax) * 100}%"></div></div></li>
          {/each}
        </ul>
      {:else}<p class="hint">Появятся с первыми вопросами.</p>{/if}
    </section>
    <section class="card">
      <div class="card-head"><h2>Типы вопросов</h2></div>
      {#if stats.types.length}
        <ul class="toplist">
          {#each stats.types as c (c.name)}
            <li><span class="mono">{c.name}</span><span class="num muted">{c.count}</span><div class="bar"><div class="bar-fill" style="width:{(c.count / typeMax) * 100}%"></div></div></li>
          {/each}
        </ul>
      {:else}<p class="hint">Появятся с первыми вопросами.</p>{/if}
      <p class="hint top">Устройства по отдельности не видны: nuxk спрашивает только роутер.</p>
    </section>
  </div>
{/if}
{/if}

{#if tab === 'log'}
  <section class="card">
    <div class="card-head">
      <h2>Журнал запросов</h2><span class="chip">последние {shownLog.length}</span>
      <span class="spacer"></span>
      <div class="seg" aria-label="Откуда ответ">
        {#each smart ? LOG_SRC.filter(([id]) => id !== 'stale' && id !== 'failed') : LOG_SRC as [id, label] (id)}
          <button class:on={logSrc === id} onclick={() => (logSrc = id)}>{label}</button>
        {/each}
      </div>
    </div>
    <input type="text" bind:value={logQ} placeholder="Домен или его часть, например youtube" aria-label="Фильтр по домену" />
    {#if shownLog.length}
      <div class="tbl-wrap top">
        <table>
          <thead><tr><th>время</th><th>домен</th><th>тип</th><th>ответ</th><th>откуда</th><th class="r">мс</th></tr></thead>
          <tbody>
            {#each shownLog as q, i (i)}
              <tr>
                <td class="mono muted">{clock(q.at)}</td>
                <td class="mono dom">{q.domain}</td>
                <td>{q.type}</td>
                <td class="mono small">{answerText(q.answer)}</td>
                <td><span class="src {SRC[q.source]?.cls}">{SRC[q.source]?.label ?? q.source}</span></td>
                <td class="r num">{q.ms}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <p class="empty">{log.length ? 'Ничего не нашлось' : 'Вопросов пока не было — журнал заполняется, когда роутер спрашивает nuxk.'}</p>
    {/if}
    <p class="hint top">
      Хранятся последние 200 вопросов, в памяти роутера.{smart ? ' SmartDNS не говорит, откуда ответ: «кэш» — ответ быстрее 1 мс.' : ''}
    </p>
  </section>
{/if}

{#if tab === 'settings'}
<div class="grid g2">
  <section class="card">
    <div class="card-head"><h2>Чем отвечать</h2></div>
    {#if s}
      <div class="via" role="radiogroup" aria-label="Чем отвечать">
        <label class:on={!smart}>
          <input type="radio" name="engine" checked={!smart} disabled={!!busy} onchange={() => chooseEngine('nuxk')} />
          <span><b>Встроенный DNS nuxk</b><small>DoH через VLESS, WARP или напрямую; свой кэш</small></span>
        </label>
        <label class:on={smart} class:off={!sd?.installed}>
          <input type="radio" name="engine" checked={smart} disabled={!!busy || !sd?.installed} onchange={() => chooseEngine('smartdns')} />
          <span>
            <b>SmartDNS <span class="chip deg">бета</span></b>
            <small>
              {#if !sd}не предлагается: нужен роутер Keenetic с агентом 0.5.0-beta.4 или новее
              {:else if !sd.installed}не установлен — на роутере по SSH: <code>nuxk dns</code>
              {:else}через WARP, при сбое — напрямую; кэш, предзагрузка частых имён{sd.version ? ` · ${sd.version}` : ''}{/if}
            </small>
          </span>
        </label>
      </div>
      {#if busy === 'engine'}<p class="hint top">Переключаю и проверяю, что новый отвечает…</p>{/if}
    {/if}
  </section>

  <section class="card">
    <div class="card-head"><h2>Как идут вопросы</h2></div>
    {#if s && smart}
      <div class="via" role="radiogroup" aria-label="Путь">
        {#each VIA_SMART as v (v.id)}
          <label class:on={viaSmart === v.id}>
            <input type="radio" name="via" checked={viaSmart === v.id} disabled={!!busy} onchange={() => change('settings', { via: v.id })} />
            <span><b>{v.label}</b><small>{v.hint}</small></span>
          </label>
        {/each}
      </div>
      <p class="hint top">VLESS для DNS не используется: до своего сервера далеко, ответы были бы медленными.</p>
    {:else if s}
      <div class="via" role="radiogroup" aria-label="Путь">
        {#each VIA as v (v.id)}
          <label class:on={s.settings.via === v.id}>
            <input
              type="radio"
              name="via"
              checked={s.settings.via === v.id}
              disabled={!!busy}
              onchange={() => change('settings', { via: v.id })}
            />
            <span><b>{v.label}</b><small>{v.hint}</small></span>
          </label>
        {/each}
      </div>
      <div class="tbl-wrap top">
        <table>
          <thead><tr><th>путь</th><th>сейчас</th><th>ответов</th><th>время</th><th></th></tr></thead>
          <tbody>
            {#each s.paths as p (p.name)}
              <tr>
                <td><b>{PATH[p.name] ?? p.name}</b>{#if p.iface}<span class="muted mono"> {p.iface}</span>{/if}</td>
                <td>{#if p.up}<span class="chip ok">доступен</span>{:else}<span class="chip">нет</span>{/if}</td>
                <td class="mono">{p.ok}{p.failed ? ` / ✗${p.failed}` : ''}</td>
                <td class="mono">{p.rtt_ms ? `${Math.round(p.rtt_ms)} мс` : '—'}</td>
                <td class="err-cell">{p.last_error ?? ''}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if s}
      <p class="hint top">{smart ? 'Серверы DoH (SmartDNS спрашивает все и берёт первый ответ):' : 'Серверы DoH (по порядку; при сбое — следующий):'}</p>
      <div class="row">
        {#each s.catalog as r (r.id)}
          <label class="res">
            <input type="checkbox" checked={s.settings.resolvers.includes(r.id)} disabled={!!busy} onchange={() => toggleResolver(r.id)} />
            {r.name}
          </label>
        {/each}
      </div>
    {/if}
  </section>
  <section class="card">
    {#if s}
      <div class="row">
        <b>Кэш ответов</b>
        {#if s.settings.cache}<span class="chip ok">включён</span>{:else}<span class="chip">выключен</span>{/if}
        <span class="spacer"></span>
        <label class="res">
          <input
            type="checkbox"
            checked={s.settings.cache}
            disabled={!!busy}
            onchange={() => s && change('cache', { cache: !s.settings.cache })}
          />
          запоминать
        </label>
        <button class="ghost sm" onclick={flush} disabled={!!busy || (!smart && !s.cache.entries) || (smart && !s.settings.enabled)}>
          {busy === 'flush' ? 'Очищаю…' : 'Очистить'}
        </button>
      </div>
      {#if smart}
        <p class="hint top-s">
          SmartDNS хранит ответы в памяти роутера (до 4096), часто нужные имена обновляет заранее, а если серверы не ответили —
          отдаёт последний известный адрес (не старше суток). «Очистить» перезапускает SmartDNS.
        </p>
      {:else}
        <p class="hint top-s">
          nuxk хранит ответ столько, сколько разрешил сервер, а часто нужные адреса обновляет заранее. Если туннели и DoH не
          ответили за 2 секунды — отдаёт последний известный адрес (не старше суток), и открытые раньше сайты продолжают
          работать. Проверка подмены кэш не использует.
        </p>
      {/if}
      {#if s.settings.cache && !smart}
        <dl class="kv top-s">
          <dt>в кэше</dt><dd>{plural(s.cache.entries, 'ответ', 'ответа', 'ответов')} · {fmtBytes(s.cache.bytes)}</dd>
          <dt>из кэша</dt><dd>{hitRate || 'пока не было'}</dd>
          <dt>обновлено заранее</dt><dd>{s.cache.refreshed}</dd>
          <dt>выручил при сбое</dt><dd>{s.cache.stale}</dd>
        </dl>
      {/if}
    {/if}
  </section>
</div>
{/if}

{#if tab === 'check'}
<section class="card">
  <div class="card-head">
    <h2>Проверка подмены</h2>
    {#if check}
      <span class="chip {check.plain_spoofed ? 'warn' : 'ok'}"
        >провайдер: {check.plain_spoofed ? `подменяет (${check.plain_spoofed} из ${check.items.length})` : 'подмены не видно'}</span
      >
      <span class="chip {check.router_spoofed ? 'warn' : 'ok'}"
        >роутер: {check.router_spoofed ? `отдаёт подмену (${check.router_spoofed})` : 'честно'}</span
      >
    {/if}
    <span class="spacer"></span>
    <button class="sm" onclick={runCheck} disabled={!!busy}>{busy === 'check' ? 'Проверяю…' : 'Проверить'}</button>
  </div>
  <p class="hint">
    Для заблокированных сайтов-«канареек» и доменов из ваших списков: что отвечает роутер (это получают устройства), что
    отвечает 8.8.8.8, если спросить его обычным DNS через провайдера, и что — через туннель (эталон). «Подмена» — только
    при явных признаках: «сайта нет», частный адрес или один адрес у разных сайтов.
  </p>
  <input type="text" class="top" bind:value={extra} placeholder="Свои домены через пробел (необязательно), например rutracker.org" />
  {#if check}
    <div class="tbl-wrap top">
      <table>
        <thead><tr><th>домен</th><th>роутер</th><th>8.8.8.8 обычным DNS</th><th>через туннель</th></tr></thead>
        <tbody>
          {#each check.items as it (it.domain)}
            <tr>
              <td><b>{it.domain}</b></td>
              <td>
                <span class="chip {VERDICT[it.router_verdict]?.chip}">{VERDICT[it.router_verdict]?.label}</span>
                <span class="mono muted small">{list(it.router)}</span>
                {#if it.router_note}<small class="note">{it.router_note}</small>{/if}
              </td>
              <td>
                <span class="chip {VERDICT[it.plain_verdict]?.chip}">{VERDICT[it.plain_verdict]?.label}</span>
                <span class="mono muted small">{list(it.plain)}</span>
                {#if it.plain_note}<small class="note">{it.plain_note}</small>{/if}
              </td>
              <td class="mono small">{list(it.truth)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <p class="hint top">
      Эталон шёл {PATH[check.path] ? (check.path === 'direct' ? 'напрямую по DoH' : `через ${PATH[check.path]}`) : '—'} ·
      {ago(check.at)}. Устройства со своим DNS («Частный DNS» на Android, свой VPN) спрашивают мимо роутера — на них эта
      защита не действует.
    </p>
  {/if}
</section>
{/if}
{#if err}<p class="err-text">{err}</p>{/if}

<style>
  .top {
    margin-top: 12px;
  }
  .top-s {
    margin-top: 6px;
  }
  .note {
    display: block;
    color: var(--muted);
    margin-top: 2px;
  }
  .muted {
    color: var(--muted);
  }
  .small {
    font-size: 12px;
  }
  .via {
    display: grid;
    gap: 6px;
  }
  .via label {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: 10px;
    cursor: pointer;
    font-size: 13px;
  }
  .via label.on {
    border-color: color-mix(in srgb, var(--accent) 55%, transparent);
    background: var(--accent-soft);
  }
  .via label > span {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .via small {
    color: var(--muted);
  }
  .res {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 13px;
    margin-right: 12px;
  }
  .err-cell {
    color: var(--muted);
    font-size: 12px;
    max-width: 280px;
    overflow-wrap: anywhere;
  }
  input:not([type]) {
    width: 100%;
  }
  .tabs {
    align-self: flex-start;
    flex-wrap: wrap;
  }
  .via label.off {
    opacity: 0.75;
    cursor: default;
  }
  .chain {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    font-size: 12.5px;
  }
  .chain .node {
    display: flex;
    flex-direction: column;
    padding: 4px 10px;
    border-radius: 8px;
    background: var(--surface-2);
    border: 1px solid var(--line);
  }
  .chain .node small {
    color: var(--muted);
    font-size: 11px;
  }
  .chain .node.me {
    background: var(--accent-soft);
    border-color: color-mix(in srgb, var(--accent) 35%, var(--line));
    color: var(--accent);
    font-weight: 500;
  }
  .chain .arr {
    color: var(--muted);
  }
  .kpis {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
    gap: 14px;
  }
  .toplist {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .toplist li {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 2px 10px;
    font-size: 13px;
  }
  .toplist li span:first-child {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .toplist .bar {
    grid-column: 1 / -1;
    height: 4px;
  }
  .src {
    font-size: 11.5px;
    font-weight: 500;
  }
  .src.cache {
    color: var(--s1);
  }
  .src.up {
    color: var(--s2);
  }
  .src.stale {
    color: var(--s3);
  }
  .src.fail {
    color: var(--warn);
  }
  .dom {
    overflow-wrap: anywhere;
  }
  th.r,
  td.r {
    text-align: right;
  }
</style>
