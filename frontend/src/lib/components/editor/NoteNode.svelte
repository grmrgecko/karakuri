<script lang="ts">
  import { NodeResizer, type NodeProps } from '@xyflow/svelte';
  import { editor } from '../../stores/editor';
  import { markdown } from '../../stores/markdown';
  import { imageSet, resolveImages } from '../../utils/noteImages';
  import type { EditorNode } from './types';

  let { id, data, selected }: NodeProps<EditorNode> = $props();

  // Sticky color palette tuned for the dark theme.
  const colors: Record<string, { bg: string; border: string }> = {
    yellow: { bg: 'rgba(249, 226, 175, 0.18)', border: '#f9e2af' },
    blue: { bg: 'rgba(137, 180, 250, 0.18)', border: '#89b4fa' },
    green: { bg: 'rgba(166, 227, 161, 0.18)', border: '#a6e3a1' },
    pink: { bg: 'rgba(245, 194, 231, 0.18)', border: '#f5c2e7' },
    gray: { bg: 'rgba(108, 112, 134, 0.25)', border: '#6c7086' },
  };

  // renderDelay keeps the canvas from asking the server for a rendering on
  // every keystroke while the note is being edited in the config panel.
  const renderDelay = 250;

  let color = $derived(colors[(data.config.color as string) || 'yellow'] ?? colors.yellow);
  let text = $derived((data.config.text as string) || '');
  let images = $derived(imageSet(data.config.images));

  let html = $state('');
  let failed = $state(false);
  let problem = $derived($editor.problems?.[`node:${id}`]);

  // The server renders `image:name` into an img src and the attached picture
  // is substituted here, so the bytes never travel to the server and back on
  // every keystroke, and attaching or removing an image redraws the note
  // without a re-render.
  let body = $derived(resolveImages(html, images));

  // The rendering comes from the server. A source already rendered paints
  // immediately from the cache; anything else waits out the typing pause first,
  // and the previous rendering stays on screen until the new one lands so the
  // note does not blink between edits.
  $effect(() => {
    const source = text;
    const hit = markdown.cached(source);
    if (hit !== undefined) {
      html = hit;
      failed = false;
      return;
    }
    const timer = setTimeout(() => {
      markdown
        .render(source)
        .then((rendered) => {
          if (source !== text) return;
          html = rendered;
          failed = false;
        })
        .catch(() => {
          if (source === text) failed = true;
        });
    }, renderDelay);
    return () => clearTimeout(timer);
  });
</script>

<NodeResizer
  isVisible={!!selected}
  minWidth={140}
  minHeight={70}
  color={color.border}
  onResizeEnd={() => editor.markDirty()}
/>

<div
  class="note-node"
  class:selected
  class:has-problem={!!problem}
  style="background-color: {color.bg}; border-color: {color.border};"
  title={problem || ''}
>
  <!-- nowheel keeps a scroll inside the note from zooming the canvas. -->
  <div class="note-body nowheel">
    {#if failed}
      <p class="note-fallback">{text}</p>
    {:else if text}
      <!-- Rendered server-side by goldmark with raw markup escaped, so the
           only tags here are the ones markdown produced, and the only image
           sources are values that passed the data-URI check. -->
      <!-- eslint-disable-next-line svelte/no-at-html-tags -->
      <div class="note-markdown">{@html body}</div>
    {:else}
      <p class="placeholder">Empty note</p>
    {/if}
  </div>
</div>

<style>
  /* A note always has an explicit size on the canvas — its own if it was
     resized, the default one otherwise — so the box fills the node exactly and
     the text inside decides nothing about how large the note is. */
  .note-node {
    width: 100%;
    height: 100%;
    padding: 0.5rem 0.65rem;
    border: 1px solid;
    border-radius: var(--theme-radius-sm, 4px);
    font-size: 0.75rem;
    color: var(--theme-text, #cdd6f4);
    overflow: hidden;
  }

  .note-node.selected {
    box-shadow: 0 0 0 1px var(--theme-accent, #89b4fa);
  }

  .note-node.has-problem {
    box-shadow: 0 0 0 2px var(--theme-error, #f38ba8);
  }

  /* Text longer than the note scrolls rather than growing the node: the size
     on the canvas is the author's decision, not the content's. */
  .note-body {
    width: 100%;
    height: 100%;
    max-height: 100%;
    overflow: auto;
  }

  .note-fallback {
    white-space: pre-wrap;
    word-break: break-word;
    opacity: 0.7;
  }

  .placeholder {
    opacity: 0.5;
    font-style: italic;
  }

  /* Markdown arrives as plain tags, so every element a note is likely to use is
     styled here. The scale is the note's, not the reference viewer's: a sticky
     on the canvas is read at a glance. */
  .note-markdown {
    line-height: 1.5;
    word-break: break-word;
  }

  .note-markdown :global(h1),
  .note-markdown :global(h2),
  .note-markdown :global(h3),
  .note-markdown :global(h4) {
    font-weight: 700;
    margin: 0.4em 0 0.25em;
    line-height: 1.3;
  }

  .note-markdown :global(h1) {
    font-size: 1.35em;
  }

  .note-markdown :global(h2) {
    font-size: 1.2em;
  }

  .note-markdown :global(h3) {
    font-size: 1.05em;
  }

  .note-markdown :global(h4) {
    font-size: 1em;
  }

  .note-markdown :global(:first-child) {
    margin-top: 0;
  }

  .note-markdown :global(p) {
    margin: 0.4em 0;
  }

  .note-markdown :global(ul),
  .note-markdown :global(ol) {
    margin: 0.4em 0;
    padding-left: 1.3em;
    list-style: disc;
  }

  .note-markdown :global(ol) {
    list-style: decimal;
  }

  .note-markdown :global(li) {
    margin: 0.15em 0;
  }

  .note-markdown :global(strong) {
    font-weight: 600;
  }

  .note-markdown :global(em) {
    font-style: italic;
  }

  .note-markdown :global(a) {
    color: var(--theme-accent, #89b4fa);
    text-decoration: underline;
  }

  .note-markdown :global(hr) {
    margin: 0.6em 0;
    border: 0;
    border-top: 1px solid currentColor;
    opacity: 0.25;
  }

  .note-markdown :global(code) {
    padding: 0 3px;
    border-radius: 3px;
    background-color: var(--theme-surface0, #313244);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 0.9em;
  }

  .note-markdown :global(pre) {
    margin: 0.4em 0;
    padding: 0.4em 0.5em;
    border-radius: 4px;
    background-color: var(--theme-mantle, #181825);
    overflow-x: auto;
  }

  .note-markdown :global(pre code) {
    padding: 0;
    background: transparent;
  }

  .note-markdown :global(blockquote) {
    margin: 0.4em 0;
    padding-left: 0.5em;
    border-left: 2px solid currentColor;
    opacity: 0.8;
  }

  .note-markdown :global(table) {
    display: block;
    width: max-content;
    max-width: 100%;
    overflow-x: auto;
    margin: 0.4em 0;
    border-collapse: collapse;
  }

  .note-markdown :global(th),
  .note-markdown :global(td) {
    padding: 2px 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    text-align: left;
    vertical-align: top;
  }

  .note-markdown :global(th) {
    font-weight: 600;
  }

  /* An attached image fits the note's width and scrolls with the rest of the
     text rather than deciding how large the note is. */
  .note-markdown :global(img) {
    display: block;
    max-width: 100%;
    height: auto;
    margin: 0.4em 0;
    border-radius: 4px;
  }

  .note-markdown :global(input[type='checkbox']) {
    margin-right: 0.3em;
  }
</style>
