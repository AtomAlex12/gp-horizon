<script lang="ts">
  // «Прогоны»: GP runs zapret2's blockcheck2 from the Pi — through the same
  // ISP as the router — and saves the strategies that open the domains. The
  // form gathers the domains from four places and says, before the start,
  // what that adds up to: how many, which ones, and roughly for how long.
  import { status } from '../status.svelte';
  import {
    gp,
    runVerdict,
    RUN_STATUS_LABEL,
    LIST_KIND,
    type DomainList,
    type PreflightStatus,
    type RunLogTail,
    type RunMode,
    type RunProgress,
    type ScanLevel,
    type V2flyCategory,
    type V2flyStorage,
    type RunHistoryItem,
  } from '../gp';
  import { runDraft } from '../gpdraft.svelte';
  import { fmtDur, planeOf, splitDomains, modeTitle, plural } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();

  const plane = $derived(planeOf(status.data));
  const lists = $derived(plane?.lists ?? []);

  let preflight = $state<PreflightStatus | null>(null);
  let progress = $state<RunProgress | null>(null);
  let log = $state<RunLogTail | null>(null);
  let err = $state('');
  let busy = $state(false);

  // the sources
  let gpLists = $state<DomainList[]>([]);
  let cats = $state<V2flyCategory[]>([]);
  let storage = $state<V2flyStorage | null>(null);
  let catDomains = $state<Record<string, string[]>>({});
  let catQuery = $state('');
  let chosen = $state<Record<string, boolean>>({}); // our lists, by name
  let chosenGp = $state<Record<string, boolean>>({}); // GP's lists, by id
  let chosenCats = $state<Record<string, boolean>>({}); // v2fly categories
  let peek = $state<Record<string, boolean>>({});
  let extra = $state('');

  // how
  let mode = $state<RunMode>('standard');
  let scan = $state<ScanLevel>('quick');
  let tls12 = $state(true);
  let tls13 = $state(false);
  let http = $state(false);
  let quic = $state(false);
  let repeats = $state(1);
  let repeatParallel = $state(false);
  let ipv6 = $state(false);
  let skipDns = $state(false);

  // «Повторить» from «Результаты»: the domains and settings of that run
  $effect(() => {
    const d = runDraft.v;
    if (!d) return;
    runDraft.v = null;
    extra = d.domains.join('\n');
    chosen = {};
    chosenGp = {};
    chosenCats = {};
    if (d.mode) mode = d.mode;
    const s = d.settings ?? {};
    if (s.scan_level) scan = s.scan_level;
    tls12 = s.enable_tls12 ?? tls12;
    tls13 = s.enable_tls13 ?? tls13;
    http = s.enable_http ?? http;
    quic = s.include_quic ?? quic;
    repeats = s.repeats ?? 1;
    repeatParallel = s.repeat_parallel ?? false;
    ipv6 = s.enable_ipv6 ?? false;
    skipDns = s.skip_dnscheck ?? false;
  });

  // a category's domains, asked for once when it's ticked
  async function toggleCat(name: string) {
    chosenCats[name] = !chosenCats[name];
    if (chosenCats[name] && !catDomains[name]) {
      try {
        catDomains[name] = (await gp.v2flyCategory(name)).domains;
      } catch (e) {
        err = e instanceof Error ? e.message : String(e);
        chosenCats[name] = false;
      }
    }
  }

  const shownCats = $derived.by(() => {
    const q = catQuery.trim().toLowerCase();
    const sel = cats.filter((c) => chosenCats[c.name]);
    const rest = cats.filter((c) => !chosenCats[c.name] && (!q || c.name.includes(q)));
    return [...sel, ...rest.slice(0, q ? 60 : 24)];
  });

  // every domain once, with where it came from first
  const picked = $derived.by(() => {
    const from = new Map<string, string>();
    const parts: string[] = [];
    const add = (d: string, src: string) => {
      if (!from.has(d)) from.set(d, src);
    };
    for (const l of lists)
      if (chosen[l.name]) {
        l.domains.forEach((d) => add(d, l.name));
        parts.push(`${l.name} ${l.domains.length}`);
      }
    for (const name of Object.keys(chosenCats))
      if (chosenCats[name] && catDomains[name]) {
        catDomains[name].forEach((d) => add(d, 'v2fly ' + name));
        parts.push(`v2fly ${name} ${catDomains[name].length}`);
      }
    for (const l of gpLists)
      if (chosenGp[l.list_id]) {
        l.domains.forEach((d) => add(d, l.name));
        parts.push(`${l.name} ${l.domains.length}`);
      }
    const man = splitDomains(extra);
    man.forEach((d) => add(d, ''));
    if (man.length) parts.push(`вручную ${man.length}`);
    return { from, parts };
  });
  const domains = $derived([...picked.from.keys()]);
  const protoCount = $derived([tls12, tls13, http, quic].filter(Boolean).length);
  // how long, from this GP's own finished runs at the same depth: the median
  // seconds per domain. No such runs yet — no number (blockcheck2 tries
  // hundreds of strategies; a domain takes minutes to hours).
  let history = $state<RunHistoryItem[]>([]);
  const perDomain = $derived.by(() => {
    const xs = history
      .filter((r) => r.status === 'success' && r.completed_at && r.domains?.length && (r.settings?.scan_level ?? 'quick') === scan)
      .map((r) => (Date.parse(r.completed_at!) - Date.parse(r.started_at)) / 1000 / r.domains!.length)
      .filter((x) => x > 0)
      .sort((a, b) => a - b);
    return xs.length ? xs[xs.length >> 1] : 0;
  });
  const eta = $derived(perDomain * domains.length);

  const active = $derived(!!progress && ['queued', 'running', 'saving', 'stopping'].includes(progress.status));

  // The newest run, and why it failed if it did. GP reports «idle» once a run
  // has ended, so a run that dies in seconds would otherwise just drop the
  // page back to this form without a word.
  const lastRun = $derived(history.reduce<RunHistoryItem | null>((a, r) => (!a || r.started_at > a.started_at ? r : a), null));
  let lastLog = $state<RunLogTail | null>(null);
  $effect(() => {
    const r = lastRun;
    lastLog = null;
    if (r && (r.status === 'failed' || r.status === 'timeout'))
      gp.runLog(r.run_id).then(
        (l) => (lastLog = l),
        () => {},
      );
  });
  const lastWhy = $derived(lastRun && lastLog ? runVerdict(lastRun.status, lastLog).causes.find((c) => c.level === 'warn') : undefined);

  const loadHistory = () =>
    gp.history().then(
      (r) => (history = r.runs),
      () => {},
    );
  let watching = false; // a run we started or saw going: when it ends, its result is in the history
  async function poll() {
    try {
      progress = await gp.progress();
      if (progress && progress.status !== 'idle') log = await gp.log();
      if (active) watching = true;
      else if (watching) {
        watching = false;
        void loadHistory();
      }
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }
  $effect(() => {
    gp.preflight().then(
      (p) => (preflight = p),
      (e) => (err = e instanceof Error ? e.message : String(e)),
    );
    void loadHistory();
    gp.domainLists().then(
      (r) => (gpLists = r.lists),
      () => {},
    );
    gp.v2flyCategories().then(
      (r) => {
        cats = r.categories.slice().sort((a, b) => a.name.localeCompare(b.name));
        storage = r.storage ?? null;
      },
      () => {},
    );
    void poll();
    const t = setInterval(poll, 3000);
    return () => clearInterval(t);
  });

  async function start() {
    busy = true;
    err = '';
    try {
      await gp.start({
        mode,
        domains,
        protocols: quic ? ['tcp', 'quic'] : ['tcp'],
        settings: {
          scan_level: scan,
          enable_tls12: tls12,
          enable_tls13: tls13,
          enable_http: http,
          include_quic: quic,
          repeats: Math.min(10, Math.max(1, repeats)),
          repeat_parallel: repeatParallel,
          enable_ipv6: ipv6,
          skip_dnscheck: skipDns,
        },
      });
      watching = true;
      await poll();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function stop() {
    if (!confirm('Остановить прогон? Найденные к этому моменту стратегии сохранятся.')) return;
    busy = true;
    try {
      await gp.stop();
      await poll();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const pct = (a?: number, b?: number) => (a && b ? Math.min(100, (a / b) * 100) : 0);
  const n = (v?: number) => (v ?? 0).toLocaleString('ru-RU');
  // the word only: plural() puts the number in front
  const doms = (k: number) => plural(k, 'домен', 'домена', 'доменов').replace(/^\S+ /, '');

  let follow = $state(true);
  function toEnd(el: HTMLElement, _text: string) {
    const end = () => {
      if (follow) el.scrollTop = el.scrollHeight;
    };
    end();
    return { update: end };
  }
</script>

{#if err}<div class="banner warn">{err}</div>{/if}

{#if !active && lastRun && lastWhy}
  <div class="banner warn">
    <div>
      <b>Последний прогон ({new Date(lastRun.started_at).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' })}) завершился с ошибкой: {lastWhy.title}.</b>
      {#if lastWhy.hint}{lastWhy.hint}{/if}
      Подробнее — <button class="linkbtn" onclick={() => go('results')}>«Результаты»</button>.
    </div>
  </div>
{/if}

<div class="banner deg">
  <div>
    <b>Проверка идёт с Pi, через роутер.</b> Для доменов, которые уже есть в списке DPI роутера, nfqws2 на роутере
    добавляет свою стратегию поверх проверяемой — пока Pi не исключён из обработки nfqws2 на роутере (политика
    Keenetic), результаты по этим доменам могут быть неточными.
  </div>
</div>

{#if active && progress}
  <div class="grid split">
    <section class="card">
      <div class="card-head">
        <h2>Идёт прогон</h2>
        <span class="chip acc">{RUN_STATUS_LABEL[progress.status] ?? progress.status}</span>
        <span class="spacer"></span>
        {#if progress.run_id}<span class="hint mono">{progress.run_id.slice(0, 8)}</span>{/if}
      </div>
      <div class="bars">
        {#if progress.domains_total}
          <div class="r">
            <span>Домены</span><span class="v">{n(progress.domains_processed)} из {n(progress.domains_total)}</span>
            <div class="bar"><div class="bar-fill" style="width:{pct(progress.domains_processed, progress.domains_total)}%"></div></div>
          </div>
        {:else}
          <p class="hint">Готовлю проверку: DNS, адреса, обход без стратегии…</p>
        {/if}
        {#if progress.attempts_total}
          <div class="r">
            <span>Попытки</span><span class="v">{n(progress.attempts_processed)} из {n(progress.attempts_total)}</span>
            <div class="bar"><div class="bar-fill" style="width:{pct(progress.attempts_processed, progress.attempts_total)}%"></div></div>
          </div>
        {/if}
        {#if progress.strategies_total}
          <div class="r">
            <span>Стратегии текущего домена</span><span class="v">{n(progress.strategies_processed)} из {n(progress.strategies_total)}</span>
            <div class="bar"><div class="bar-fill" style="width:{pct(progress.strategies_processed, progress.strategies_total)}%"></div></div>
          </div>
        {/if}
      </div>
      <dl class="kv top">
        {#if progress.stage}<dt>этап</dt><dd>{progress.stage}</dd>{/if}
        {#if progress.current_file}<dt>сейчас</dt><dd class="mono">{progress.current_file}</dd>{/if}
        <dt>прошло</dt><dd>{progress.elapsed_seconds ? fmtDur(progress.elapsed_seconds) : '—'}</dd>
        <dt>осталось</dt><dd>{progress.eta_seconds ? '≈ ' + fmtDur(progress.eta_seconds) : '—'}</dd>
        {#if progress.avg_attempt_seconds}<dt>в среднем на попытку</dt><dd>{progress.avg_attempt_seconds.toFixed(1)} с</dd>{/if}
      </dl>
      <div class="row top">
        <button class="warn" onclick={stop} disabled={busy || progress.status === 'stopping'}>Остановить</button>
        <span class="hint">Найденное к этому моменту сохранится.</span>
      </div>
      <p class="hint top">
        Найденные стратегии появляются в <button class="linkbtn" onclick={() => go('strategies')}>«Стратегиях»</button> по
        ходу прогона.
      </p>
    </section>
    <section class="card">
      <div class="card-head">
        <h2>Журнал blockcheck2</h2>
        <span class="spacer"></span>
        <label class="check hint"><input type="checkbox" bind:checked={follow} /> следить</label>
      </div>
      <pre class="term" use:toEnd={log?.stdout_tail ?? ''}>{log?.stdout_tail || 'Ждём первых строк…'}</pre>
      {#if log?.stderr_tail}<details><summary class="hint">ошибки</summary><pre class="term small">{log.stderr_tail}</pre></details>{/if}
    </section>
  </div>
{:else}
  <div class="grid split">
    <div class="stack">
      <section class="card">
        <div class="card-head"><h2>Какие домены проверить</h2></div>
        <div class="stack">
          <div class="src">
            <div class="src-head"><b>Списки GP Horizon</b><span class="hint">из «Списков» — по ним маршрутизирует роутер</span></div>
            {#each lists as l (l.name)}
              <div class="opt">
                <label class="check"
                  ><input type="checkbox" bind:checked={chosen[l.name]} /> <b>{l.name}</b> <span class="muted">· {modeTitle(l.mode)}</span></label
                >
                <span class="n">{n(l.domains.length)} {doms(l.domains.length)}</span>
                <button class="linkbtn" onclick={() => (peek[l.name] = !peek[l.name])}>{peek[l.name] ? 'скрыть' : 'показать'}</button>
              </div>
              {#if peek[l.name]}
                <div class="peek">
                  {l.domains.slice(0, 15).join(', ')}{l.domains.length > 15 ? ` … ещё ${n(l.domains.length - 15)}` : ''}
                  {#if l.source?.startsWith('imported:')}<br /><span class="muted">наполнен из {l.source.slice(9)} при установке</span>{/if}
                </div>
              {/if}
            {:else}
              <p class="hint">Списков пока нет.</p>
            {/each}
          </div>

          <div class="src">
            <div class="src-head">
              <b>Категории v2fly</b><span class="hint">сервис со всеми его доменами и CDN</span>
              <span class="spacer"></span>
              {#if storage && storage.state !== 'ready'}<button class="linkbtn" onclick={() => go('gpdata')}>скачать категории →</button>{/if}
            </div>
            {#if cats.length}
              <input type="search" bind:value={catQuery} placeholder="youtube, telegram, discord…" aria-label="Найти категорию" />
              <div class="cats">
                {#each shownCats as c (c.name)}
                  <label class:on={chosenCats[c.name]}>
                    <input type="checkbox" checked={!!chosenCats[c.name]} onchange={() => toggleCat(c.name)} />
                    {c.name}{#if catDomains[c.name] || c.domain_count}<span class="n">{n(catDomains[c.name]?.length ?? c.domain_count)}</span>{/if}
                  </label>
                {/each}
              </div>
            {:else}
              <p class="hint">{storage && storage.state !== 'ready' ? 'Категории ещё не скачаны.' : 'Загрузка…'}</p>
            {/if}
          </div>

          {#if gpLists.length}
            <div class="src">
              <div class="src-head"><b>Списки GP</b><span class="hint">правятся в «Данных GP»</span></div>
              {#each gpLists as l (l.list_id)}
                <div class="opt">
                  <label class="check"><input type="checkbox" bind:checked={chosenGp[l.list_id]} /> {l.name}</label>
                  <span class="chip {LIST_KIND[l.kind]?.chip}">{LIST_KIND[l.kind]?.label ?? l.kind}</span>
                  <span class="n">{n(l.domains.length)}</span>
                </div>
              {/each}
            </div>
          {/if}

          <div class="src">
            <div class="src-head"><b>Вручную</b><span class="hint">по одному в строке; http:// и путь отрежутся</span></div>
            <textarea rows="3" bind:value={extra} placeholder={'youtube.com\nrutracker.org'} aria-label="Домены вручную"></textarea>
          </div>
        </div>
      </section>

      <section class="card">
        <div class="card-head"><h2>Как подбирать</h2></div>
        <div class="stack">
          <span class="label">Режим</span>
          <div class="pick">
            <label class:on={mode === 'standard'}
              ><input type="radio" bind:group={mode} value="standard" /><b>Каждый домен отдельно</b><small>своя стратегия для каждого сайта</small></label
            >
            <label class:on={mode === 'multi_domain'}
              ><input type="radio" bind:group={mode} value="multi_domain" /><b>Несколько вместе</b><small>стратегии, что открывают их все</small></label
            >
            <label class:on={mode === 'common_strategy'}
              ><input type="radio" bind:group={mode} value="common_strategy" /><b>Одна общая</b><small>одна стратегия на весь список</small></label
            >
          </div>
          <span class="label">Что проверять</span>
          <div class="row">
            <label class="check"><input type="checkbox" bind:checked={tls12} /> HTTPS · TLS 1.2</label>
            <label class="check"><input type="checkbox" bind:checked={tls13} /> HTTPS · TLS 1.3</label>
            <label class="check"><input type="checkbox" bind:checked={http} /> HTTP</label>
            <label class="check"><input type="checkbox" bind:checked={quic} /> QUIC</label>
          </div>
          <span class="label">Глубина перебора</span>
          <div class="seg" role="radiogroup" aria-label="Глубина перебора">
            <button class:on={scan === 'quick'} onclick={() => (scan = 'quick')}>быстро</button>
            <button class:on={scan === 'standard'} onclick={() => (scan = 'standard')}>обычно</button>
            <button class:on={scan === 'force'} onclick={() => (scan = 'force')}>всё подряд</button>
          </div>
          <details>
            <summary class="linkbtn">Дополнительно</summary>
            <dl class="kv top">
              <dt>повторов каждой проверки</dt>
              <dd><input type="number" min="1" max="10" bind:value={repeats} aria-label="повторов" /></dd>
              <dt>повторы параллельно</dt><dd><input type="checkbox" bind:checked={repeatParallel} aria-label="повторы параллельно" /></dd>
              <dt>проверять и IPv6</dt><dd><input type="checkbox" bind:checked={ipv6} aria-label="IPv6" /></dd>
              <dt>пропустить проверку DNS</dt><dd><input type="checkbox" bind:checked={skipDns} aria-label="без проверки DNS" /></dd>
            </dl>
            <p class="hint top">
              Таймауты curl и параллельность — общие настройки GP, в <button class="linkbtn" onclick={() => go('gpdata')}>«Данных GP»</button>.
            </p>
          </details>
        </div>
      </section>
    </div>

    <aside class="card sum" aria-label="Итог перед запуском">
      <div class="stack">
        <div>
          <span class="label">Будет проверено</span>
          <div class="row">
            <span class="big">{n(domains.length)}</span><span class="muted">{doms(domains.length)}</span>
            <span class="spacer"></span>
            {#if domains.length && eta}<span class="chip {eta > 3600 ? 'warn' : eta > 900 ? 'deg' : ''}" title="по прошлым прогонам этой глубины">≈ {fmtDur(eta)}</span>{/if}
          </div>
        </div>
        {#if domains.length > 30}
          <div class="banner {domains.length > 100 ? 'warn' : 'deg'}">
            <div>
              <b>{n(domains.length)} {doms(domains.length)} — {eta ? `прогон на ≈ ${fmtDur(eta)}` : 'прогон на много часов'}.</b>
              blockcheck2 перебирает сотни стратегий для каждого домена. Чтобы проверить один сайт, снимите списки и впишите его вручную.
            </div>
          </div>
        {:else if domains.length && !eta}
          <p class="hint">Каждый домен — от нескольких минут до часов: оценка появится после первых прогонов.</p>
        {/if}
        {#if domains.length}
          <div class="chips">
            {#each domains.slice(0, 60) as d (d)}
              <span class="chip mono" title={picked.from.get(d) || 'вручную'}>{d}{#if picked.from.get(d)}<i> · {picked.from.get(d)}</i>{/if}</span>
            {/each}
            {#if domains.length > 60}<span class="chip">ещё {n(domains.length - 60)}</span>{/if}
          </div>
          <p class="hint">Из: {picked.parts.join(' + ')}{picked.parts.length > 1 ? ' · повторы убраны' : ''}</p>
        {:else}
          <p class="hint">Отметьте список, категорию или впишите домены.</p>
        {/if}

        {#if preflight}
          <span class="label">Готовность</span>
          <ul class="checklist">
            {#each preflight.checks as c (c.name)}
              <li title={c.message}>
                <span class="dot {c.status === 'ok' ? 'ok' : c.status === 'warning' ? 'deg' : c.status === 'error' ? 'warn' : ''}"></span>
                <span><b>{c.name}</b>{#if c.status !== 'ok' && c.message}<span class="muted"> · {c.message}</span>{/if}</span>
              </li>
            {/each}
          </ul>
        {/if}
        <button onclick={start} disabled={busy || !domains.length || !protoCount || (preflight !== null && !preflight.ready)}>
          {busy ? 'Запускаю…' : domains.length ? `Начать прогон · ${n(domains.length)}` : 'Выберите домены'}
        </button>
        {#if progress && progress.status !== 'idle'}
          <p class="hint">
            Прошлый прогон: {RUN_STATUS_LABEL[progress.status] ?? progress.status} —
            <button class="linkbtn" onclick={() => go('results')}>«Результаты»</button>.
          </p>
        {/if}
      </div>
    </aside>
  </div>
{/if}

<style>
  h2 {
    font-size: 15px;
  }
  .split {
    grid-template-columns: minmax(0, 1.25fr) minmax(0, 1fr);
    align-items: start;
  }
  .top {
    margin-top: 12px;
  }
  .label {
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--muted);
    font-weight: 600;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
  }
  .src {
    border: 1px solid var(--line);
    border-radius: 10px;
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .src-head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    font-size: 13px;
  }
  .opt {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    font-size: 13px;
  }
  .n {
    margin-left: auto;
    color: var(--muted);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .peek {
    font-family: var(--font-mono);
    font-size: 11.5px;
    color: var(--muted);
    padding-left: 24px;
    overflow-wrap: anywhere;
  }
  .cats {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 6px;
    max-height: 170px;
    overflow: auto;
  }
  .cats label {
    display: flex;
    gap: 6px;
    align-items: center;
    font-size: 12.5px;
    padding: 4px 8px;
    border: 1px solid var(--line);
    border-radius: 8px;
    cursor: pointer;
  }
  .cats label.on,
  .pick label.on {
    border-color: color-mix(in srgb, var(--accent) 55%, transparent);
    background: var(--accent-soft);
  }
  .pick {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
    gap: 6px;
  }
  .pick label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: 10px;
    cursor: pointer;
    font-size: 13px;
    position: relative;
  }
  .pick small {
    color: var(--muted);
    font-size: 11.5px;
  }
  .pick input {
    position: absolute;
    opacity: 0;
    pointer-events: none;
  }
  .sum {
    position: sticky;
    top: 12px;
  }
  .big {
    font-size: 30px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.1;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    max-height: 170px;
    overflow: auto;
  }
  .chips .chip {
    font-weight: 400;
  }
  .chips i {
    font-style: normal;
    color: var(--muted);
  }
  .checklist {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 13px;
  }
  .checklist li {
    display: flex;
    gap: 8px;
    align-items: baseline;
  }
  .bars {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .bars .r {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 4px 10px;
    font-size: 13px;
  }
  .bars .v {
    font-variant-numeric: tabular-nums;
    color: var(--ink-2);
  }
  .bars .bar {
    grid-column: 1 / -1;
  }
  .term {
    margin: 0;
    height: 340px;
    overflow: auto;
    background: var(--surface-2);
    border-radius: 8px;
    padding: 10px;
    font-family: var(--font-mono);
    font-size: 11.5px;
    white-space: pre-wrap;
    word-break: break-word;
  }
  .term.small {
    height: auto;
    max-height: 160px;
  }
  .linkbtn {
    display: inline;
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    font-size: inherit;
    text-decoration: underline;
    cursor: pointer;
  }
  dd input[type='number'] {
    width: 70px;
    text-align: right;
  }
  @media (max-width: 860px) {
    .split {
      grid-template-columns: 1fr;
    }
    .sum {
      position: static;
    }
  }
</style>
