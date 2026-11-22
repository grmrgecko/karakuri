<script lang="ts">
  import { onDestroy } from 'svelte';
  import { markdown } from '../stores/markdown';

  let { source, onrender }: { source: string; onrender?: () => void } = $props();
  let html = $state<string>();
  let failed = $state(false);
  let pending = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let rendering = false;
  let destroyed = false;

  function queue(next: string) {
    pending = next;
    if (!timer && !rendering) timer = setTimeout(() => void render(), 150);
  }

  // contain keeps a reply from contacting other hosts on its own. Replies can
  // echo data a prompt injection controls, and an off-origin image would send
  // it out as soon as it renders, so those become their alt text. Links still
  // need a click.
  function contain(rendered: string): string {
    const doc = new DOMParser().parseFromString(rendered, 'text/html');
    for (const img of doc.body.querySelectorAll('img')) {
      let sameOrigin = false;
      try { sameOrigin = new URL(img.getAttribute('src') ?? '', location.href).origin === location.origin; } catch { /* invalid URL */ }
      if (!sameOrigin) img.replaceWith(doc.createTextNode(img.getAttribute('alt') ?? ''));
    }
    return doc.body.innerHTML;
  }

  async function render() {
    timer = undefined;
    rendering = true;
    const text = pending;
    try {
      const result = contain(await markdown.render(text));
      if (!destroyed) { html = result; failed = false; }
    } catch {
      if (!destroyed) { html = undefined; failed = true; }
    } finally {
      rendering = false;
      if (!destroyed) {
        onrender?.();
        // Keep one render in flight and catch the final delta after streaming stops.
        if (pending !== text) queue(pending);
      }
    }
  }

  $effect(() => queue(source));
  onDestroy(() => { destroyed = true; clearTimeout(timer); });
</script>

{#if html !== undefined}
  <div class="prompt-markdown assistant-markdown">{@html html}</div>
{:else}
  <div class="whitespace-pre-wrap break-words">{source}</div>
{/if}
{#if failed}
  <p class="text-xs opacity-60 mt-2">Markdown rendering failed. Showing the original reply.</p>
{/if}

<style>
  .assistant-markdown { min-width: 0; overflow-x: auto; }
  .assistant-markdown :global(:first-child) { margin-top: 0; }
  .assistant-markdown :global(:last-child) { margin-bottom: 0; }
  .assistant-markdown :global(h4), .assistant-markdown :global(h5), .assistant-markdown :global(h6) { font-weight: 600; margin: 1em 0 .5em; }
  .assistant-markdown :global(strong) { font-weight: 700; }
  .assistant-markdown :global(em) { font-style: italic; }
  .assistant-markdown :global(code) { font-family: var(--theme-font-mono); font-size: .9em; background: var(--theme-crust); border-radius: 3px; padding: .1em .3em; }
  .assistant-markdown :global(pre) { white-space: pre; overflow-wrap: normal; }
  .assistant-markdown :global(pre code) { background: none; padding: 0; }
  .assistant-markdown :global(hr) { border-color: var(--theme-surface2); margin: 1em 0; }
  .assistant-markdown :global(input[type='checkbox']) { margin-right: .4em; }
  .assistant-markdown :global(.chroma :is(.c, .c1, .cm, .cp, .cs)) { color: var(--theme-overlay0); font-style: italic; }
  .assistant-markdown :global(.chroma :is(.k, .kc, .kd, .kn, .kp, .kr, .kt, .err)) { color: var(--theme-error); }
  .assistant-markdown :global(.chroma :is(.s, .s1, .s2, .sb, .sc, .sd, .se, .sh, .si, .sr, .ss, .sx)) { color: var(--theme-success); }
  .assistant-markdown :global(.chroma :is(.m, .mb, .mf, .mh, .mi, .mo, .l, .ld)) { color: var(--theme-warning); }
  .assistant-markdown :global(.chroma :is(.nb, .bp, .nv, .vi)) { color: var(--theme-accent); }
  .assistant-markdown :global(.chroma :is(.nf, .nc, .nn, .ne, .nd)) { color: var(--theme-info); }
  .assistant-markdown :global(.chroma :is(.o, .ow, .p)) { color: var(--theme-text); opacity: .7; }
</style>
