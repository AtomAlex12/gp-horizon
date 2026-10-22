<script lang="ts">
  // What counts as "good enough" for a way out, the reference site the limits
  // follow, and discovery from the DNS log. The agent keeps ms and kbit/s;
  // people read seconds and Mbit/s, so the form converts both ways.
  import { api, type SuggestReference, type SuggestSettings } from '../api';
  import { ago } from '../ui';
  import { mbit, msText, secText } from './text';

  let {
    settings,
    reference,
    discovering,
    onsaved,
  }: {
    settings: SuggestSettings;
    reference: SuggestReference;
    discovering: boolean;
    onsaved: (s: SuggestSettings) => void;
  } = $props();

  let open = $state(false);
  let saving = $state(false);
  // shown next to what it is about: the thresholds' Save or the discovery switch
  let msg = $state<{ ok: boolean; text: string; at: 'form' | 'discover' } | null>(null);

  // the form keeps text (a comma is a decimal point here); untouched fields follow the agent
  type Field = 'ttfb' | 'factor' | 'speed' | 'share' | 'ref';
  const num = (v: number) => v.toLocaleString('ru-RU', { maximumFractionDigits: 2, useGrouping: false });
  const fromAgent = $derived<Record<Field, string>>({
    ttfb: secText(settings.ttfb_max_ms),
    factor: num(settings.ttfb_factor),
    speed: num(settings.kbps_min / 1000),
    share: String(settings.kbps_share),
    ref: settings.reference,
  });
  let edits = $state<Partial<Record<Field, string>>>({});
  const val = (f: Field) => edits[f] ?? fromAgent[f];
  const dirty = $derived(Object.keys(edits).some((f) => edits[f as Field] !== fromAgent[f as Field]));

  const parse = (t: string) => {
    const v = Number(t.trim().replace(/\s/g, '').replace(',', '.'));
    return t.trim() !== '' && Number.isFinite(v) ? v : NaN;
  };

  async function put(body: Partial<SuggestSettings>, done: string, at: 'form' | 'discover') {
    saving = true;
    msg = null;
    try {
      onsaved(await api.suggestSettings(body));
      msg = { ok: true, text: done, at };
      return true;
    } catch (e) {
      // 400 bad_settings: the agent's own words
      msg = { ok: false, text: e instanceof Error ? e.message : String(e), at };
      return false;
    } finally {
      saving = false;
    }
  }

  async function save() {
    const ttfb = parse(val('ttfb')),
      factor = parse(val('factor')),
      speed = parse(val('speed')),
      share = parse(val('share'));
    const fields: [number, string][] = [
      [ttfb, 'первый байт'],
      [factor, '× эталона'],
      [speed, 'скорость'],
      [share, '% от эталона'],
    ];
    const bad = fields.find(([v]) => Number.isNaN(v));
    if (bad) {
      msg = { ok: false, text: `Введите число в поле «${bad[1]}».`, at: 'form' };
      return;
    }
    // only what was changed: an untouched field keeps the agent's exact value
    // (the form shows it rounded)
    const changed = (f: Field) => f in edits && edits[f] !== fromAgent[f];
    const body: Partial<SuggestSettings> = {};
    if (changed('ttfb')) body.ttfb_max_ms = Math.round(ttfb * 1000);
    if (changed('factor')) body.ttfb_factor = factor;
    if (changed('speed')) body.kbps_min = Math.round(speed * 1000);
    if (changed('share')) body.kbps_share = Math.round(share);
    if (changed('ref')) body.reference = val('ref').trim();
    const ok = await put(
      body,
      'Сохранено. Новые пороги действуют со следующей проверки.',
      'form',
    );
    if (ok) edits = {};
  }

  async function toggleDiscover(e: Event & { currentTarget: HTMLInputElement }) {
    const box = e.currentTarget;
    const on = !settings.discover;
    // not saved: the box goes back to what the agent has
    if (!(await put({ discover: on }, on ? 'Поиск по DNS включён.' : 'Поиск по DNS выключен.', 'discover')))
      box.checked = settings.discover;
  }

  // what the limits come to right now, from the settings and the reference's last measurement
  const now = $derived.by(() => {
    if (!reference.ok || !reference.at) return null;
    const ttfb = reference.ttfb_ms
      ? Math.min(settings.ttfb_max_ms, Math.round(settings.ttfb_factor * reference.ttfb_ms) + 500)
      : settings.ttfb_max_ms;
    // the share of the reference's speed: only for a site about as near as the reference
    // (the agent applies it only when the reference's TLS handshake time is known)
    const near = reference.kbps && reference.tls_ms ? Math.round((reference.kbps * settings.kbps_share) / 100) : 0;
    return { ttfb, near: near > settings.kbps_min ? near : 0, nearMs: 2 * (reference.tls_ms ?? 0) + 20 };
  });
</script>

<section class="card">
  <div class="card-head">
    <h2>
      <button class="linkbtn head-btn" onclick={() => (open = !open)} aria-expanded={open} aria-controls="suggest-settings">
        Настройки проверки
        <span class="caret" class:on={open} aria-hidden="true">▸</span>
      </button>
    </h2>
    <span class="spacer"></span>
    {#if !open}
      <span class="hint">
        первый байт ≤ {secText(settings.ttfb_max_ms)} с · {settings.kbps_min
          ? `скорость ≥ ${num(settings.kbps_min / 1000)} Мбит/с`
          : settings.kbps_share
            ? `скорость ≥ ${settings.kbps_share} % эталона`
            : 'скорость не ограничена'} · эталон <span class="mono">{settings.reference}</span>
      </span>
    {/if}
  </div>

  {#if open}
    <div id="suggest-settings" class="stack">
      <p class="hint">
        Путь годится, если сайт открылся хотя бы 4 раза из 5, не замирает, и первый байт и скорость — в пределах ниже.
        Скорость сравнивается, только когда её удалось измерить (на маленькой странице — нет), а доля от скорости эталона —
        только для сайтов примерно так же близких, как эталон: скорость короткой загрузки зависит от расстояния. Берётся
        первый подходящий путь по порядку (напрямую → DPI → WARP → VLESS), а не самый быстрый.
      </p>

      <div class="fields">
        <label class="field">
          <span>Первый байт — не дольше</span>
          <span class="inp">
            <input type="text" inputmode="decimal" value={val('ttfb')} oninput={(e) => (edits.ttfb = e.currentTarget.value)} />
            <span>с</span>
          </span>
          <small>всегда; от 0,5 до 30 с</small>
        </label>
        <label class="field">
          <span>…и не хуже, чем</span>
          <span class="inp">
            <input type="text" inputmode="decimal" value={val('factor')} oninput={(e) => (edits.factor = e.currentTarget.value)} />
            <span>× эталона</span>
          </span>
          <small>плюс 0,5 с запаса; от 1 до 20</small>
        </label>
        <label class="field">
          <span>Скорость — не ниже</span>
          <span class="inp">
            <input type="text" inputmode="decimal" value={val('speed')} oninput={(e) => (edits.speed = e.currentTarget.value)} />
            <span>Мбит/с</span>
          </span>
          <small>0 — без нижней границы; до 1000</small>
        </label>
        <label class="field">
          <span>…и не ниже</span>
          <span class="inp">
            <input type="text" inputmode="numeric" value={val('share')} oninput={(e) => (edits.share = e.currentTarget.value)} />
            <span>% от эталона</span>
          </span>
          <small>от 0 до 100; только для близких сайтов</small>
        </label>
        <label class="field wide">
          <span>Эталон — сайт, который открывается хорошо</span>
          <input type="text" value={val('ref')} oninput={(e) => (edits.ref = e.currentTarget.value)} placeholder="ya.ru" />
          <small>
            {#if !reference.at}
              ещё не измерялся — измерится при следующей проверке сайта
            {:else if !reference.ok}
              <b class="degt">эталон не открылся напрямую — действуют только абсолютные пороги</b> · {ago(reference.at)}
            {:else}
              <span class="mono">{reference.domain}</span> напрямую: первый байт {msText(reference.ttfb_ms)}, скорость {mbit(reference.kbps)}
              · {ago(reference.at)}
            {/if}
          </small>
        </label>
      </div>

      {#if now}
        <p class="hint">
          С этим эталоном сейчас: первый байт — не дольше {msText(now.ttfb)}, скорость — {settings.kbps_min
            ? `не ниже ${mbit(settings.kbps_min)}`
            : 'без нижней границы'}{now.near
            ? `, у сайтов так же близких, как эталон (соединение устанавливается не дольше ${now.nearMs} мс), — не ниже ${mbit(now.near)}`
            : ''}.
        </p>
      {/if}

      <div class="row">
        <button onclick={save} disabled={saving || !dirty}>{saving ? 'Сохраняю…' : 'Сохранить'}</button>
        {#if dirty}<button class="ghost" onclick={() => ((edits = {}), (msg = null))} disabled={saving}>Отменить</button>{/if}
      </div>
      {#if msg?.at === 'form'}<p class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</p>{/if}

      <div class="divider"></div>

      <label class="check">
        <input type="checkbox" checked={settings.discover} disabled={saving} onchange={toggleDiscover} />
        <span>
          <b>Находить сайты по журналу DNS</b>
          <small>
            nuxk сам проверяет имена, о которых роутер часто спрашивает, и показывает только те, с которыми что-то не так.
            {#if settings.discover && !discovering}
              Сейчас не работает: нужен защищённый DNS — включите его в <a href="#dns">«DNS»</a>.
            {/if}
          </small>
        </span>
      </label>
      {#if msg?.at === 'discover'}<p class={msg.ok ? 'ok-text' : 'err-text'}>{msg.text}</p>{/if}
    </div>
  {/if}
</section>

<style>
  .head-btn {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    font: inherit;
    color: var(--ink);
    text-decoration: none;
  }
  .caret {
    color: var(--muted);
    font-size: 12px;
    transition: transform 0.15s;
  }
  .caret.on {
    transform: rotate(90deg);
  }
  /* pairs side by side: the absolute limit, then the one relative to the reference */
  .fields {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px 16px;
    max-width: 640px;
  }
  .wide {
    grid-column: 1 / -1;
  }
  .field > span:first-child {
    color: var(--ink-2);
  }
  .field small {
    color: var(--muted);
    font-size: 11.5px;
  }
  .inp {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--ink-2);
    white-space: nowrap;
  }
  .inp input {
    width: 90px;
    flex: none;
    font-variant-numeric: tabular-nums;
  }
  .wide input {
    max-width: 320px;
  }
  .degt {
    color: var(--degraded);
    font-weight: 500;
  }
  .check {
    display: flex;
    gap: 10px;
    align-items: flex-start;
    font-size: 13px;
    cursor: pointer;
  }
  .check input {
    margin-top: 3px;
  }
  .check > span {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .check small {
    color: var(--muted);
    font-size: 12px;
  }
  .check a {
    color: var(--accent);
  }
  @media (max-width: 520px) {
    .fields {
      grid-template-columns: 1fr;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .caret {
      transition: none;
    }
  }
</style>
