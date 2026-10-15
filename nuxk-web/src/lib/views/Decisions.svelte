<script lang="ts">
  // «Решения»: why a site doesn't open and which way out works. nuxk probes the
  // domain itself (directly, then through the tunnels) and only suggests —
  // "Принять" adds the domain to the «Подбор» list, nothing happens on its own.
  import { api, type ListMode, type SuggestOverview, type Suggestion } from '../api';
  import { status } from '../status.svelte';
  import { planeOf, modeTitle, ago, splitDomains } from '../ui';

  const plane = $derived(planeOf(status.data));

  let ov = $state<SuggestOverview | null>(null);
  let err = $state('');
  let input = $state('');
  let busyBtn = $state(false);
  let msg = $state<{ ok: boolean; text: string } | null>(null);
  let open = $state<Record<string, boolean>>({});

  async function load() {
    try {
      ov = await api.suggest();
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
  }

  // quick while something is being probed, lazy otherwise
  $effect(() => {
    void load();
    let t: ReturnType<typeof setTimeout>;
    const tick = async () => {
      await load();
      t = setTimeout(tick, ov && ov.busy > 0 ? 1500 : 15000);
    };
    t = setTimeout(tick, 1500);
    return () => clearTimeout(t);
  });

  function queuedText(q: { queued: string[]; skipped: { domain: string; reason: string }[] }) {
    const parts: string[] = [];
    if (q.queued.length) parts.push(`Проверяю: ${q.queued.join(', ')}.`);
    for (const s of q.skipped) parts.push(`${s.domain} — пропущен: ${s.reason}.`);
    return parts.join(' ') || 'Нечего проверять.';
  }

  async function run(f: () => Promise<{ queued: string[]; skipped: { domain: string; reason: string }[] }>) {
    busyBtn = true;
    msg = null;
    try {
      const q = await f();
      msg = { ok: q.queued.length > 0, text: queuedText(q) };
      await load();
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    } finally {
      busyBtn = false;
    }
  }

  const check = () => {
    const domains = splitDomains(input);
    if (!domains.length) return;
    void run(() => api.suggestCheck(domains)).then(() => (input = ''));
  };

  async function act(f: () => Promise<unknown>, done: string) {
    msg = null;
    try {
      await f();
      msg = { ok: true, text: done };
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    }
    await load();
  }

  const accept = (s: Suggestion, mode: ListMode) =>
    act(() => api.suggestAccept(s.domain, mode), `${s.domain} добавлен в список «Подбор: ${modeTitle(mode)}».`);
  const dismiss = (s: Suggestion) => act(() => api.suggestDismiss(s.domain), `${s.domain} скрыт.`);

  const VERDICT: Record<string, { label: string; cls: string }> = {
    ok: { label: 'открывается', cls: 'ok' },
    geoblock: { label: 'отказ по стране', cls: 'deg' },
    'sni-block': { label: 'DPI по имени (SNI)', cls: 'deg' },
    'ip-block': { label: 'блокировка по IP', cls: 'deg' },
    'dns-block': { label: 'подмена DNS', cls: 'deg' },
    unknown: { label: 'неясно', cls: '' },
  };
  const PATH_TITLE: Record<string, string> = { direct: 'напрямую', vless: 'VLESS', warp: 'WARP', dns: 'DNS' };

  const active = $derived((ov?.items ?? []).filter((i) => i.state !== 'dismissed'));
  const dismissed = $derived((ov?.items ?? []).filter((i) => i.state === 'dismissed'));
  const vlessUsable = $derived(ov?.paths?.find((p) => p.via === 'vless')?.usable ?? false);
</script>

{#if err && !ov}
  <p class="err-text">{err}</p>
{:else if !ov}
  <p class="muted">Загрузка…</p>
{:else}
  <section class="card">
    <div class="card-head"><h2>Почему не открывается сайт</h2></div>
    <p class="hint">
      nuxk заходит на сайт сам: сначала напрямую, и только если не вышло — через туннели. По тому, где именно
      обрывается связь, он отличает блокировку провайдером (DPI, IP) от отказа самого сайта по стране (например,
      ChatGPT или Claude). Сам ничего не меняет: вы решаете, принять ли предложение. Домены, которые уже есть в ваших
      списках, не проверяются.
    </p>
    <div class="row add">
      <input
        type="text"
        bind:value={input}
        placeholder="chatgpt.com, claude.ai"
        aria-label="Домены для проверки"
        onkeydown={(e) => e.key === 'Enter' && check()}
      />
      <button onclick={check} disabled={busyBtn || !input.trim()}>Проверить</button>
      <button class="ghost" onclick={() => run(api.suggestScan)} disabled={busyBtn} title="ChatGPT, OpenAI, Claude, Anthropic, Perplexity, Netflix, Spotify">
        Проверить популярные сервисы
      </button>
    </div>
    {#if msg}<p class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</p>{/if}
  </section>

  <section class="card">
    <div class="card-head">
      <h2>Чем проверяется</h2>
      <span class="spacer"></span>
      {#if ov.busy > 0}<span class="chip acc"><span class="dot ok pulse"></span>проверяется: {ov.busy}</span>{/if}
    </div>
    <div class="row paths">
      {#each ov.paths ?? [] as p (p.via)}
        <span class="chip {p.usable ? 'ok' : 'deg'}" title={p.why ?? ''}>
          {PATH_TITLE[p.via] ?? p.via}{p.iface ? ` · ${p.iface}` : ''}{p.usable ? '' : ` — ${p.why}`}
        </span>
      {/each}
      <span class="chip {ov.desync ? 'ok' : ''}">DPI (nfqws2): {ov.desync ? 'подключён' : 'нет'}</span>
    </div>
    {#if !vlessUsable}
      <p class="hint warnline">
        ⚠ VLESS-туннель сейчас недоступен для проверки: вывод «отказ по стране» будет непроверенным — сайт не удалось
        открыть через туннель для сравнения.
      </p>
    {/if}
    {#if !plane}
      <p class="hint warnline">⚠ Плоскость маршрутизации выключена (<span class="mono">PLANE=</span>): предложения можно смотреть, принять нельзя.</p>
    {:else if !plane.apply}
      <p class="hint">
        Плоскость в режиме плана: «Принять» добавит домен в список, но роутер не изменится, пока не включено
        <span class="mono">PLANE_APPLY="1"</span>.
      </p>
    {/if}
  </section>

  <section class="card">
    <div class="card-head">
      <h2>Предложения</h2>
      <span class="spacer"></span>
      <span class="chip mono">{active.length}</span>
    </div>
    {#if active.length === 0}
      <p class="hint">Пока пусто. Введите домен выше или проверьте популярные сервисы — результат появится здесь.</p>
    {:else}
      <div class="tbl-wrap">
        <table>
          <thead><tr><th>домен</th><th>что с ним</th><th>решение</th><th></th></tr></thead>
          <tbody>
            {#each active as s (s.domain)}
              {@const v = VERDICT[s.verdict ?? 'unknown'] ?? VERDICT.unknown}
              <tr>
                <td class="mono dom">{s.domain}</td>
                <td>
                  {#if s.state === 'checking'}
                    <span class="chip acc"><span class="dot ok pulse"></span>проверяется…</span>
                  {:else}
                    <span class="chip {v.cls}">{v.label}</span>
                    <p class="reason">{s.reason}</p>
                    {#if (s.paths ?? []).length}
                      <button class="linkbtn" onclick={() => (open[s.domain] = !open[s.domain])}>
                        {open[s.domain] ? 'скрыть подробности' : 'подробности'}
                      </button>
                      {#if open[s.domain]}
                        <ul class="paths-list">
                          {#each s.paths ?? [] as p}
                            <li><b>{PATH_TITLE[p.via] ?? p.via}:</b> {p.result}{p.ms ? ` · ${p.ms} мс` : ''}</li>
                          {/each}
                        </ul>
                        <p class="hint">проверено {ago(s.checked_at)}</p>
                      {/if}
                    {/if}
                  {/if}
                </td>
                <td>
                  {#if s.state === 'accepted'}
                    <span class="chip acc">принято</span>
                  {:else if s.mode}
                    <span class="chip mono">{modeTitle(s.mode)}</span>
                    <span class="hint">{s.confirmed ? 'проверено: сайт открылся' : 'не проверено'}</span>
                  {:else if s.state === 'ready'}
                    <span class="hint">маршрут не поможет</span>
                  {/if}
                </td>
                <td class="actions">
                  {#if s.state === 'ready'}
                    <div class="row">
                      {#if s.mode}
                        <button class="sm" onclick={() => accept(s, s.mode as ListMode)} disabled={!plane}>
                          Принять · {modeTitle(s.mode)}
                        </button>
                      {/if}
                      {#if s.alt}
                        <button class="ghost sm" onclick={() => accept(s, s.alt as ListMode)} disabled={!plane}>
                          {modeTitle(s.alt)} вместо
                        </button>
                      {/if}
                      <button class="ghost sm" onclick={() => dismiss(s)} aria-label="Скрыть предложение для {s.domain}">Скрыть</button>
                    </div>
                  {:else if s.state === 'accepted'}
                    <button class="ghost sm" onclick={() => dismiss(s)} aria-label="Убрать {s.domain} из списка предложений">Убрать</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if dismissed.length}
      <details class="dismissed">
        <summary>Скрытые ({dismissed.length})</summary>
        <p class="hint">
          Популярные сервисы из проверки не возвращаются сюда сами. Чтобы проверить снова, введите домен выше.
        </p>
        <p class="mono small">{dismissed.map((d) => d.domain).join(', ')}</p>
      </details>
    {/if}
  </section>
{/if}

<style>
  .add {
    margin-top: 10px;
    flex-wrap: wrap;
  }
  .add input {
    flex: 1;
    min-width: 220px;
  }
  .paths {
    gap: 6px;
    flex-wrap: wrap;
  }
  .dom {
    white-space: nowrap;
    vertical-align: top;
  }
  td {
    vertical-align: top;
  }
  .reason {
    margin: 6px 0 2px;
    max-width: 520px;
    color: var(--ink-2);
    font-size: 13px;
  }
  .paths-list {
    margin: 6px 0 2px;
    padding-left: 18px;
    font-size: 12.5px;
    color: var(--ink-2);
  }
  .actions {
    min-width: 170px;
  }
  .warnline {
    color: var(--degraded);
    margin-top: 6px;
  }
  .small {
    font-size: 11.5px;
    color: var(--muted);
  }
  .linkbtn {
    background: none;
    border: 0;
    padding: 0;
    color: var(--accent);
    font-size: 12px;
    text-decoration: underline;
  }
  .dismissed {
    margin-top: 12px;
  }
  .dismissed summary {
    cursor: pointer;
    font-size: 13px;
    color: var(--muted);
  }
</style>
