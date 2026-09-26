<script lang="ts">
  // Вход: на роутере — root из Entware (тот же, что для SSH), на контроллере
  // Pi — admin с паролем из первичной настройки.
  import { api, HttpError } from '../api';
  import { node, pollNow } from '../status.svelte';

  const ctl = node.via === 'controller';
  let user = $state(ctl ? 'admin' : 'root');
  let password = $state('');
  let busy = $state(false);
  let err = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    err = '';
    try {
      await api.login(ctl ? 'controller' : 'agent', user.trim(), password);
      password = '';
      pollNow();
    } catch (x) {
      err =
        x instanceof HttpError && x.status === 401
          ? 'Неверный логин или пароль'
          : x instanceof Error
            ? x.message
            : String(x);
    } finally {
      busy = false;
    }
  }
</script>

<form class="card gate-card stack" onsubmit={submit}>
  <div>
    <h2>Вход в nuxk</h2>
    <p class="hint">
      {#if ctl}
        Логин <span class="mono">admin</span> и пароль, который вы задали при настройке контроллера.
      {:else}
        Логин и пароль <span class="mono">root</span> от Entware на этом роутере — те же, что для входа по SSH.
      {/if}
    </p>
  </div>
  <label class="field">
    Логин
    <input type="text" bind:value={user} autocomplete="username" autocapitalize="none" spellcheck="false" required />
  </label>
  <label class="field">
    Пароль
    <!-- svelte-ignore a11y_autofocus -->
    <input type="password" bind:value={password} autocomplete="current-password" required autofocus />
  </label>
  {#if err}<p class="form-err" role="alert">{err}</p>{/if}
  <button type="submit" disabled={busy || !user.trim() || !password}>{busy ? 'Проверяю…' : 'Войти'}</button>
</form>

<style>
  .form-err {
    margin: 0;
    color: var(--warn);
    font-size: 12.5px;
  }
</style>
