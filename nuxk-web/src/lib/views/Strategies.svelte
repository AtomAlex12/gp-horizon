<script lang="ts">
  // «Стратегии»: what GP found, and what nuxk has put on the router's nfqws2.
  // Applying is always the person's click, with what will change spelled out:
  // a profile in NFQWS_ARGS_CUSTOM for the chosen domains only, a backup of
  // nfqws2.conf, a restart of nfqws2 — and the old conf back if it won't start.
  import { api, type Strategy } from '../api';
  import { gp, type StrategyCandidate } from '../gp';
  import { ago } from '../ui';

  let candidates = $state<StrategyCandidate[] | null>(null);
  let applied = $state<Strategy[] | null>(null);
  let err = $state('');
  let msg = $state('');
  let domain = $state('');
  let proto = $state('');

  async function load() {
    try {
      // everything found = candidates of every domain a run has checked
      const runDomains = [...new Set((await gp.history()).runs.flatMap((r) => r.domains ?? []))];
      candidates = runDomains.length ? (await gp.candidates(runDomains)).candidates : [];
      err = '';
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    }
    try {
      applied = (await api.strategies('nfqws2')).strategies;
    } catch (e) {
      applied = null;
      err ||= 'Стратегии на роутере: ' + (e instanceof Error ? e.message : String(e));
    }
  }
  $effect(() => {
    void load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  });

  const domainsOf = (c: StrategyCandidate) => [...new Set([...(c.seen ?? []).map((s) => s.domain), ...(c.common_seen ?? []).flatMap((x) => x.domains)])];
  const allDomains = $derived([...new Set((candidates ?? []).flatMap(domainsOf))].sort());
  const allProtos = $derived([...new Set((candidates ?? []).map((c) => c.protocol))].sort());
  const shown = $derived(
    (candidates ?? []).filter((c) => (!domain || domainsOf(c).includes(domain)) && (!proto || c.protocol === proto)),
  );

  // GP's test name → the nfqws2 filter nuxk puts in front of the profile
  function protocolOf(p: string): 'tls' | 'http' | 'quic' | '' {
    const v = p.toLowerCase();
    if (/quic|http3|h3/.test(v)) return 'quic';
    if (/tls|https/.test(v)) return 'tls';
    if (/http/.test(v)) return 'http';
    return '';
  }
  const PROTO_LABEL: Record<string, string> = { tls: 'TLS (443/tcp)', http: 'HTTP (80/tcp)', quic: 'QUIC (443/udp)' };

  // the one being applied
  let pending = $state<{ c: StrategyCandidate; protocol: 'tls' | 'http' | 'quic' | ''; picked: Record<string, boolean> } | null>(null);
  let busy = $state(false);

  function startApply(c: StrategyCandidate) {
    msg = '';
    const picked: Record<string, boolean> = {};
    for (const d of domainsOf(c)) picked[d] = true;
    pending = { c, protocol: protocolOf(c.protocol), picked };
  }

  // the agent's rule (engine.ValidateStrategy), checked here first so the
  // button can say why before anyone clicks: desync args only, no files,
  // no Lua code (--lua-init), no shell characters
  const ARG = /^--(payload|lua-desync|out-range|in-range)=[A-Za-z0-9_.,:=+<%-]+$/;
  function blocker(args: string): string {
    const toks = args
      .split(/\s+/)
      .filter(Boolean)
      .filter((t, i) => !(i === 0 && /(^|\/)nfqws2$/.test(t)) && !t.startsWith('--filter-') && !t.startsWith('--hostlist'));
    const bad = toks.find((t) => !ARG.test(t));
    if (bad) return bad.startsWith('--lua-init') ? 'содержит --lua-init (код Lua) — только вручную' : `аргумент ${bad.split('=')[0]} — только вручную`;
    if (!toks.some((t) => t.startsWith('--lua-desync='))) return 'нет --lua-desync';
    return '';
  }

  const sid = (c: StrategyCandidate) => ('gp-' + c.id).replace(/[^A-Za-z0-9_.:-]/g, '-').slice(0, 80);

  async function put(next: Strategy[], done: string) {
    busy = true;
    err = '';
    try {
      await api.setStrategies('nfqws2', next);
      msg = done;
      pending = null;
      await load();
    } catch (e) {
      err = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  function confirmApply() {
    if (!pending || !pending.protocol) return;
    const doms = Object.entries(pending.picked).filter(([, v]) => v).map(([d]) => d);
    const s: Strategy = { id: sid(pending.c), protocol: pending.protocol, domains: doms, args: pending.c.args, source: 'gp' };
    // the new one goes first (nfqws2 takes the first matching profile) and
    // its domains leave the other profiles of the same protocol
    const others = (applied ?? [])
      .filter((x) => x.id !== s.id)
      .map((x) => (x.protocol === s.protocol ? { ...x, domains: x.domains.filter((d) => !doms.includes(d)) } : x))
      .filter((x) => x.domains.length);
    void put([s, ...others], `Применено: ${doms.length} доменов, nfqws2 перезапущен.`);
  }

  function remove(id: string) {
    if (!confirm('Убрать стратегию с роутера? nfqws2 перезапустится без неё.')) return;
    void put((applied ?? []).filter((x) => x.id !== id), 'Стратегия убрана, nfqws2 перезапущен.');
  }
</script>

{#if err}<div class="banner warn">{err}</div>{/if}
{#if msg}<div class="banner acc">{msg}</div>{/if}

<section class="card">
  <div class="card-head">
    <h2>На роутере</h2>
    <span class="hint">nfqws2 · свои профили nuxk перед вашими стратегиями</span>
  </div>
  {#if applied === null}
    <p class="muted">Нет связи с nfqws2 на роутере.</p>
  {:else if !applied.length}
    <p class="hint">nuxk пока не добавлял стратегий: nfqws2 работает только на ваших собственных (NFQWS_ARGS).</p>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Протокол</th><th>Домены</th><th>Аргументы</th><th>Применена</th><th></th></tr></thead>
        <tbody>
          {#each applied as s (s.id)}
            <tr>
              <td>{PROTO_LABEL[s.protocol] ?? s.protocol}</td>
              <td title={s.domains.join('\n')}>{s.domains.slice(0, 3).join(', ')}{s.domains.length > 3 ? ` +${s.domains.length - 3}` : ''}</td>
              <td class="mono args">{s.args}</td>
              <td class="hint">{ago(s.applied_at)}</td>
              <td><button class="ghost sm" onclick={() => remove(s.id)} disabled={busy}>Убрать</button></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

{#if pending}
  <section class="card confirm">
    <div class="card-head"><h2>Применить на роутере</h2></div>
    <p class="mono args">{pending.c.args}</p>
    {#if pending.c.fragmentation_safe === false}
      <div class="banner deg">Стратегия режет пакеты на фрагменты: {pending.c.fragmentation_reason ?? 'на части сетей это ломает соединения'}.</div>
    {/if}
    <label class="field">
      Протокол
      <select bind:value={pending.protocol}>
        <option value="" disabled>выберите</option>
        <option value="tls">TLS (443/tcp)</option>
        <option value="http">HTTP (80/tcp)</option>
        <option value="quic">QUIC (443/udp)</option>
      </select>
    </label>
    <span class="hint">Домены (на них стратегия и нашлась):</span>
    <div class="doms">
      {#each Object.keys(pending.picked) as d (d)}<label class="check"><input type="checkbox" bind:checked={pending.picked[d]} /> {d}</label>{/each}
    </div>
    <p class="hint what">
      Что произойдёт: в <span class="mono">/opt/etc/nfqws2/nfqws2.conf</span> в строку
      <span class="mono">NFQWS_ARGS_CUSTOM</span> встанет отдельный профиль только для этих доменов (перед вашими стратегиями),
      сам конфиг сначала скопируется в <span class="mono">nfqws2.conf.nuxk-bak</span>. nfqws2 перезапустится — на
      1–2 секунды обход прервётся. Если nfqws2 не запустится с новой стратегией, прежний конфиг вернётся сам.
    </p>
    <div class="row">
      <button onclick={confirmApply} disabled={busy || !pending.protocol || !Object.values(pending.picked).some(Boolean)}>
        {busy ? 'Применяю…' : 'Применить'}
      </button>
      <button class="ghost" onclick={() => (pending = null)} disabled={busy}>Отмена</button>
    </div>
  </section>
{/if}

<section class="card">
  <div class="card-head">
    <h2>Найдено прогонами</h2>
    <span class="spacer"></span>
    <select bind:value={domain} aria-label="Домен">
      <option value="">все домены</option>
      {#each allDomains as d (d)}<option value={d}>{d}</option>{/each}
    </select>
    <select bind:value={proto} aria-label="Проверка">
      <option value="">все проверки</option>
      {#each allProtos as p (p)}<option value={p}>{p}</option>{/each}
    </select>
  </div>
  {#if candidates === null}
    <p class="muted">Загрузка…</p>
  {:else if !candidates.length}
    <div class="empty">Стратегий пока нет — они появятся во время прогона.</div>
  {:else}
    <div class="tbl-wrap">
      <table>
        <thead><tr><th>Проверка</th><th>Аргументы nfqws2</th><th>Домены</th><th>Семейство</th><th>Последний раз</th><th></th></tr></thead>
        <tbody>
          {#each shown as c (c.id)}
            {@const ds = domainsOf(c)}
            <tr>
              <td>{c.protocol}{#if c.fragmentation_safe === false} <span class="chip deg" title={c.fragmentation_reason ?? ''}>фрагменты</span>{/if}</td>
              <td class="mono args">{c.args}</td>
              <td title={ds.join('\n')}>{ds.slice(0, 2).join(', ')}{ds.length > 2 ? ` +${ds.length - 2}` : ''}</td>
              <td class="hint">{c.family ?? '—'}</td>
              <td class="hint">{c.last_seen_at ? new Date(c.last_seen_at).toLocaleString('ru-RU', { dateStyle: 'short', timeStyle: 'short' }) : '—'}</td>
              <td>
                {#if blocker(c.args)}
                  <span class="hint" title="nuxk пишет на роутер только аргументы десинка без файлов и кода">{blocker(c.args)}</span>
                {:else}
                  <button class="ghost sm" onclick={() => startApply(c)} disabled={busy || !ds.length}>На роутер…</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<style>
  h2 {
    font-size: 15px;
  }
  .args {
    font-size: 11.5px;
    word-break: break-all;
    max-width: 460px;
  }
  .confirm {
    border-color: color-mix(in srgb, var(--accent) 45%, var(--line));
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .doms {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 14px;
  }
  .check {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
  }
  .what {
    max-width: 760px;
  }
  select {
    max-width: 220px;
  }
</style>
