<script lang="ts">
  // One site: what's wrong with it, the way out nuxk would take and how sure
  // it is, and — once accepted — what the check afterwards found. Only
  // advises: the buttons are the person's decision.
  import type { ListMode, Suggestion } from '../api';
  import { ago, modeTitle } from '../ui';
  import PathTable from './PathTable.svelte';
  import SearchAdvice from './SearchAdvice.svelte';
  import { SOURCE_TITLE, ahead, verdictOf } from './text';

  let {
    s,
    canAccept,
    pending = false,
    onaccept,
    ondismiss,
  }: {
    s: Suggestion;
    canAccept: boolean;
    pending?: boolean;
    onaccept: (mode: ListMode) => void;
    ondismiss: () => void;
  } = $props();

  let open = $state(false);
  const v = $derived(verdictOf(s));
  const paths = $derived(s.paths ?? []);
  const acceptTitle = $derived(canAccept ? 'Добавить домен в список «Подбор»' : 'Маршрутизация выключена — принять нельзя');
</script>

<article class="item">
  <header class="head">
    <span class="dom mono">{s.domain}</span>
    {#if s.state === 'checking'}
      <span class="chip acc"><span class="dot ok pulse"></span>проверяется…</span>
    {:else}
      <span class="chip {v.cls}">{v.label}</span>
    {/if}
    <span class="chip src">{SOURCE_TITLE[s.source] ?? s.source}</span>
    <span class="spacer"></span>
    {#if s.checked_at && s.state !== 'checking'}<span class="hint">проверено {ago(s.checked_at)}</span>{/if}
  </header>

  {#if s.state === 'checking'}
    <p class="hint">Захожу на сайт напрямую, потом — если нужно — через туннели, по несколько попыток. Это до пары минут.</p>
  {:else}
    {#if s.reason}<p class="reason">{s.reason}</p>{/if}

    {#if s.state === 'accepted' && s.accepted}
      <div class="rec">
        <span class="lbl">Принято</span>
        <span class="mode {s.accepted}"><b>{modeTitle(s.accepted)}</b></span>
        <span class="hint">список «Подбор: {modeTitle(s.accepted)}»{s.accepted_at ? ` · ${ago(s.accepted_at)}` : ''}</span>
      </div>
      <div class="rec">
        <span class="lbl">Проверка</span>
        {#if s.verified_at}
          <span class="chip {s.verified ? 'ok' : 'warn'}">проверено {ago(s.verified_at)}: {s.verified ? 'работает' : 'не работает'}</span>
        {:else}
          <span class="chip">ещё не проверено</span>
        {/if}
        {#if s.verify_due}<span class="hint">следующая — {ahead(s.verify_due)}</span>{/if}
      </div>
      {#if s.verify_note}<p class="note">{s.verify_note}</p>{/if}
    {:else if s.mode}
      <div class="rec">
        <span class="lbl">Решение</span>
        <span class="mode {s.mode}"><b>{modeTitle(s.mode)}</b></span>
        {#if s.confirmed}
          <span class="chip ok">проверено: открывается</span>
        {:else}
          <span class="chip">не проверено</span>
          {#if s.mode === 'desync'}<span class="hint">DPI можно проверить только после принятия</span>{/if}
        {/if}
      </div>
      {#if s.alt}
        <div class="rec">
          <span class="lbl">Можно и так</span>
          <span class="mode {s.alt}"><b>{modeTitle(s.alt)}</b></span>
          <span class="hint">проверено: открывается</span>
        </div>
      {/if}
    {:else if s.state === 'ready'}
      <p class="hint">{s.verdict === 'ok' ? 'Ничего делать не нужно.' : 'Маршрут тут не поможет — принимать нечего.'}</p>
    {/if}

    {#if s.next === 'search'}
      <SearchAdvice domains={[s.domain]} interim={s.interim} />
    {/if}

    <div class="row actions">
      {#if s.state === 'ready'}
        {#if s.mode}
          <button class="sm" onclick={() => onaccept(s.mode as ListMode)} disabled={!canAccept || pending} title={acceptTitle}>
            Принять · {modeTitle(s.mode)}
          </button>
        {/if}
        {#if s.alt}
          <button class="ghost sm" onclick={() => onaccept(s.alt as ListMode)} disabled={!canAccept || pending} title={acceptTitle}>
            {modeTitle(s.alt)} вместо
          </button>
        {/if}
        <button class="ghost sm" onclick={ondismiss} disabled={pending} aria-label="Скрыть предложение для {s.domain}">Скрыть</button>
      {:else if s.state === 'accepted'}
        <button
          class="ghost sm"
          onclick={ondismiss}
          disabled={pending}
          title="Домен останется в списке «Подбор», nuxk перестанет его проверять"
          aria-label="Убрать {s.domain} из «Решений»"
        >
          Убрать из «Решений»
        </button>
      {/if}
      {#if s.verdict === 'dns-block'}<a class="lnk" href="#dns">Открыть «DNS»</a>{/if}
      <span class="spacer"></span>
      {#if paths.length}
        <button class="linkbtn" onclick={() => (open = !open)} aria-expanded={open}>
          {open ? 'скрыть замеры' : `замеры по путям (${paths.length})`}
        </button>
      {/if}
    </div>

    {#if open && paths.length}
      <div class="paths">
        <PathTable {paths} />
      </div>
    {/if}
  {/if}
</article>

<style>
  .item {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 14px 0;
    border-bottom: 1px solid var(--line);
    min-width: 0;
  }
  .item:first-child {
    padding-top: 4px;
  }
  .item:last-child {
    border-bottom: 0;
    padding-bottom: 4px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .dom {
    font-size: 14px;
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .src {
    background: none;
    border: 1px solid var(--line);
    color: var(--muted);
  }
  .reason {
    margin: 0;
    max-width: 760px;
    color: var(--ink-2);
    font-size: 13px;
  }
  .rec {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    font-size: 13px;
  }
  .lbl {
    min-width: 92px;
    color: var(--muted);
    font-size: 12px;
  }
  .note {
    margin: 0;
    max-width: 760px;
    font-size: 12.5px;
    color: var(--ink-2);
    padding-left: 10px;
    border-left: 2px solid var(--line);
  }
  .actions {
    margin-top: 2px;
  }
  .lnk {
    font-size: 12px;
    color: var(--accent);
  }
  .paths {
    margin-top: 2px;
  }
  @media (max-width: 480px) {
    .lbl {
      min-width: 0;
      width: 100%;
    }
  }
</style>
