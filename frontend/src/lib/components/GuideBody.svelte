<script lang="ts">
  // GuideBody shows one guide's HTML with the prose styles every guide uses,
  // so a guide reads the same in the help pages and in an editor panel. Links
  // between guides are written as `other.md#section`; a click on one calls
  // onnavigate instead of following the file path, a link to a section of the
  // same guide scrolls to it, and a link off the site opens in a new tab.
  let {
    html,
    onnavigate,
  }: {
    html: string;
    onnavigate: (id: string, anchor: string) => void;
  } = $props();

  let body = $state<HTMLDivElement | null>(null);

  // GUIDE_LINK matches a link to another guide, with an optional section.
  const GUIDE_LINK = /^([a-z0-9-]+)\.md(?:#([a-z0-9-]+))?$/;

  function click(e: MouseEvent) {
    const a = (e.target as HTMLElement).closest('a');
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
    const href = a.getAttribute('href') ?? '';
    const guide = GUIDE_LINK.exec(href);
    if (guide) {
      e.preventDefault();
      onnavigate(guide[1], guide[2] ?? '');
      return;
    }
    if (href.startsWith('#')) {
      e.preventDefault();
      scrollTo(href.slice(1));
      return;
    }
    if (/^https?:\/\//.test(href)) {
      e.preventDefault();
      window.open(href, '_blank', 'noopener');
    }
  }

  /** scrollTo brings a heading of this guide into view. */
  export function scrollTo(anchor: string): boolean {
    if (!body || !anchor) return false;
    const target = body.querySelector<HTMLElement>(`#${CSS.escape(anchor)}`);
    if (!target) return false;
    target.scrollIntoView({ block: 'start' });
    return true;
  }

  /** sections lists the guide's second-level headings by anchor and text. */
  export function sections(): Array<{ anchor: string; label: string }> {
    if (!body) return [];
    return [...body.querySelectorAll<HTMLElement>('h2[id]')].map((h) => ({ anchor: h.id, label: h.textContent ?? '' }));
  }
</script>

<!-- The HTML is rendered server-side by goldmark with raw markup escaped, so
     the only tags here are the ones markdown produced. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="docs-body" bind:this={body} onclick={click}>
  <!-- eslint-disable-next-line svelte/no-at-html-tags -->
  {@html html}
</div>

<style>
  /* Markdown comes in as plain tags, so every element the reference uses is
     styled here rather than by a class the renderer would have to emit. */
  /* Prose is capped and centred rather than filling the editor-sized modal:
     a 1200px line is hard to read, and dead space to one side reads as a
     rendering fault. Tables and code blocks scroll inside the same column. */
  .docs-body {
    font-size: 13px;
    line-height: 1.65;
    max-width: 96ch;
    margin: 0 auto;
  }

  .docs-body :global(h1) {
    font-size: 20px;
    font-weight: 700;
    margin: 20px 0 8px;
  }

  .docs-body :global(h2) {
    font-size: 16px;
    font-weight: 700;
    margin: 28px 0 8px;
    padding-top: 12px;
    border-top: 1px solid var(--theme-surface1, #45475a);
  }

  .docs-body :global(h3) {
    font-size: 14px;
    font-weight: 600;
    margin: 18px 0 6px;
    opacity: 0.9;
  }

  .docs-body :global(p) {
    margin: 8px 0;
  }

  .docs-body :global(ul),
  .docs-body :global(ol) {
    margin: 8px 0;
    padding-left: 20px;
    list-style: disc;
  }

  .docs-body :global(ol) {
    list-style: decimal;
  }

  .docs-body :global(li) {
    margin: 3px 0;
  }

  .docs-body :global(strong) {
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
  }

  .docs-body :global(a) {
    color: var(--theme-accent, #89b4fa);
    text-decoration: underline;
  }

  .docs-body :global(hr) {
    margin: 24px 0;
    border: 0;
    border-top: 1px solid var(--theme-surface1, #45475a);
  }

  .docs-body :global(code) {
    padding: 1px 4px;
    border-radius: 3px;
    background-color: var(--theme-surface0, #313244);
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 12px;
  }

  .docs-body :global(pre) {
    margin: 10px 0;
    padding: 10px 12px;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-mantle, #181825);
    overflow-x: auto;
  }

  .docs-body :global(pre code) {
    padding: 0;
    background: transparent;
    font-size: 12px;
    line-height: 1.55;
  }

  /* Chroma tokenizes the fenced code on the server into classes; the colors
     are picked here so a snippet reads the same as the script editor does.
     They mirror the highlight style in scriptLang.ts. */
  .docs-body :global(.chroma .c),
  .docs-body :global(.chroma .c1),
  .docs-body :global(.chroma .cm),
  .docs-body :global(.chroma .cp),
  .docs-body :global(.chroma .cs) {
    color: var(--theme-overlay0, #6c7086);
    font-style: italic;
  }

  .docs-body :global(.chroma .k),
  .docs-body :global(.chroma .kc),
  .docs-body :global(.chroma .kd),
  .docs-body :global(.chroma .kn),
  .docs-body :global(.chroma .kp),
  .docs-body :global(.chroma .kr),
  .docs-body :global(.chroma .kt) {
    color: var(--theme-error, #f38ba8);
  }

  .docs-body :global(.chroma .s),
  .docs-body :global(.chroma .s1),
  .docs-body :global(.chroma .s2),
  .docs-body :global(.chroma .sb),
  .docs-body :global(.chroma .sc),
  .docs-body :global(.chroma .sd),
  .docs-body :global(.chroma .se),
  .docs-body :global(.chroma .sh),
  .docs-body :global(.chroma .si),
  .docs-body :global(.chroma .sr),
  .docs-body :global(.chroma .ss),
  .docs-body :global(.chroma .sx) {
    color: var(--theme-success, #a6e3a1);
  }

  .docs-body :global(.chroma .m),
  .docs-body :global(.chroma .mb),
  .docs-body :global(.chroma .mf),
  .docs-body :global(.chroma .mh),
  .docs-body :global(.chroma .mi),
  .docs-body :global(.chroma .mo),
  .docs-body :global(.chroma .l),
  .docs-body :global(.chroma .ld) {
    color: var(--theme-warning, #f9e2af);
  }

  .docs-body :global(.chroma .nb),
  .docs-body :global(.chroma .bp),
  .docs-body :global(.chroma .nv),
  .docs-body :global(.chroma .vi) {
    color: var(--theme-accent, #89b4fa);
  }

  .docs-body :global(.chroma .nf),
  .docs-body :global(.chroma .nc),
  .docs-body :global(.chroma .nn),
  .docs-body :global(.chroma .ne),
  .docs-body :global(.chroma .nd) {
    color: var(--theme-info, #94e2d5);
  }

  .docs-body :global(.chroma .o),
  .docs-body :global(.chroma .ow),
  .docs-body :global(.chroma .p) {
    color: var(--theme-text, #cdd6f4);
    opacity: 0.7;
  }

  .docs-body :global(.chroma .err) {
    color: var(--theme-error, #f38ba8);
  }

  /* A wide table scrolls inside its own wrapper so the page never does. */
  .docs-body :global(table) {
    display: block;
    width: max-content;
    max-width: 100%;
    overflow-x: auto;
    margin: 12px 0;
    border-collapse: collapse;
    font-size: 12px;
  }

  .docs-body :global(th),
  .docs-body :global(td) {
    padding: 5px 10px;
    border: 1px solid var(--theme-surface1, #45475a);
    text-align: left;
    vertical-align: top;
  }

  .docs-body :global(th) {
    background-color: var(--theme-surface0, #313244);
    font-weight: 600;
  }

  .docs-body :global(blockquote) {
    margin: 10px 0;
    padding-left: 12px;
    border-left: 3px solid var(--theme-surface1, #45475a);
    opacity: 0.85;
  }
</style>
