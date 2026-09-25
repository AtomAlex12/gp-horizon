<script lang="ts">
  import { setToken } from '../api';
  import { status, pollNow } from '../status.svelte';
  import { planeOf } from '../ui';

  const plane = $derived(planeOf(status.data));

  function logout() {
    setToken('');
    pollNow();
  }
</script>

<div class="grid g2">
  <section class="card">
    <div class="card-head"><h2>nuxk</h2></div>
    <dl class="kv">
      <dt>nuxk-core</dt><dd class="mono">{status.data?.version ?? '—'}</dd>
      <dt>веб</dt><dd class="mono">{__APP_VERSION__}{__LITE__ ? ' · lite' : ' · full'}</dd>
      <dt>плоскость</dt><dd class="mono">{plane ? plane.backend : 'выключена'}</dd>
      {#if plane}
        <dt>режим</dt><dd>{plane.apply ? 'применяет (PLANE_APPLY=1)' : 'только план (PLANE_APPLY=0)'}</dd>
        <dt>интерфейс WARP</dt><dd class="mono">{plane.ifaces?.warp || '—'}</dd>
        <dt>интерфейс VLESS</dt><dd class="mono">{plane.ifaces?.vless || '—'}</dd>
      {/if}
    </dl>
  </section>

  <section class="card stack">
    <div class="card-head"><h2>Настройки</h2></div>
    <p class="hint">
      Настройки демона — в <span class="mono">/opt/etc/nuxk/nuxk.conf</span> на роутере (после правки:
      <span class="mono">/opt/etc/init.d/S99nuxk-core restart</span>). Главные:
    </p>
    <dl class="kv keys">
      <dt class="mono">PLANE_APPLY</dt><dd>0 — только план, 1 — менять роутер</dd>
      <dt class="mono">PLANE_V6</dt><dd>deny — не пускать IPv6 к доменам из списков мимо туннеля</dd>
      <dt class="mono">PLANE_IFACE_WARP</dt><dd>туннель usque (обычно OpkgTun0)</dd>
      <dt class="mono">ENGINE_*</dt><dd>пусто — движок выключен</dd>
    </dl>
    <div class="row">
      <button class="ghost" onclick={logout}>Выйти (забыть токен)</button>
    </div>
  </section>
</div>

<style>
  .keys dd {
    text-align: left;
    color: var(--muted);
  }
</style>
