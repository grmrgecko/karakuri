<svelte:options customElement="news-feed-config" />

<script lang="ts">
  import { MAX_FEEDS, parseConfig, type FeedSource, type NewsConfig } from "./config";
  import { placementIds } from "../../shared/dashboardLive";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<NewsConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const { widgetId } = placementIds(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function update(patch: Partial<NewsConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: $state.snapshot(cfg), bubbles: true, composed: true }),
    );
  }

  // numberInput reads a number field clamped to what the schema accepts, so a
  // stray entry cannot leave a config that fails the deploy check.
  function numberInput(e: Event, fallback: number, min: number, max = Infinity, integer = false): number {
    const input = e.target as HTMLInputElement;
    let n = Number(input.value);
    if (input.value.trim() === "" || !Number.isFinite(n)) n = fallback;
    if (integer) n = Math.round(n);
    n = Math.min(max, Math.max(min, n));
    input.value = String(n);
    return n;
  }

  function addFeed(): void {
    if (cfg.feeds.length >= MAX_FEEDS) return;
    update({ feeds: [...cfg.feeds, { url: "", title: "" }] });
  }

  function removeFeed(index: number): void {
    update({ feeds: cfg.feeds.filter((_, i) => i !== index) });
  }

  function editFeed(index: number, patch: Partial<FeedSource>): void {
    update({ feeds: cfg.feeds.map((f, i) => (i === index ? { ...f, ...patch } : f)) });
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Feeds</span>
    {#each cfg.feeds as feed, index}
      <div class="feed">
        <input class="input" placeholder="Title" value={feed.title}
               aria-label="Feed {index + 1} title"
               oninput={(e: Event) => editFeed(index, { title: (e.target as HTMLInputElement).value })} />
        <div class="feed-row">
          <input class="input mono" type="url" placeholder="https://example.com/rss.xml" value={feed.url}
                 aria-label="Feed {index + 1} URL"
                 oninput={(e: Event) => editFeed(index, { url: (e.target as HTMLInputElement).value.trim() })} />
          <button type="button" class="remove" title="Remove feed" aria-label="Remove feed {index + 1}"
                  onclick={() => removeFeed(index)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
    {/each}
    {#if cfg.feeds.length < MAX_FEEDS}
      <button type="button" class="add" onclick={addFeed}>+ Add feed</button>
    {/if}
    <span class="hint">Up to {MAX_FEEDS} RSS or Atom feeds. The server fetches them, so a viewer's browser never contacts the feed.</span>
  </div>

  <div class="field">
    <label class="label" for="nf-layout">Layout</label>
    <select id="nf-layout" class="input" value={cfg.layout}
            onchange={(e: Event) => update({ layout: (e.target as HTMLSelectElement).value as NewsConfig["layout"] })}>
      <option value="ticker">One headline at a time</option>
      <option value="list">List</option>
    </select>
  </div>

  <div class="row">
    <div class="field">
      <label class="label" for="nf-interval">Headline interval (seconds)</label>
      <input id="nf-interval" class="input" type="number" min="2" value={cfg.interval}
             onchange={(e: Event) => update({ interval: numberInput(e, cfg.interval, 2) })} />
    </div>
    <div class="field">
      <label class="label" for="nf-reload">Refresh (minutes)</label>
      <input id="nf-reload" class="input" type="number" min="1" value={cfg.reload}
             onchange={(e: Event) => update({ reload: numberInput(e, cfg.reload, 1) })} />
    </div>
  </div>

  <div class="row">
    <div class="field">
      <label class="label" for="nf-max">Most headlines</label>
      <input id="nf-max" class="input" type="number" min="1" value={cfg.maxItems}
             onchange={(e: Event) => update({ maxItems: numberInput(e, cfg.maxItems, 1, Infinity, true) })} />
    </div>
    <div class="field">
      <label class="label" for="nf-age">Oldest (hours)</label>
      <input id="nf-age" class="input" type="number" min="0" value={cfg.maxAgeHours}
             onchange={(e: Event) => update({ maxAgeHours: numberInput(e, cfg.maxAgeHours, 0) })} />
      <span class="hint">0 keeps every headline.</span>
    </div>
  </div>

  {#if cfg.layout === "list"}
    <div class="field">
      <label class="label" for="nf-size">Headlines shown</label>
      <input id="nf-size" class="input" type="number" min="1" value={cfg.listSize}
             onchange={(e: Event) => update({ listSize: numberInput(e, cfg.listSize, 1, Infinity, true) })} />
    </div>
  {/if}

  <div class="field">
    <label class="checkbox-label">
      <input type="checkbox" checked={cfg.showDescription}
             onchange={(e: Event) => update({ showDescription: (e.target as HTMLInputElement).checked })} />
      <span>Show descriptions</span>
    </label>
    <label class="checkbox-label">
      <input type="checkbox" checked={cfg.showSource}
             onchange={(e: Event) => update({ showSource: (e.target as HTMLInputElement).checked })} />
      <span>Show source</span>
    </label>
    <label class="checkbox-label">
      <input type="checkbox" checked={cfg.showTime}
             onchange={(e: Event) => update({ showTime: (e.target as HTMLInputElement).checked })} />
      <span>Show age</span>
    </label>
  </div>

  <span class="hint">
    A flow can steer the widget with a content command whose value is next, previous, or details.
  </span>
</div>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-input-border, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-input-bg, var(--theme-surface0, #313244));
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }

  .input.mono {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.8rem;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .feed {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 0.4rem;
    border: 1px solid var(--theme-border-translucent, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
  }

  .feed-row {
    display: flex;
    gap: 0.3rem;
  }

  .remove {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    flex-shrink: 0;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface0, #313244);
    color: var(--theme-error, #f38ba8);
    cursor: pointer;
  }

  .remove:hover {
    border-color: var(--theme-error, #f38ba8);
  }

  .remove svg {
    width: 14px;
    height: 14px;
  }

  .add {
    align-self: flex-start;
    padding: 0.35rem 0.75rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--theme-accent, #89b4fa);
  }
</style>
