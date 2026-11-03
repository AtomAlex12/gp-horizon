<script lang="ts">
  // Full (Pi) build only — rendered under `!__LITE__`, so the router bundle
  // leaves it out. Hands the domains to «Прогоны»: put into the run form (and
  // copied to the clipboard when the browser lets it), then the tab opens.
  import { runDraft } from '../gpdraft.svelte';

  let { domains, label = 'Открыть «Прогоны»' }: { domains: string[]; label?: string } = $props();

  async function copy(text: string): Promise<boolean> {
    try {
      if (window.isSecureContext && navigator.clipboard) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch {
      /* fall back below */
    }
    // the Pi is usually opened over plain http, where the async clipboard API is absent
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand('copy');
    } catch {
      ok = false;
    }
    ta.remove();
    return ok;
  }

  async function open() {
    await copy(domains.join('\n'));
    runDraft.v = { domains: [...domains] };
    location.hash = 'runs';
    window.scrollTo(0, 0);
  }
</script>

<button class="sm" onclick={open} title="Домены подставятся в форму нового прогона">{label}</button>
