<script lang="ts">
  import { api, type WarpTransportStatus } from '../api';
  import { refresh, status } from '../status.svelte';
  import { fmtDur, healthChip, HEALTH_LABEL, planeOf, probeState, probeText } from '../ui';

  let { go }: { go: (tab: string) => void } = $props();
  type Transport = WarpTransportStatus['transport'];
  type Operation = 'switch' | 'profile' | 'engine';
  const e = $derived(status.data?.engines.find((engine) => engine.kind === 'usque'));
  const plane = $derived(planeOf(status.data));
  const domains = $derived([...new Set((plane?.lists ?? []).filter((list) => list.mode === 'warp').flatMap((list) => list.domains))]);
  let warp = $state<WarpTransportStatus | null>(null);
  let operation = $state<Operation | null>(null);
  let connectionNote = $state('');
  let error = $state('');
  let importOpen = $state(false);
  let mode = $state<'file' | 'text'>('file');
  let file = $state<File>();
  let config = $state('');
  let profileError = $state('');
  let profileSaved = $state(false);
  let requestNumber = 0;
  const selected = $derived(warp?.transport ?? (e?.detail?.transport === 'amneziawg' ? 'amneziawg' : 'usque'));
  const locked = $derived(operation !== null || warp?.pending === true);
  const canImport = $derived(warp?.available === true && !warp.native_interface && selected === 'usque');
  const awgReady = $derived(warp?.available === true && warp.awg_installed !== false && warp.profile_configured !== false);

  $effect(() => {
    let mounted = true;
    async function poll() {
      if (operation) return;
      const id = ++requestNumber;
      try {
        const result = await api.warpTransport();
        if (!mounted || id !== requestNumber) return;
        const changed = warp?.pending && !result.pending;
        warp = result;
        if (changed) await refresh();
      } catch {
        // Older agents can still manage their USQUE engine on this page.
      }
    }
    const timer = setInterval(poll, 2000);
    queueMicrotask(poll);
    return () => { mounted = false; clearInterval(timer); };
  });

  async function change(target: Transport) {
    ++requestNumber;
    operation = 'switch';
    error = '';
    connectionNote = '';
    try {
      const next = await api.setWarpTransport(target);
      warp = warp ? { ...warp, ...next, error: next.error } : next;
      clearImport();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      operation = null;
    }
  }

  async function act(action: 'start' | 'stop' | 'restart' | 'probe') {
    operation = 'engine';
    error = '';
    connectionNote = '';
    try {
      if (action === 'probe') {
        const result = await api.engineProbe('usque');
        const message = probeText({ probe: result });
        if (probeState(result) === 'ok') connectionNote = message;
        else error = message;
      } else {
        await api.engineAction('usque', action);
      }
      await refresh();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      operation = null;
    }
  }

  function clearImport() {
    config = '';
    file = undefined;
    profileError = '';
    importOpen = false;
  }

  function chooseMode(next: typeof mode) {
    if (mode === next) return;
    mode = next;
    config = '';
    file = undefined;
    profileError = '';
  }

  async function saveProfile(event: SubmitEvent) {
    event.preventDefault();
    if (locked || !canImport) return;
    ++requestNumber;
    operation = 'profile';
    profileError = '';
    try {
      if (mode === 'file' && (!file || !/\.conf$/i.test(file.name))) {
        throw new Error('Выберите файл с расширением .conf.');
      }
      if (file && file.size > 32768) throw new Error('Файл больше 32 КиБ.');
      const text = mode === 'file' && file ? await file.text() : config;
      if (!text.trim()) throw new Error('Добавьте текст конфигурации.');
      if (new TextEncoder().encode(text).length > 32768) throw new Error('Профиль больше 32 КиБ.');
      await api.setWarpProfile(text);
      if (warp) warp = { ...warp, profile_configured: true };
      profileSaved = true;
      clearImport();
    } catch (err) {
      profileError = err instanceof Error ? err.message : String(err);
    } finally {
      operation = null;
    }
  }
</script>

{#snippet currentControls()}
  <div class="row warp-actions">
    {#if e?.running}
      <button type="button" class="ghost" onclick={() => act('probe')} disabled={locked}>Проверить</button>
      <button type="button" class="ghost" onclick={() => act('restart')} disabled={locked}>Перезапустить</button>
      <button type="button" class="ghost" onclick={() => act('stop')} disabled={locked}>Остановить</button>
    {:else}
      <button type="button" onclick={() => act('start')} disabled={locked || !e}>Запустить</button>
    {/if}
  </div>
{/snippet}

{#snippet transportState(transport: Transport)}
  {#if selected === transport && e}
    <span class="chip {healthChip(e)}">{HEALTH_LABEL[e.health]}</span>
  {:else if warp?.available}
    <span class="chip">Резервный</span>
  {/if}
{/snippet}

<div class="grid g2 warp-grid">
  <section class="card transport">
    <div class="card-head">
      <h2>USQUE / MASQUE</h2>
      {@render transportState('usque')}
      <span class="spacer"></span>
      {#if selected === 'usque' && e?.version}<span class="mono muted small">{e.version}</span>{/if}
    </div>
    <dl class="kv">
      <dt>Интерфейс</dt>
      <dd class="mono">{(selected === 'usque' ? e?.iface : undefined) || '—'}</dd>
      {#if selected === 'usque' && e}
        <dt>Аптайм</dt><dd>{e.running && e.uptime_sec ? fmtDur(e.uptime_sec) : '—'}</dd>
        <dt>SNI</dt><dd class="mono">{e.detail?.sni || '—'}</dd>
        <dt>HTTP/2</dt><dd>{e.detail?.http2 === '1' ? 'Вкл.' : 'Выкл.'}</dd>
      {/if}
    </dl>
    {#if selected === 'usque' && e}
      {@render currentControls()}
    {:else if selected === 'amneziawg'}
      <div class="row warp-actions"><button type="button" class="ghost" onclick={() => change('usque')} disabled={locked || !warp?.available}>Переключить на USQUE</button></div>
    {:else if !e}
      <p class="hint">Статус USQUE пока недоступен.</p>
    {/if}
  </section>

  <section class="card transport">
    <div class="card-head">
      <h2>{warp?.native_interface ? "AmneziaWG · Keenetic" : "AmneziaWG"}</h2>
      {#if warp?.awg_installed !== undefined}<span class="chip {warp.awg_installed ? 'ok' : ''}">{warp.awg_installed ? 'Установлен' : 'Не установлен'}</span>{/if}
      {@render transportState('amneziawg')}
    </div>
    <dl class="kv">
      <dt>Интерфейс</dt><dd class="mono">{warp?.native_interface ?? "awg0"}</dd>
      <dt>Профиль</dt><dd class="mono">{warp?.native_interface ? "Настраивается в KeeneticOS" : warp?.profile_configured ? 'warp-awg.conf' : warp?.available && warp.profile_configured === false ? 'Не добавлен' : '—'}</dd>
    </dl>
    <div class="row warp-actions">
      {#if !warp?.native_interface}
      <button type="button" disabled={locked || !canImport} aria-expanded={importOpen} aria-controls="warp-profile-import"
        onclick={() => {
          if (importOpen) clearImport();
          else { importOpen = true; profileSaved = false; }
        }}>Импорт .conf</button>
      {/if}
      {#if selected !== 'amneziawg'}
        <button type="button" class="ghost" disabled={locked || !awgReady} onclick={() => change('amneziawg')}>Переключить</button>
      {/if}
    </div>
    {#if selected === 'amneziawg'}
      {@render currentControls()}
      {#if !warp?.native_interface}<p class="hint">Для замены профиля сначала выберите USQUE.</p>{/if}
    {:else if warp && !warp.available}
      <p class="hint">На этом узле AmneziaWG ещё не настроен.</p>
    {:else if !warp}
      <p class="hint">Настройки AmneziaWG недоступны. Проверьте версию агента.</p>
    {/if}
    {#if profileSaved}<p class="ok-text" role="status">Профиль сохранён.</p>{/if}

    <div id="warp-profile-import" hidden={!importOpen}>
      {#if importOpen}
        <form class="import" onsubmit={saveProfile} aria-label="Импорт профиля WG / AWG">
          <h3>Импорт конфигурации WG / AWG</h3>
          <div class="row">
            <button type="button" class="ghost" aria-pressed={mode === 'file'} onclick={() => chooseMode('file')} disabled={locked}>Выбрать файл</button>
            <button type="button" class="ghost" aria-pressed={mode === 'text'} onclick={() => chooseMode('text')} disabled={locked}>Вставить текст</button>
          </div>
          {#if mode === 'file'}
            <label for="warp-config-file">Файл .conf</label>
            <input id="warp-config-file" type="file" accept=".conf,text/plain" disabled={locked}
              onchange={(event) => { file = event.currentTarget.files?.[0]; profileError = ''; }} />
          {:else}
            <label for="warp-config-text">Конфигурация WG / AWG</label>
            <textarea id="warp-config-text" class="mono" rows="6" maxlength="32768" spellcheck="false" autocomplete="off"
              placeholder="[Interface]" bind:value={config} disabled={locked}></textarea>
          {/if}
          <div class="row">
            <button type="submit" disabled={locked || !canImport || (mode === 'file' ? !file : !config.trim())}>{operation === 'profile' ? 'Сохранение…' : 'Проверить и сохранить'}</button>
            <button type="button" class="ghost" disabled={locked} onclick={clearImport}>Отмена</button>
          </div>
          {#if profileError}<p class="err-text" role="alert">{profileError}</p>{/if}
          <p class="hint">Профиль хранится на роутере. Переключение выполняется отдельной кнопкой.</p>
        </form>
      {/if}
    </div>
  </section>
</div>

{#if warp?.pending || operation === 'switch'}
  <p class="hint" role="status">Переключение и проверка WARP…</p>
{:else if error || e?.last_error}
  <p class="err-text" role="alert">{error || e?.last_error}</p>
{:else if connectionNote}
  <p class="ok-text" role="status">{connectionNote}</p>
{/if}

{#if warp?.error && !warp.pending && operation !== 'switch'}
  <p class="err-text" role="alert">
    Не удалось переключиться на {warp.transport === 'amneziawg' ? 'USQUE / MASQUE' : 'AmneziaWG'}. {warp.error}
  </p>
{/if}

<section class="card warp-domains">
  <div class="card-head">
    <h2>Домены через WARP</h2>
    {#if plane}<span class="chip">{domains.length}</span>{/if}
    <span class="spacer"></span>
    <button class="ghost sm" type="button" onclick={() => go('lists')}>Списки →</button>
  </div>
  {#if !plane}<p class="hint">Маршрутизация WARP недоступна.</p>
  {:else if domains.length}
    <div class="row">{#each domains as domain (domain)}<span class="chip mono domain">{domain}</span>{/each}</div>
  {:else}<p class="hint">Список WARP пуст.</p>{/if}
</section>

<style>
  .warp-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .transport { min-width: 0; }
  .transport :is(p, .import) { margin-top: 10px; }
  .warp-actions { margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--line); }
  .import { display: grid; gap: 8px; border: 1px solid var(--line); border-radius: 8px; padding: 12px; }
  .import h3, .import p { margin: 0; }
  .import textarea { width: 100%; resize: vertical; }
  .import input { max-width: 100%; }
  .import button[aria-pressed="true"] { color: var(--accent); border-color: var(--accent); }
  .warp-domains { margin-top: 12px; }
  .domain { max-width: 100%; overflow-wrap: anywhere; white-space: normal; }
  @media (max-width: 880px) { .warp-grid { grid-template-columns: minmax(0, 1fr); } }
</style>
