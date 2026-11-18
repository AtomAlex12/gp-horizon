<script lang="ts">
  let { value, label, rows = 8, readonly = false, placeholder = '', onchange }: {
    value: string; label: string; rows?: number; readonly?: boolean; placeholder?: string;
    onchange: (value: string) => void;
  } = $props();
  let gutter: HTMLPreElement;
</script>

<div class="code-editor">
  <pre bind:this={gutter} aria-hidden="true">{Array.from({ length: value.split('\n').length }, (_, i) => i + 1).join('\n')}</pre>
  <textarea aria-label={label} {rows} {readonly} {placeholder} {value} spellcheck="false" wrap="off"
    oninput={event => onchange(event.currentTarget.value)}
    onscroll={event => { if (gutter) gutter.scrollTop = event.currentTarget.scrollTop; }}></textarea>
</div>

<style>
  .code-editor { display: flex; position: relative; padding-left: 44px; min-width: 0; overflow: hidden; border: 1px solid var(--line); border-radius: 6px; background: var(--surface-2); }
  .code-editor:focus-within { outline: 2px solid var(--accent); outline-offset: 1px; }
  pre, textarea { font: 12.5px/1.65 var(--font-mono); margin: 0; padding: 10px 12px; }
  pre { position: absolute; inset: 0 auto 0 0; width: 44px; max-height: 100%; overflow: hidden; text-align: right; color: var(--muted); user-select: none; align-self: stretch; height: auto; }
  textarea { flex: 1; min-width: 0; border: 0; border-radius: 0; outline: none; background: var(--surface); color: var(--accent); resize: vertical; overflow: auto; }
  textarea:focus-visible { outline: none; }
</style>
