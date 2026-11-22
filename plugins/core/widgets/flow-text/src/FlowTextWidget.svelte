<svelte:options customElement="flow-text-widget" />

<script lang="ts">
  import { parseConfig, sameOriginHTML, toTable, type Table } from "./config";
  import { placementIds, watchContent, type LiveContent } from "../../shared/dashboardLive";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  // The IDs are strings so an edit to any other field does not resubscribe.
  let dashboardId = $derived(placementIds(configAttr).dashboardId);
  let widgetId = $derived(placementIds(configAttr).widgetId);

  let content = $state<LiveContent | null>(null);
  let box = $state<HTMLDivElement | null>(null);
  let body = $state<HTMLDivElement | null>(null);

  $effect(() => {
    const dash = dashboardId;
    const widget = widgetId;
    content = null;
    return watchContent(dash, widget, (next) => {
      content = next;
    });
  });

  $effect(() => {
    if (cfg.size !== "fit" || !box || !body) return;
    const el = body;
    const off = (window as any).__karakuri?.fitText(box, el);
    return () => {
      off?.();
      el.style.fontSize = "";
    };
  });

  type View =
    | { kind: "placeholder"; text: string }
    | { kind: "html"; html: string }
    | { kind: "text"; text: string }
    | { kind: "json"; text: string }
    | { kind: "table"; table: Table };

  let view: View = $derived.by(() => {
    if (!content) return { kind: "placeholder", text: cfg.placeholder };
    const { value, format, html } = content;
    const table = toTable(value, cfg);
    if (table) return { kind: "table", table };
    if (format === "markdown" && typeof html === "string" && html !== "") {
      return { kind: "html", html: sameOriginHTML(html) };
    }
    if (typeof value === "string" && format !== "json") return { kind: "text", text: value };
    // A list of strings reads as lines, the same shape a rotating message
    // takes, so one flow can feed either widget.
    if (format !== "json" && Array.isArray(value) && value.length > 0 && value.every((v) => typeof v === "string")) {
      return { kind: "text", text: value.join("\n") };
    }
    let text: string;
    try {
      text = JSON.stringify(value, null, 2);
    } catch {
      text = String(value);
    }
    return { kind: "json", text };
  });
</script>

<div class="root align-{cfg.align} size-{cfg.size}">
  {#if cfg.title}
    <div class="title">{cfg.title}</div>
  {/if}
  <div class="box" bind:this={box}>
    <div class="body" bind:this={body}>
      {#if view.kind === "placeholder"}
        <span class="placeholder">{view.text}</span>
      {:else if view.kind === "html"}
        <div class="markdown">{@html view.html}</div>
      {:else if view.kind === "text"}
        <div class="text">{view.text}</div>
      {:else if view.kind === "table"}
        <table class="table">
          <thead>
            <tr>
              {#each view.table.headers as header}
                <th>{header}</th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each view.table.rows as row}
              <tr>
                {#each row as cell}
                  <td>{cell}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if view.table.hidden > 0}
          <div class="more">{view.table.hidden} more</div>
        {/if}
      {:else}
        <pre class="json">{view.text}</pre>
      {/if}
    </div>
  </div>
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    box-sizing: border-box;
  }

  .root {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    padding: 0.5rem 0.75rem;
    box-sizing: border-box;
    gap: 0.4rem;
  }

  .align-left { text-align: left; }
  .align-center { text-align: center; }
  .align-right { text-align: right; }

  .title {
    flex: none;
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--theme-text-muted, #a6adc8);
    border-bottom: 1px solid var(--theme-border-translucent, rgba(127, 127, 127, 0.3));
    padding-bottom: 0.25rem;
  }

  .box {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }

  .size-fit .box {
    overflow: hidden;
  }

  /* Fit measures the body, so it is shrink-wrapped: a word wider than the box
     makes it wider than the box and the text shrinks until it is not. */
  .size-fit .body {
    display: inline-block;
    text-align: inherit;
  }

  .size-small .body { font-size: 0.9rem; }
  .size-medium .body { font-size: 1.25rem; }
  .size-large .body { font-size: 2rem; }

  .body {
    line-height: 1.35;
    overflow-wrap: anywhere;
  }

  .placeholder {
    color: var(--theme-text-muted, #a6adc8);
  }

  .text {
    white-space: pre-wrap;
  }

  .json {
    margin: 0;
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.8em;
    white-space: pre-wrap;
    text-align: left;
  }

  .table {
    border-collapse: collapse;
    width: 100%;
    font-variant-numeric: tabular-nums;
  }

  .size-fit .table {
    width: auto;
  }

  .table th {
    font-size: 0.7em;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--theme-text-muted, #a6adc8);
    text-align: inherit;
    padding: 0 0.6em 0.3em 0;
    border-bottom: 1px solid var(--theme-border-translucent, rgba(127, 127, 127, 0.3));
    white-space: nowrap;
  }

  .table td {
    padding: 0.25em 0.6em 0.25em 0;
    vertical-align: top;
  }

  .table th:last-child,
  .table td:last-child {
    padding-right: 0;
  }

  .table tbody tr + tr td {
    border-top: 1px solid var(--theme-border-translucent, rgba(127, 127, 127, 0.15));
  }

  .more {
    margin-top: 0.3em;
    font-size: 0.7em;
    color: var(--theme-text-muted, #a6adc8);
  }

  .markdown :global(:first-child) { margin-top: 0; }
  .markdown :global(:last-child) { margin-bottom: 0; }
  .markdown :global(p) { margin: 0 0 0.6em; }
  .markdown :global(h1),
  .markdown :global(h2),
  .markdown :global(h3),
  .markdown :global(h4) {
    margin: 0.6em 0 0.3em;
    line-height: 1.2;
    color: var(--theme-heading-color, inherit);
  }
  .markdown :global(h1) { font-size: 1.5em; }
  .markdown :global(h2) { font-size: 1.3em; }
  .markdown :global(h3) { font-size: 1.15em; }
  .markdown :global(ul),
  .markdown :global(ol) {
    margin: 0 0 0.6em;
    padding-left: 1.3em;
    text-align: left;
  }
  .markdown :global(code) {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.9em;
    background: var(--theme-code-bg, rgba(127, 127, 127, 0.15));
    padding: 0.05em 0.3em;
    border-radius: var(--theme-radius-sm, 4px);
  }
  .markdown :global(pre) {
    overflow: auto;
    text-align: left;
  }
  .markdown :global(pre code) {
    display: block;
    padding: 0.5em;
  }
  .markdown :global(a) {
    color: var(--theme-link-color, var(--theme-accent, #89b4fa));
  }
  .markdown :global(img) {
    max-width: 100%;
  }
  .markdown :global(table) {
    border-collapse: collapse;
  }
  .markdown :global(th),
  .markdown :global(td) {
    border: 1px solid var(--theme-border, #45475a);
    padding: 0.2em 0.5em;
  }
  .markdown :global(blockquote) {
    margin: 0 0 0.6em;
    padding-left: 0.8em;
    border-left: 3px solid var(--theme-border, #45475a);
    color: var(--theme-text-muted, #a6adc8);
  }
</style>
