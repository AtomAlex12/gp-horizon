<script lang="ts">
  // «Обновления»: this version, the newest release and what's in it, the
  // button that updates the router (its own `nuxk update` in the background),
  // how the last update went. Through the Pi's controller: «Обновить всё» —
  // the router, then the controller (the Pi's helper swaps its container).
  import {
    upd,
    running,
    checkNow,
    saveSettings,
    startUpdate,
    newer,
    notesBlocks,
    ctl,
    ctlGoing,
    updateAll,
  } from './update.svelte';
  import { node } from './status.svelte';
  import { ago } from './ui';

  const s = $derived(upd.s);
  const run = $derived(s?.run ?? null);
  const latest = $derived(s?.latest ?? null);
  const full = $derived(node.via === 'controller');
  const cs = $derived(ctl.s);
  const ctlVer = $derived(cs?.current ?? node.agent?.controller_version ?? '');
  const piBehind = $derived(full && !!latest && newer(latest.version, ctlVer));
  const behind = $derived(!!latest && (!!s?.available || piBehind));
  const all = $derived(cs?.all ?? null);
  const showAll = $derived(!!all && (all.state !== 'done' || Date.now() / 1000 - all.at < 86400));
  const ctlLog = $derived(cs?.run?.log ?? []);
  // where «Обновить всё» is: each step as a chip
  const stepChip = (which: 'router' | 'controller') => {
    if (!all) return { label: '', chip: '' };
    const order = { router: 0, controller: 1, done: 2 };
    const at = order[all.step];
    const mine = order[which];
    if (at > mine) return { label: 'готово', chip: 'ok' };
    if (at < mine) return { label: all.state === 'failed' ? 'не начато' : 'ждёт', chip: '' };
    return all.state === 'failed' ? { label: 'не вышло', chip: 'warn' } : { label: 'обновляется', chip: 'acc' };
  };

  function goAll() {
    if (!latest) return;
    const parts: string[] = [];
    if (s?.available) parts.push('роутер — его агент перезапустится, обход и туннели продолжат работать');
    if (piBehind) parts.push('контроллер на Pi — эта страница пропадёт примерно на минуту и загрузится заново');
    const ok = confirm(
      `Обновить всё до ${latest.version}:\n\n• ${parts.join('\n• ')}\n\n` +
        'Сначала роутер, потом контроллер. Файлы проверяются по подписи релиза; если новая версия не запустится, ' +
        'вернётся прежняя.',
    );
    if (ok) void updateAll(latest.version);
  }
  const notes = $derived(latest?.notes ? notesBlocks(latest.notes) : []);
  // the release page, only as GitHub's own link
  const page = $derived(latest?.url?.startsWith('https://github.com/') ? latest.url : '');
  // the last run: while it goes, when it didn't go well, and for a day after
  const showRun = $derived(!!run && (run.state !== 'done' || Date.now() / 1000 - run.at < 86400));

  const STATE: Record<string, { label: string; chip: string }> = {
    running: { label: 'идёт', chip: 'acc' },
    done: { label: 'готово', chip: 'ok' },
    failed: { label: 'не вышло', chip: 'warn' },
    rolled_back: { label: 'возвращена прежняя версия', chip: 'warn' },
    interrupted: { label: 'прервано', chip: 'warn' },
  };

  function go() {
    if (!latest) return;
    const what = full ? 'Роутер обновится' : 'nuxk на роутере обновится';
    const ok = confirm(
      `${what} до ${latest.version}.\n\n` +
        'Панель пропадёт на 10–30 секунд, пока перезапускается агент; обход и туннели продолжают работать. ' +
        'Если в новой версии другая версия xray, VLESS перезапустится.\n\n' +
        'Файлы проверяются по подписи релиза. Если новая версия не запустится, вернётся прежняя.',
    );
    if (ok) void startUpdate(latest.version);
  }

  // the output shows its last lines: where it got to, or where it broke
  function toEnd(el: HTMLElement, _log: string[]) {
    const end = () => {
      el.scrollTop = el.scrollHeight;
    };
    end();
    return { update: end };
  }

  const date = (unix?: number) =>
    unix ? new Date(unix * 1000).toLocaleDateString('ru-RU', { day: 'numeric', month: 'long', year: 'numeric' }) : '';
</script>

<section class="card">
  <div class="card-head">
    <h2>Обновления</h2>
    {#if running()}<span class="chip acc">обновляется</span>
    {:else if behind && latest}<span class="chip acc">доступна {latest.version}</span>
    {:else if s && latest}<span class="chip ok">последняя версия</span>{/if}
    <span class="spacer"></span>
    {#if s?.settings.check !== false}
      <button class="ghost sm" onclick={checkNow} disabled={!!upd.busy || running()}>{upd.busy === 'check' ? 'Проверяю…' : 'Проверить'}</button>
    {/if}
  </div>

  {#if !s}
    <p class="muted">{upd.err ? `Агент не ответил: ${upd.err}` : 'Загрузка…'}</p>
  {:else}
    <dl class="kv">
      <dt>{full ? 'роутер (nuxk-core)' : 'nuxk на роутере'}</dt><dd class="mono">{s.current}</dd>
      {#if full}<dt>Pi (nuxk-controller)</dt><dd class="mono">{ctlVer || '—'}</dd>{/if}
      <dt>последний релиз</dt>
      <dd>
        {#if latest}{#if page}<a href={page} target="_blank" rel="noopener noreferrer" class="mono">{latest.version}</a
            >{:else}<span class="mono">{latest.version}</span>{/if}{latest.prerelease ? ' · бета' : ''}{latest.published_at ? ` · ${date(latest.published_at)}` : ''}
        {:else if s.settings.check}—{:else}проверка выключена{/if}
      </dd>
      <dt>проверено</dt><dd>{s.checked_at ? ago(s.checked_at) : 'ещё нет'}</dd>
    </dl>
    {#if s.check_error}<p class="err-text">GitHub: {s.check_error}</p>{/if}

    {#if showRun && run}
      {@const st = STATE[run.state] ?? { label: run.state, chip: '' }}
      <div class="run" class:bad={st.chip === 'warn'}>
        <div class="row">
          <b>Обновление {run.from} → {run.to}</b>
          <span class="chip {st.chip}">{st.label}</span>
          <span class="spacer"></span>
          <span class="hint">{ago(run.at)}</span>
        </div>
        {#if run.state === 'running'}
          <p class="hint">{upd.restarting ? 'Агент перезапускается — панель вернётся через несколько секунд…' : run.message}</p>
        {:else if run.message}
          <p class="hint">{run.message}</p>
        {/if}
        {#if upd.reloading}<p class="hint">Панель обновилась на роутере — загружаю новую…</p>{/if}
        {#if run.log.length}
          <details open={run.state === 'failed' || run.state === 'rolled_back'}>
            <summary class="hint">вывод обновления</summary>
            <pre class="log mono" use:toEnd={run.log}>{run.log.join('\n')}</pre>
          </details>
        {/if}
      </div>
    {/if}

    {#if full && showAll && all}
      <div class="run" class:bad={all.state === 'failed'}>
        <div class="row">
          <b>Обновить всё → {all.version}</b>
          <span class="chip {all.state === 'done' ? 'ok' : all.state === 'failed' ? 'warn' : 'acc'}"
            >{all.state === 'done' ? 'готово' : all.state === 'failed' ? 'не вышло' : 'идёт'}</span
          >
          <span class="spacer"></span>
          <span class="hint">{ago(all.at)}</span>
        </div>
        <div class="row">
          <span class="chip {stepChip('router').chip}">роутер · {stepChip('router').label}</span>
          <span class="muted">→</span>
          <span class="chip {stepChip('controller').chip}">контроллер на Pi · {stepChip('controller').label}</span>
        </div>
        <p class="hint">
          {#if ctl.reloading}Контроллер обновился — загружаю новую страницу…
          {:else if ctl.restarting && all.step === 'controller'}Контроллер перезапускается — страница вернётся примерно через минуту…
          {:else}{all.message}{/if}
        </p>
        {#if all.step !== 'router' && ctlLog.length}
          <details open={all.state === 'failed'}>
            <summary class="hint">вывод обновления на Pi</summary>
            <pre class="log mono" use:toEnd={ctlLog}>{ctlLog.join('\n')}</pre>
          </details>
        {/if}
      </div>
    {/if}

    {#if behind && latest && !running() && !ctlGoing()}
      {#if notes.length}
        <div class="notes">
          {#each notes as n, i (i)}
            {#if n.kind === 'h'}<h3>{n.text}</h3>
            {:else if n.kind === 'li'}<p class="li">{n.text}</p>
            {:else}<p>{n.text}</p>{/if}
          {/each}
        </div>
      {/if}
      <div class="row top">
        {#if full}
          <button
            onclick={goAll}
            disabled={!!upd.busy || !!ctl.busy || (piBehind && !cs?.can_apply) || (s.available && !s.can_apply)}
          >
            {ctl.busy === 'all' ? 'Запускаю…' : `Обновить всё до ${latest.version}`}
          </button>
          {#if s.available}
            <button class="ghost" onclick={go} disabled={!s.can_apply || !!upd.busy || !!ctl.busy}>
              {upd.busy === 'start' ? 'Запускаю…' : 'Только роутер'}
            </button>
          {/if}
        {:else}
          <button onclick={go} disabled={!s.can_apply || !!upd.busy}>
            {upd.busy === 'start' ? 'Запускаю…' : `Обновить до ${latest.version}`}
          </button>
        {/if}
        {#if page}<a class="hint" href={page} target="_blank" rel="noopener noreferrer">что нового на GitHub</a>{/if}
      </div>
      {#if !s.can_apply && s.cannot}<p class="hint">{s.cannot}</p>{/if}
    {/if}

    {#if piBehind && latest && cs && !cs.can_apply}
      <p class="hint top">
        Контроллер на Pi ({ctlVer}) пока обновляется командой на Pi:
        <span class="mono">sh ~/nuxk/nuxk-full.sh update</span>. {cs.cannot}
      </p>
    {/if}

    {#if upd.err}<p class="err-text">{upd.err}</p>{/if}
    {#if ctl.err}<p class="err-text">{ctl.err}</p>{/if}

    <div class="settings">
      <label>
        <input
          type="checkbox"
          checked={s.settings.check}
          disabled={!!upd.busy}
          onchange={(e) => saveSettings({ ...s.settings, check: e.currentTarget.checked })}
        />
        Проверять новые версии раз в сутки
      </label>
      <label>
        <select
          value={s.settings.channel}
          disabled={!!upd.busy || !s.settings.check}
          onchange={(e) => saveSettings({ ...s.settings, channel: e.currentTarget.value as 'stable' | 'beta' })}
        >
          <option value="stable">только релизы</option>
          <option value="beta">и бета-версии</option>
        </select>
      </label>
    </div>
    <p class="hint">
      Проверка — один запрос к GitHub в сутки с роутера (GitHub видит его адрес). Файлы обновления сверяются с подписью
      релиза: её проверяет уже работающий агент, поэтому подменённый релиз не встанет.
    </p>
  {/if}
</section>

<style>
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
  .notes {
    margin-top: 12px;
    padding: 10px 12px;
    border-radius: 10px;
    background: var(--accent-soft);
    font-size: 13px;
    max-height: 320px;
    overflow: auto;
  }
  .notes h3 {
    font-size: 13px;
    margin: 8px 0 4px;
  }
  .notes h3:first-child {
    margin-top: 0;
  }
  .notes p {
    margin: 2px 0;
  }
  .notes .li {
    padding-left: 14px;
    position: relative;
  }
  .notes .li::before {
    content: '•';
    position: absolute;
    left: 2px;
    color: var(--muted);
  }
  .settings {
    display: flex;
    gap: 16px;
    flex-wrap: wrap;
    align-items: center;
    margin: 14px 0 6px;
    font-size: 13px;
  }
  .settings label {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .top {
    margin-top: 12px;
  }
  .muted {
    color: var(--muted);
  }
</style>
