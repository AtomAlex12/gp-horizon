<script lang="ts">
  import { setToken } from '../api';
  import { status, node, pollNow } from '../status.svelte';
  import { ENGINE_LABEL, ago, fmtDur, planeOf, roleLabel } from '../ui';

  const plane = $derived(planeOf(status.data));
  const i = $derived(node.info);

  function logout() {
    setToken('');
    pollNow();
  }
</script>

<div class="grid g2">
  <section class="card">
    <div class="card-head">
      <h2>Узел</h2>
      <span class="chip {i?.role === 'router' ? 'acc' : 'deg'}">{roleLabel(i)}</span>
    </div>
    <dl class="kv">
      {#if i}
        {#if i.role === 'router'}
          <dt>модель</dt><dd>{i.model || '—'}</dd>
          <dt>KeeneticOS</dt><dd class="mono">{i.firmware || '—'}</dd>
        {/if}
        <dt>имя</dt><dd class="mono">{i.hostname}</dd>
        <dt>архитектура</dt><dd class="mono">{i.arch} · Linux {i.kernel}</dd>
        <dt>работает</dt><dd>{fmtDur(i.uptime_sec)}</dd>
        <dt>nuxk-core запущен</dt><dd>{ago(i.core_since)}</dd>
      {:else}
        <dt>сведения</dt><dd>агент не сообщил (старая версия nuxk-core)</dd>
      {/if}
    </dl>
    {#if i?.role === 'stand'}
      <p class="banner deg note">
        Это тестовый стенд в Docker на Raspberry Pi. Движки здесь настоящие, но маршрутизации Keenetic нет — списки
        работают только на роутере.
      </p>
    {/if}
  </section>

  <section class="card">
    <div class="card-head">
      <h2>Как открыт интерфейс</h2>
      <span class="chip {node.via === 'controller' ? 'acc' : ''}">{node.via === 'controller' ? 'через контроллер Pi' : 'напрямую с узла'}</span>
    </div>
    <dl class="kv">
      {#if node.via === 'controller' && node.agent}
        <dt>агент</dt><dd class="mono">{node.agent.url}</dd>
        <dt>связь</dt>
        <dd>{#if node.agent.reachable}<span class="chip ok">есть</span>{:else}<span class="chip warn">нет</span>{/if}</dd>
        {#if node.agent.last_error}<dt>ошибка</dt><dd class="err-text">{node.agent.last_error}</dd>{/if}
        <dt>nuxk-controller</dt><dd class="mono">{node.agent.controller_version}</dd>
        <dt>история графиков</dt><dd>1 час на контроллере</dd>
      {:else}
        <dt>история графиков</dt><dd>10 минут, пока открыта вкладка</dd>
      {/if}
      <dt>живые события</dt><dd>{status.live ? 'поток SSE' : 'опрос раз в 5 с'}</dd>
    </dl>
    <div class="row top"><button class="ghost" onclick={logout}>Выйти (забыть токен)</button></div>
  </section>
</div>

<div class="grid g2">
  <section class="card">
    <div class="card-head"><h2>Версии</h2></div>
    <dl class="kv">
      <dt>nuxk-core</dt><dd class="mono">{status.data?.version ?? '—'}{i?.commit ? ` · ${i.commit}` : ''}</dd>
      <dt>nuxk-web</dt><dd class="mono">{__APP_VERSION__}{__LITE__ ? ' · lite' : ''}</dd>
      {#each status.data?.engines ?? [] as e (e.kind)}<dt>{ENGINE_LABEL[e.kind]}</dt><dd class="mono">{e.version || '—'}</dd>{/each}
      <dt>API</dt><dd class="mono">/api/v1 · OpenAPI</dd>
    </dl>
  </section>

  <section class="card">
    <div class="card-head"><h2>Настройки</h2></div>
    <p class="hint">
      В <span class="mono">/opt/etc/nuxk/nuxk.conf</span> на роутере; после правки —
      <span class="mono">/opt/etc/init.d/S99nuxk-core restart</span>.
    </p>
    <dl class="kv keys">
      <dt class="mono">PLANE_APPLY</dt><dd>{plane ? (plane.apply ? '1 — меняет роутер' : '0 — только план') : 'маршрутизация выключена'}</dd>
      <dt class="mono">PLANE_V6</dt><dd>deny — IPv6 к доменам из списков не идёт мимо туннеля</dd>
      <dt class="mono">PLANE_IFACE_WARP</dt><dd class="mono">{plane?.ifaces?.warp || '—'}</dd>
      <dt class="mono">PLANE_IFACE_VLESS</dt><dd class="mono">{plane?.ifaces?.vless || '—'}</dd>
      <dt class="mono">INFO_EVERY / PROBE_EVERY</dt><dd>опрос движков / активная проба, с</dd>
      <dt class="mono">ENGINE_*</dt><dd>пусто — движок выключен</dd>
    </dl>
  </section>
</div>

<style>
  .keys dd {
    text-align: left;
    color: var(--muted);
  }
  .note {
    margin-top: 12px;
    display: block;
  }
  .top {
    margin-top: 12px;
  }
</style>
