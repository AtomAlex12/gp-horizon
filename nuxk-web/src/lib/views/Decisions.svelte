<script lang="ts">
  // «Решения»: why a site doesn't open and which way out works. nuxk measures
  // the domain itself (directly, then through the tunnels) and only advises —
  // "Принять" adds the domain to a «Подбор» list; the router changes only when
  // routing apply is on. A site that refuses Russia needs VLESS; one the
  // provider cuts gets DPI, then WARP, then a strategy search on the Pi.
  import { api, HttpError, type ListMode, type SuggestOverview, type Suggestion, type SuggestQueued } from '../api';
  import { status } from '../status.svelte';
  import { planeOf, modeTitle, ago, splitDomains } from '../ui';
  import SuggestItem from '../decisions/SuggestItem.svelte';
  import SearchAdvice from '../decisions/SearchAdvice.svelte';
  import SettingsCard from '../decisions/SettingsCard.svelte';
  import { verdictOf } from '../decisions/text';
  import { untrack } from 'svelte';

  const plane = $derived(planeOf(status.data));

  let ov = $state<SuggestOverview | null>(null);
  let err = $state('');
  let input = $state('');
  let sending = $state(false);
  let pending = $state<string[]>([]); // domains an accept/dismiss is in flight for
  let msg = $state<{ ok: boolean; text: string } | null>(null);

  // a poll and an action's refresh can overlap: only the latest answer counts
  let seq = 0;
  async function load() {
    const n = ++seq;
    try {
      const o = await api.suggest();
      if (n !== seq) return;
      ov = o;
      err = '';
    } catch (e) {
      if (n !== seq) return;
      err =
        e instanceof HttpError && e.status === 404
          ? 'Агент на роутере не знает этого раздела — обновите nuxk-core («Система» → обновление).'
          : e instanceof Error
            ? e.message
            : String(e);
    }
  }

  // every 3 s while something is being measured, every 30 s otherwise;
  // refresh() also restarts the clock after an action
  let timer: ReturnType<typeof setTimeout> | undefined;
  let alive = false;
  async function refresh() {
    clearTimeout(timer);
    await load();
    if (!alive) return;
    clearTimeout(timer);
    timer = setTimeout(refresh, (ov?.busy ?? 0) > 0 ? 3000 : 30000);
  }
  $effect(() => {
    alive = true;
    untrack(() => void refresh());
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  });

  function queuedText(q: SuggestQueued) {
    const parts: string[] = [];
    if (q.queued.length) parts.push(`Проверяю: ${q.queued.join(', ')}.`);
    for (const s of q.skipped) parts.push(`${s.domain} — пропущен: ${s.reason}.`);
    return parts.join(' ') || 'Нечего проверять.';
  }

  async function queue(f: () => Promise<SuggestQueued>) {
    if (sending) return false;
    sending = true;
    msg = null;
    try {
      const q = await f();
      msg = { ok: q.queued.length > 0, text: queuedText(q) };
      await refresh();
      return true;
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
      return false;
    } finally {
      sending = false;
    }
  }

  async function check() {
    const domains = splitDomains(input);
    if (!domains.length) return;
    if (domains.length > 20) {
      msg = { ok: false, text: `За раз — не больше 20 доменов, а введено ${domains.length}.` };
      return;
    }
    if (await queue(() => api.suggestCheck(domains))) input = '';
  }
  const recheck = (d: string) => queue(() => api.suggestCheck([d]));

  // the buttons stay locked until the list shows the result, not just the answer
  async function act(domain: string, f: () => Promise<unknown>, done: string) {
    if (pending.includes(domain)) return;
    pending = [...pending, domain];
    msg = null;
    try {
      await f();
      msg = { ok: true, text: done };
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    }
    await refresh();
    pending = pending.filter((d) => d !== domain);
  }

  function accept(s: Suggestion, mode: ListMode) {
    // the first «DPI» list makes nuxk the owner of nfqws2's own site list
    if (
      mode === 'desync' &&
      plane &&
      !plane.desync_managed &&
      !confirm(
        `Это будет первый список «DPI». С ним nuxk начнёт сам вести список сайтов nfqws2 (user.list): ` +
          `при включённом применении файл заменится списками nuxk. Если вы вписывали туда сайты вручную, ` +
          `сначала перенесите их в «Списки».

Добавить ${s.domain} в «Подбор: DPI»?`,
      )
    )
      return;
    return act(
      s.domain,
      () => api.suggestAccept(s.domain, mode),
      `${s.domain} добавлен в список «Подбор: ${modeTitle(mode)}». ` +
        (plane?.apply
          ? 'Роутер применит его за пару минут, после этого nuxk проверит, открывается ли сайт.'
          : 'Роутер не изменится, пока маршрутизация в режиме плана.'),
    );
  }
  const dismiss = (s: Suggestion) =>
    act(
      s.domain,
      () => api.suggestDismiss(s.domain),
      s.state === 'accepted' ? `${s.domain} убран из «Решений»; в списке он остался.` : `${s.domain} скрыт.`,
    );

  const items = $derived(ov?.items ?? []);
  // what's being measured first, then what can be acted on, then the rest
  const rank = (s: Suggestion) => (s.state === 'checking' ? 0 : s.mode ? 1 : 2);
  const active = $derived(
    items.filter((i) => i.state === 'checking' || (i.state === 'ready' && !i.hidden)).sort((a, b) => rank(a) - rank(b)),
  );
  const accepted = $derived(items.filter((i) => i.state === 'accepted'));
  const dismissed = $derived(items.filter((i) => i.state === 'dismissed'));
  const quiet = $derived(items.filter((i) => i.state === 'ready' && i.hidden));
  const searching = $derived(items.filter((i) => i.next === 'search' && i.state !== 'dismissed'));

  const WAY_TITLE: Record<string, string> = { direct: 'напрямую', warp: 'WARP', vless: 'VLESS' };
  const tunnels = $derived((ov?.paths ?? []).filter((p) => p.via !== 'direct'));
  const direct = $derived((ov?.paths ?? []).find((p) => p.via === 'direct'));
  const vlessUsable = $derived(ov?.paths?.find((p) => p.via === 'vless')?.usable ?? false);
</script>

{#if err && !ov}
  <p class="err-text">{err}</p>
{:else if !ov}
  <p class="muted">Загрузка…</p>
{:else}
  <section class="card">
    <div class="card-head">
      <h2>Почему не открывается сайт</h2>
      <span class="spacer"></span>
      {#if ov.busy > 0}<span class="chip acc"><span class="dot ok pulse"></span>проверяется: {ov.busy}</span>{/if}
    </div>
    <p class="hint">
      nuxk заходит на сайт сам: сначала напрямую, и только если не вышло — через туннели, по несколько попыток на путь. По
      тому, где обрывается связь, он отличает блокировку провайдером (DPI, адрес, подмена DNS, заморозка) от отказа самого
      сайта работать с Россией. Домены, которые уже есть в ваших списках, не проверяются.
    </p>
    <div class="row add">
      <input
        type="text"
        bind:value={input}
        placeholder="chatgpt.com, claude.ai"
        aria-label="Домены для проверки"
        onkeydown={(e) => e.key === 'Enter' && check()}
      />
      <button onclick={check} disabled={sending || !input.trim()}>Проверить</button>
      <button
        class="ghost"
        onclick={() => queue(api.suggestScan)}
        disabled={sending}
        title="ChatGPT, OpenAI, Claude, Anthropic, Perplexity, Gemini, Copilot, Netflix, Spotify, Udemy"
      >
        Проверить популярные сервисы
      </button>
    </div>
    {#if msg}<p class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</p>{/if}

    <p class="advise">
      <b>Только советует.</b> Сам nuxk ничего не меняет: «Принять» добавляет домен в список «Подбор» (раздел
      <a href="#lists">«Списки»</a>).
      {#if !plane}
        <span class="warnline">Маршрутизация выключена (<span class="mono">PLANE=</span> в nuxk.conf) — предложения можно смотреть, принять нельзя.</span>
      {:else if !plane.apply}
        Маршрутизация в режиме плана: роутер не изменится, пока не включено <span class="mono">PLANE_APPLY="1"</span>.
      {:else}
        Применение включено: принятый домен попадёт на роутер, и nuxk проверит, открывается ли сайт.
      {/if}
    </p>

    <div class="ways" role="group" aria-label="Чем проверяется">
      <span class="lbl">Пути</span>
      <span class="chip {direct && !direct.usable ? 'deg' : 'ok'}" title={direct?.iface ?? ''}>
        напрямую{direct && !direct.usable && direct.why ? ` — ${direct.why}` : ''}
      </span>
      {#if ov.desync}<span class="chip ok" title="nfqws2 подключён">DPI (nfqws2)</span>{/if}
      {#each tunnels as p (p.via)}
        <span class="chip {p.usable ? 'ok' : 'deg'}" title={p.iface ? `интерфейс ${p.iface}` : ''}>
          {WAY_TITLE[p.via] ?? p.via}{p.usable ? '' : ` — ${p.why ?? 'недоступен'}`}
        </span>
      {/each}
    </div>
    {#if !vlessUsable}
      <p class="hint warnline">
        VLESS сейчас проверить нельзя: вывод «сайт блокирует Россию» будет непроверенным — открыть сайт через туннель для
        сравнения нечем.
      </p>
    {/if}

    <div class="ways">
      <span class="lbl">Поиск по DNS</span>
      {#if ov.discovering}
        <span class="chip ok">идёт</span>
        <span class="hint">nuxk сам проверяет имена, о которых часто спрашивает роутер, и показывает здесь те, с которыми что-то не так.</span>
      {:else if ov.settings.discover}
        <span class="chip deg">не работает</span>
        <span class="hint">Новые сайты находятся по журналу защищённого DNS — включите его в <a href="#dns">«DNS»</a>.</span>
      {:else}
        <span class="chip">выключен</span>
        <span class="hint">включается в «Настройках проверки» ниже</span>
      {/if}
    </div>
  </section>

  {#if searching.length > 1}
    <section class="card">
      <div class="card-head"><h2>Нужен подбор стратегии</h2><span class="chip mono">{searching.length}</span></div>
      <p class="hint mono doms">{searching.map((s) => s.domain).join(', ')}</p>
      <SearchAdvice domains={searching.map((s) => s.domain)} />
    </section>
  {/if}

  <section class="card">
    <div class="card-head">
      <h2>Предложения</h2>
      <span class="chip mono">{active.length}</span>
    </div>
    {#if active.length === 0}
      <p class="empty">Пока пусто. Введите домен выше или проверьте популярные сервисы — результат появится здесь.</p>
    {:else}
      <div class="items">
        {#each active as s (s.domain)}
          <SuggestItem
            {s}
            canAccept={!!plane}
            pending={pending.includes(s.domain)}
            onaccept={(m) => accept(s, m)}
            ondismiss={() => dismiss(s)}
          />
        {/each}
      </div>
    {/if}

    {#if quiet.length}
      <details class="fold">
        <summary>Найдены по DNS, делать ничего не нужно: {quiet.length}</summary>
        <p class="hint">
          Открываются напрямую или не похожи на сайт, который маршрут сделал бы рабочим. Снова проверятся не раньше чем через
          неделю; проверить сейчас — введите домен выше.
        </p>
        <ul class="mini">
          {#each quiet as s (s.domain)}
            {@const v = verdictOf(s)}
            <li>
              <span class="mono">{s.domain}</span>
              <span class="chip {v.cls}">{v.label}</span>
              {#if s.verdict !== 'ok' && s.reason}<span class="hint">{s.reason}</span>{/if}
              <span class="hint">{ago(s.checked_at)}</span>
            </li>
          {/each}
        </ul>
      </details>
    {/if}

    {#if dismissed.length}
      <details class="fold">
        <summary>Скрытые: {dismissed.length}</summary>
        <p class="hint">Сами сюда не возвращаются и не перепроверяются. Проверить снова — кнопкой рядом с доменом.</p>
        <ul class="mini">
          {#each dismissed as s (s.domain)}
            {@const v = verdictOf(s)}
            <li>
              <span class="mono">{s.domain}</span>
              {#if s.verdict && s.verdict !== 'unknown'}<span class="chip {v.cls}">{v.label}</span>{/if}
              <span class="spacer"></span>
              {#if s.accepted}
                <!-- still in «Подбор»: domains already in a list aren't checked -->
                <span class="hint">в списке «Подбор: {modeTitle(s.accepted)}» — чтобы проверить заново, уберите его оттуда</span>
              {:else}
                <button class="ghost sm" onclick={() => recheck(s.domain)} disabled={sending} aria-label="Проверить {s.domain} снова">
                  Проверить снова
                </button>
              {/if}
            </li>
          {/each}
        </ul>
      </details>
    {/if}
  </section>

  {#if accepted.length}
    <section class="card">
      <div class="card-head">
        <h2>Принятые</h2>
        <span class="chip mono">{accepted.length}</span>
        <span class="spacer"></span>
        <span class="hint">проверяются через ~2 мин после применения, потом раз в сутки</span>
      </div>
      <div class="items">
        {#each accepted as s (s.domain)}
          <SuggestItem
            {s}
            canAccept={!!plane}
            pending={pending.includes(s.domain)}
            onaccept={(m) => accept(s, m)}
            ondismiss={() => dismiss(s)}
          />
        {/each}
      </div>
    </section>
  {/if}

  <SettingsCard
    settings={ov.settings}
    reference={ov.reference}
    discovering={ov.discovering}
    onsaved={(s) => ov && (ov.settings = s)}
  />

  {#if err}<p class="err-text">Нет связи с агентом: {err} Показаны последние данные.</p>{/if}
  <p class="hint legend">
    Как читать выводы: «сайт блокирует Россию» — отказывает сам сайт, поможет только VLESS (WARP выходит в Москве); «блок
    по имени», «заморозка» — режет провайдер: сначала DPI, если не помог — подбор стратегии, а пока он идёт — туннель;
    «блок по адресу» — то же, но если соединения нет вовсе, DPI не поможет, нужен туннель; «подмена DNS» — нужен
    защищённый DNS, маршрут не поможет.
  </p>
{/if}

<style>
  .add {
    margin-top: 10px;
  }
  .add input {
    flex: 1;
    min-width: 200px;
  }
  .advise {
    margin: 12px 0 0;
    font-size: 12.5px;
    color: var(--ink-2);
  }
  .advise a,
  .ways a {
    color: var(--accent);
  }
  .ways {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    margin-top: 10px;
  }
  /* a way's "why not" can be long: let it wrap on a phone */
  .ways .chip {
    white-space: normal;
  }
  .lbl {
    min-width: 92px;
    color: var(--muted);
    font-size: 12px;
  }
  .warnline {
    color: var(--degraded);
  }
  p.warnline {
    margin-top: 6px;
  }
  .items {
    display: flex;
    flex-direction: column;
  }
  .doms {
    margin-bottom: 10px;
    overflow-wrap: anywhere;
  }
  .fold {
    margin-top: 12px;
    border-top: 1px solid var(--line);
    padding-top: 10px;
  }
  .fold summary {
    cursor: pointer;
    font-size: 13px;
    color: var(--muted);
  }
  .fold > .hint {
    margin: 6px 0;
  }
  .mini {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .mini li {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    font-size: 13px;
    padding: 3px 0;
  }
  .mini .mono {
    overflow-wrap: anywhere;
  }
  .legend {
    max-width: 860px;
  }
  @media (max-width: 480px) {
    .lbl {
      width: 100%;
    }
  }
</style>
