<script lang="ts">
  // «Списки»: three ways out — DPI (nfqws2), WARP (usque), VLESS (xray).
  // Edits the plane's desired lists; the router side (firmware groups, routes,
  // the nfqws2 hostlist) is shown as the plane reports it.
  import { api, type ListMode, type OnDown, type PlaneDesired, type PlaneList } from '../api';
  import { status, refresh } from '../status.svelte';
  import { MODES, planeOf, splitDomains, ago, OP_LABEL, modeTitle, healthDot } from '../ui';

  const plane = $derived(planeOf(status.data));
  const engines = $derived(new Map((status.data?.engines ?? []).map((e) => [e.kind, e])));

  // editable copy: domains kept as text so a paste isn't reformatted mid-edit
  type Draft = { id: number; name: string; mode: ListMode; text: string; source?: string };
  let seq = 0;
  let drafts = $state<Draft[]>([]);
  let onDown = $state<OnDown>('direct');
  let loaded = $state(false);
  let dirty = $state(false);
  let saving = $state(false);
  let msg = $state<{ ok: boolean; text: string } | null>(null);

  function toDrafts(d: PlaneDesired) {
    drafts = (d.lists ?? []).map((l) => ({ id: ++seq, name: l.name, mode: l.mode, text: l.domains.join('\n'), source: l.source }));
    onDown = d.on_down ?? 'direct';
    dirty = false;
  }

  async function load() {
    try {
      toDrafts(await api.planeLists());
      loaded = true;
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    }
  }

  $effect(() => {
    if (plane && !loaded) void load();
  });

  function add(mode: ListMode) {
    const n = drafts.filter((d) => d.mode === mode).length;
    drafts.push({ id: ++seq, name: n ? `${modeTitle(mode)} ${n + 1}` : modeTitle(mode), mode, text: '', source: 'manual' });
    dirty = true;
  }

  function remove(i: number) {
    drafts.splice(i, 1);
    dirty = true;
  }

  function desired(): PlaneDesired {
    const lists: PlaneList[] = [];
    for (const d of drafts) {
      const domains = splitDomains(d.text);
      if (domains.length) lists.push({ name: d.name.trim() || modeTitle(d.mode), mode: d.mode, domains, source: d.source });
    }
    return { lists, on_down: onDown };
  }

  async function save() {
    saving = true;
    msg = null;
    try {
      const d = desired();
      await api.setPlaneLists(d);
      toDrafts(d);
      msg = { ok: true, text: 'Сохранено. nuxk пересчитает план за пару секунд.' };
      setTimeout(refresh, 1500);
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    } finally {
      saving = false;
    }
  }

  async function importGroup(group: string, mode: ListMode) {
    msg = null;
    try {
      toDrafts(await api.planeImport([group], mode));
      msg = { ok: true, text: `«${group}» скопирован в ${modeTitle(mode)}. Пока домены есть и в старом списке, они в «Конфликтах».` };
      setTimeout(refresh, 1500);
    } catch (e) {
      msg = { ok: false, text: e instanceof Error ? e.message : String(e) };
    }
  }

  function setOnDown(v: OnDown) {
    if (onDown !== v) {
      onDown = v;
      dirty = true;
    }
  }

  const count = (mode: ListMode) =>
    drafts.filter((d) => d.mode === mode).reduce((n, d) => n + splitDomains(d.text).length, 0);
  const group = (mode: ListMode) => plane?.groups?.find((g) => g.mode === mode);
  const conflicts = $derived(plane?.conflicts ?? []);
  const pending = $derived(plane?.pending ?? []);
  const imported = (g: string) => drafts.some((d) => d.source === `imported:${g}`);
</script>

{#if !status.data}
  <p class="muted">Загрузка…</p>
{:else if !plane}
  <div class="banner deg">
    <div>
      <b>Плоскость маршрутизации выключена.</b>
      Списки работают через встроенную в KeeneticOS маршрутизацию по доменам. Включите её в
      <span class="mono">/opt/etc/nuxk/nuxk.conf</span>: <span class="mono">PLANE="keenetic"</span>, затем
      <span class="mono">/opt/etc/init.d/S99nuxk-core restart</span>.
    </div>
  </div>
{:else}
  {#if !plane.apply}
    <div class="banner deg">
      <div>
        <b>Режим плана.</b> nuxk показывает, что сделал бы, но роутер не меняет. Когда план устроит — поставьте
        <span class="mono">PLANE_APPLY="1"</span> в <span class="mono">/opt/etc/nuxk/nuxk.conf</span> и перезапустите
        <span class="mono">S99nuxk-core</span>. nuxk трогает только свои объекты <span class="mono">nuxk-*</span>.
      </div>
    </div>
  {/if}

  <section class="card">
    <div class="card-head">
      <h2>Куда идёт трафик</h2>
      <span class="spacer"></span>
      <span class="hint">Туннель упал:</span>
      <div class="seg" role="group" aria-label="Если туннель упал">
        <button class:on={onDown === 'direct'} onclick={() => setOnDown('direct')}>напрямую</button>
        <button class:on={onDown === 'block'} onclick={() => setOnDown('block')}>блокировать</button>
      </div>
    </div>
    <p class="hint">
      Домен — одна строка; поддомены покрываются сами (<span class="mono">youtube.com</span> включает
      <span class="mono">www.youtube.com</span>). Можно вставить список целиком: ссылки, запятые и
      <span class="mono">*.</span> убираются. «Блокировать» — пока туннель лежит, сайты из WARP/VLESS не открываются,
      зато ничего не уходит мимо туннеля.
    </p>
  </section>

  <div class="grid g3 modes">
    {#each MODES as m (m.mode)}
      {@const e = engines.get(m.engine)}
      {@const g = group(m.mode)}
      <section class="card mode-card" style="--c: var(--m-{m.mode})">
        <div class="card-head">
          <span class="mode {m.mode}"><b>{m.title}</b></span>
          <span class="spacer"></span>
          <span class="chip mono">{count(m.mode)}</span>
        </div>
        <p class="hint">{m.what}</p>
        <div class="row meta">
          {#if e}
            <span class="chip"><span class="dot {healthDot(e)}"></span>{m.engine}</span>
          {:else}
            <span class="chip deg">{m.engine} не подключён</span>
          {/if}
          {#if m.mode === 'desync'}
            {#if plane.desync_ok}
              <span class="chip ok">в nfqws2</span>
            {:else if (plane.desync ?? []).length}
              <span class="chip deg">{plane.apply ? 'не передан' : 'план'}</span>
            {/if}
          {:else if plane.ifaces?.[m.mode]}
            <span class="chip mono">{plane.ifaces[m.mode]}</span>
            {#if g}<span class="chip {plane.apply && !pending.some((o) => o.group === g.name) ? 'ok' : 'deg'}">{g.name}</span>{/if}
          {/if}
        </div>

        {#each drafts as d, i (d.id)}
          {#if d.mode === m.mode}
            <div class="list">
              <div class="row">
                <input type="text" bind:value={d.name} oninput={() => (dirty = true)} aria-label="Название списка" />
                <button class="ghost sm" onclick={() => remove(i)} aria-label="Удалить список">✕</button>
              </div>
              {#if d.source && d.source !== 'manual'}<span class="hint mono">{d.source}</span>{/if}
              <textarea
                rows="7"
                bind:value={d.text}
                oninput={() => (dirty = true)}
                placeholder={m.mode === 'desync' ? 'youtube.com\ndiscord.com' : m.mode === 'warp' ? 'chatgpt.com\nopenai.com' : 'instagram.com'}
              ></textarea>
            </div>
          {/if}
        {/each}
        <button class="ghost sm add" onclick={() => add(m.mode)}>+ список</button>
        {#if m.mode === 'warp'}
          <p class="hint">cloudflareclient.com автоматически добавляется в DPI — «WARP через nfqws».</p>
        {/if}
      </section>
    {/each}
  </div>

  <div class="row savebar">
    <button onclick={save} disabled={saving || !dirty}>{saving ? 'Сохраняю…' : 'Сохранить списки'}</button>
    {#if dirty}<button class="ghost" onclick={load}>Отменить</button>{/if}
    {#if msg}<span class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</span>{/if}
  </div>

  <div class="grid g2">
    <section class="card">
      <div class="card-head">
        <h2>Роутер</h2>
        <span class="spacer"></span>
        <span class="chip {plane.apply ? 'acc' : 'deg'}">{plane.apply ? 'применяется' : 'только план'}</span>
      </div>
      <dl class="kv">
        <dt>проверено</dt><dd>{ago(plane.checked_at)}</dd>
        <dt>последнее изменение</dt><dd>{ago(plane.applied_at)}</dd>
        <dt>при падении туннеля</dt><dd>{plane.on_down === 'block' ? 'блокировать' : 'напрямую'}</dd>
        <dt>DPI-список nfqws2</dt><dd>{(plane.desync ?? []).length} доменов{plane.desync_ok ? ' · передан' : ''}</dd>
      </dl>
      {#if plane.last_error}<p class="err-text">{plane.last_error}</p>{/if}
      {#each plane.warnings ?? [] as w}<p class="hint warnline">⚠ {w}</p>{/each}

      <h3 class="sub">{plane.apply ? 'Не применено' : 'План'}</h3>
      {#if pending.length === 0}
        <p class="hint">Изменений нет — роутер совпадает со списками.</p>
      {:else}
        <div class="tbl-wrap">
          <table>
            <thead><tr><th>действие</th><th>объект</th><th>детали</th></tr></thead>
            <tbody>
              {#each pending as o}
                <tr>
                  <td>{OP_LABEL[o.kind] ?? o.kind}</td>
                  <td class="mono">{o.group ?? ''}</td>
                  <td class="mono small">
                    {#if o.interface}→ {o.interface}{o.block ? ' (без auto)' : ''}{/if}
                    {#if o.domains?.length}{o.domains.slice(0, 4).join(', ')}{o.domains.length > 4 ? ` +${o.domains.length - 4}` : ''}{/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    <section class="card">
      <div class="card-head">
        <h2>Конфликты</h2>
        <span class="spacer"></span>
        <span class="chip {conflicts.length ? 'deg' : 'ok'}">{conflicts.length}</span>
      </div>
      {#if conflicts.length === 0}
        <p class="hint">Нет: ни один домен nuxk не маршрутизируется вашими старыми списками.</p>
      {:else}
        <p class="hint">
          Эти домены ещё есть в ваших списках Keenetic с маршрутом. Пока они там, nuxk их не трогает — уберите их из
          старого списка в веб-интерфейсе роутера, и nuxk подхватит их сам.
        </p>
        <div class="tbl-wrap">
          <table>
            <thead><tr><th>домен</th><th>в списке</th><th>хочет</th></tr></thead>
            <tbody>
              {#each conflicts as c}
                <tr><td class="mono">{c.domain}</td><td class="mono">{c.user_group}</td><td class="mono">{c.group}</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  </div>

  {#if (plane.foreign ?? []).length}
    <section class="card">
      <div class="card-head"><h2>Ваши списки в Keenetic</h2></div>
      <p class="hint">
        Маршрутизация по доменам, настроенная вручную. Импорт копирует домены в список nuxk; сам список Keenetic не
        меняется.
      </p>
      <div class="tbl-wrap">
        <table>
          <thead><tr><th>список</th><th>интерфейс</th><th>доменов</th><th>импорт в</th></tr></thead>
          <tbody>
            {#each plane.foreign ?? [] as f (f.group)}
              <tr>
                <td><b>{f.description || f.group}</b> <span class="muted mono">{f.group}</span></td>
                <td class="mono">{f.interface}</td>
                <td class="num">{f.domains.length}</td>
                <td>
                  {#if imported(f.group)}
                    <span class="chip acc">импортирован</span>
                  {:else}
                    <div class="row">
                      {#each MODES as m (m.mode)}
                        <button class="ghost sm" onclick={() => importGroup(f.group, m.mode)}>{m.title}</button>
                      {/each}
                    </div>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
{/if}

<style>
  .modes {
    align-items: start;
  }
  .mode-card {
    border-top: 3px solid var(--c);
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .mode-card .card-head {
    margin-bottom: 0;
  }
  .mode b {
    font-size: 15px;
  }
  .meta {
    gap: 6px;
  }
  .list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .list input {
    flex: 1;
    font-weight: 500;
  }
  .add {
    align-self: flex-start;
  }
  .savebar {
    position: sticky;
    bottom: 0;
    padding: 10px 0;
    background: color-mix(in srgb, var(--bg) 90%, transparent);
    backdrop-filter: blur(6px);
    z-index: 2;
  }
  .sub {
    font-size: 13px;
    font-weight: 600;
    margin: 14px 0 6px;
  }
  .small {
    font-size: 11.5px;
    color: var(--muted);
  }
  .warnline {
    color: var(--degraded);
    margin-top: 6px;
  }
  dl.kv {
    margin-bottom: 6px;
  }
</style>
