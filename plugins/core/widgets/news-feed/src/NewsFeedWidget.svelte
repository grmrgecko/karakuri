<svelte:options customElement="news-feed-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { ago, itemKey, keep, parseConfig, type FeedError, type NewsItem } from "./config";
  import { placementIds, watchContent } from "../../shared/dashboardLive";
  import { currentVariables } from "../../shared/variables";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  // The IDs are strings so an edit to any other field does not refetch.
  let dashboardId = $derived(placementIds(configAttr).dashboardId);
  let widgetId = $derived(placementIds(configAttr).widgetId);

  let raw = $state<NewsItem[]>([]);
  let errors = $state<FeedError[]>([]);
  let loadError = $state("");
  let loaded = $state(false);
  let index = $state(0);
  // expanded is the details toggle a content command flips for the headline
  // showing; moving to another headline clears it.
  let expanded = $state(false);
  let visible = $state(true);
  let now = $state(Date.now());

  let items = $derived(keep(raw, cfg, now));

  let fetchGeneration = 0;
  let fadeTimer: ReturnType<typeof setTimeout> | null = null;
  const FADE_MS = 400;

  const bridge = () => (window as any).__karakuri;

  // fetchFeed reads the placement's headlines. A failed read keeps the items
  // already showing; a stale display beats an empty one on a wall.
  async function fetchFeed(dash: string, widget: string): Promise<void> {
    const generation = ++fetchGeneration;
    if (!bridge() || !dash || !widget) return;
    try {
      // The server reads the feed URLs itself, so a URL naming a variable
      // needs this screen's values; the server accepts only options.
      const values = currentVariables().values;
      const vars = Object.keys(values).length > 0 ? `?vars=${encodeURIComponent(JSON.stringify(values))}` : "";
      const res: any = await bridge().api.get(
        `/dashboards/${encodeURIComponent(dash)}/widgets/${encodeURIComponent(widget)}/feed${vars}`,
      );
      if (generation !== fetchGeneration) return;
      const data = res && typeof res === "object" && "items" in res ? res : res?.data;
      const next: NewsItem[] = Array.isArray(data?.items) ? data.items : [];
      const showing = items[index];
      raw = next;
      errors = Array.isArray(data?.errors) ? data.errors : [];
      loadError = "";
      now = Date.now();
      // Stay on the headline that was showing if it is still there.
      const kept = keep(next, cfg, now);
      const at = showing ? kept.findIndex((i) => itemKey(i) === itemKey(showing)) : -1;
      index = at >= 0 ? at : 0;
    } catch (e: any) {
      if (generation !== fetchGeneration) return;
      loadError = e?.message || "Headlines could not be read";
    } finally {
      if (generation === fetchGeneration) loaded = true;
    }
  }

  // The feed list is read by the server from the saved config, and the editor
  // saves after the panel changes it, so a change refetches a moment later.
  let feedsKey = $derived(JSON.stringify(cfg.feeds));
  let firstFetch = true;
  $effect(() => {
    void feedsKey;
    const dash = dashboardId;
    const widget = widgetId;
    const delay = firstFetch ? 0 : 1500;
    firstFetch = false;
    const timer = setTimeout(() => void fetchFeed(dash, widget), delay);
    return () => clearTimeout(timer);
  });

  $effect(() => {
    const ms = cfg.reload * 60_000;
    const dash = dashboardId;
    const widget = widgetId;
    const timer = setInterval(() => void fetchFeed(dash, widget), ms);
    return () => clearInterval(timer);
  });

  // ---- Rotation ----

  function move(step: number, animate: boolean): void {
    const n = items.length;
    if (n === 0) return;
    const next = (((index + step) % n) + n) % n;
    expanded = false;
    now = Date.now();
    if (next === index) return;
    if (fadeTimer) clearTimeout(fadeTimer);
    if (!animate || cfg.layout !== "ticker") {
      visible = true;
      index = next;
      return;
    }
    visible = false;
    fadeTimer = setTimeout(() => {
      fadeTimer = null;
      index = next;
      visible = true;
    }, FADE_MS);
  }

  // rotationRun restarts the interval, so a steered headline gets its full
  // time on screen.
  let rotationRun = $state(0);
  $effect(() => {
    void rotationRun;
    const ms = cfg.interval * 1000;
    const timer = setInterval(() => untrack(() => {
      // An expanded headline stays until it is steered away from.
      if (!expanded) move(1, true);
    }), ms);
    return () => clearInterval(timer);
  });

  $effect(() => {
    const dash = dashboardId;
    const widget = widgetId;
    // The first state read is history: a command sent before this display
    // opened has already been acted on by whoever was watching then.
    let last: string | null = null;
    return watchContent(dash, widget, (content) => {
      const stamp = content?.updated_at ?? "";
      if (last === null || stamp === last || !content) {
        last = stamp;
        return;
      }
      last = stamp;
      const command = typeof content.value === "string" ? content.value.trim().toLowerCase() : "";
      untrack(() => {
        if (command === "next") move(1, true);
        else if (command === "previous") move(-1, true);
        else if (command === "details") expanded = !expanded;
        else return;
        rotationRun += 1;
      });
    });
  });

  onDestroy(() => {
    fetchGeneration += 1;
    if (fadeTimer) clearTimeout(fadeTimer);
  });

  // ---- Rendering ----

  let current = $derived(items.length > 0 ? items[Math.min(index, items.length - 1)] : null);

  let shown = $derived.by(() => {
    const n = items.length;
    const size = Math.min(cfg.listSize, n);
    const out: NewsItem[] = [];
    for (let i = 0; i < size; i++) out.push(items[(index + i) % n]);
    return out;
  });

  function meta(item: NewsItem): string {
    const parts: string[] = [];
    if (cfg.showSource && item.source) parts.push(item.source);
    if (cfg.showTime) {
      const age = ago(item.published, now);
      if (age) parts.push(age);
    }
    return parts.join(", ");
  }

  function described(isCurrent: boolean): boolean {
    return isCurrent && expanded ? !cfg.showDescription : cfg.showDescription;
  }

  let status = $derived(
    cfg.feeds.filter((f) => f.url).length === 0
      ? "No feeds configured"
      : !loaded
        ? "Loading headlines"
        : loadError
          ? loadError
          : "No headlines",
  );
</script>

<div class="root">
  {#if items.length === 0}
    <div class="status">{status}</div>
  {:else if cfg.layout === "list"}
    <ul class="list">
      {#each shown as item, i}
        <li>
          {#if meta(item)}<div class="meta">{meta(item)}</div>{/if}
          <div class="title small">{item.title}</div>
          {#if described(i === 0) && item.description}
            <div class="description">{item.description}</div>
          {/if}
        </li>
      {/each}
    </ul>
  {:else if current}
    <div class="ticker" class:hidden={!visible} style:transition-duration="{FADE_MS}ms">
      {#if meta(current)}<div class="meta">{meta(current)}</div>{/if}
      <div class="title">{current.title}</div>
      {#if described(true) && current.description}
        <div class="description">{current.description}</div>
      {/if}
    </div>
  {/if}

  {#if items.length > 0 && (errors.length > 0 || loadError)}
    <div class="errors" title={[loadError, ...errors.map((e) => `${e.feed || "Feed"}: ${e.error}`)].filter(Boolean).join("\n")}>
      {#if loadError}
        Not refreshed: {loadError}
      {:else}
        {errors.map((e) => e.feed || "Feed").join(", ")} could not be read
      {/if}
    </div>
  {/if}
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
    overflow: hidden;
    gap: 0.4rem;
  }

  .status {
    margin: auto;
    color: var(--theme-text-muted, #a6adc8);
    font-size: 0.9rem;
    text-align: center;
  }

  .ticker {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    justify-content: center;
    text-align: center;
    gap: 0.25rem;
    opacity: 1;
    transition-property: opacity;
    transition-timing-function: ease-in-out;
  }

  .ticker.hidden {
    opacity: 0;
  }

  .meta {
    font-size: 0.75rem;
    color: var(--theme-text-muted, #a6adc8);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .title {
    font-size: 1.35rem;
    line-height: 1.25;
    overflow-wrap: anywhere;
  }

  .title.small {
    font-size: 1rem;
  }

  .description {
    font-size: 0.85rem;
    line-height: 1.35;
    color: var(--theme-subtext1, var(--theme-text-muted, #a6adc8));
    display: -webkit-box;
    -webkit-line-clamp: 4;
    line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .list {
    flex: 1;
    min-height: 0;
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    overflow: hidden;
  }

  .errors {
    flex: none;
    font-size: 0.65rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.7;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
