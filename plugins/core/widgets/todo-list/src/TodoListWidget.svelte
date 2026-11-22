<svelte:options customElement="todo-list-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { dueLabel, parseConfig, type TodoItem } from "./config";
  import { hostOperates } from "../../shared/entitySource";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let items = $state<TodoItem[]>([]);
  let loaded = $state(false);
  let entityName = $state("");
  let entityState = $state("");
  let error = $state("");
  let actionError = $state("");
  // denied is the server saying this dashboard may not change the list. The
  // checkboxes and the add field go away after it rather than failing on
  // every tap.
  let denied = $state(false);
  let pending = $state<Record<string, boolean>>({});
  let draft = $state("");
  let adding = $state(false);

  let generation = 0;
  let unsubscribe: (() => void) | null = null;
  let refetchTimer: ReturnType<typeof setTimeout> | null = null;
  let actionTimer: ReturnType<typeof setTimeout> | null = null;

  const bridge = () => (window as any).__karakuri;

  let bound = $derived(!!cfg.connectionId && !!cfg.entityId);
  let bindKey = $derived(JSON.stringify([cfg.connectionId, cfg.entityId]));
  let base = $derived(`/connections/${encodeURIComponent(cfg.connectionId)}`);

  $effect(() => {
    void bindKey;
    untrack(() => void bind());
    return () => unbind();
  });

  onDestroy(() => {
    unbind();
    if (actionTimer) clearTimeout(actionTimer);
  });

  function unbind(): void {
    generation += 1;
    unsubscribe?.();
    unsubscribe = null;
    if (refetchTimer) clearTimeout(refetchTimer);
    refetchTimer = null;
  }

  async function bind(): Promise<void> {
    const gen = ++generation;
    items = [];
    loaded = false;
    error = "";
    denied = false;
    entityName = "";
    entityState = "";
    if (!bound) return;

    // A todo entity's state is its open item count, so a state change is the
    // signal that the list changed somewhere else. Bursts of edits arrive as
    // bursts of changes, so the refetch waits for them to settle.
    const off = bridge()?.subscribeTopic(
      `ha:${cfg.connectionId}:state:${cfg.entityId}`,
      (data: any) => {
        if (gen !== generation || !data?.new_state) return;
        applyEntity(data.new_state);
        if (refetchTimer) clearTimeout(refetchTimer);
        refetchTimer = setTimeout(() => void loadItems(gen), 300);
      },
    );
    if (gen !== generation) {
      off?.();
      return;
    }
    unsubscribe = off ?? null;

    void loadEntity(gen);
    await loadItems(gen);
  }

  function applyEntity(entity: any): void {
    entityName = String(entity?.attributes?.friendly_name ?? "");
    entityState = String(entity?.state ?? "");
  }

  async function loadEntity(gen: number): Promise<void> {
    try {
      const res = await bridge().api.get(`${base}/ha/states`);
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      const entity = list.find((s) => s.entity_id === cfg.entityId);
      if (gen === generation && entity) applyEntity(entity);
    } catch {
      // The name falls back to the entity ID; the items are what matter.
    }
  }

  async function loadItems(gen: number): Promise<void> {
    try {
      const res = await bridge().api.get(`${base}/ha/todo/${encodeURIComponent(cfg.entityId)}`);
      if (gen !== generation) return;
      const body = res?.items ? res : (res?.data ?? res);
      items = Array.isArray(body?.items) ? body.items : [];
      loaded = true;
      error = "";
    } catch (e: any) {
      if (gen !== generation) return;
      error = e?.message || "cannot read the list";
    }
  }

  // ---- Writes ----

  let available = $derived(entityState !== "unavailable");
  let writable = $derived(!cfg.displayOnly && !denied && hostOperates() && available);

  async function service(service: string, data: Record<string, unknown>): Promise<boolean> {
    try {
      await bridge().api.post(`${base}/ha/service`, {
        domain: "todo",
        service,
        service_data: data,
        target: { entity_id: cfg.entityId },
      });
      actionError = "";
      return true;
    } catch (e: any) {
      if (e?.code === "FORBIDDEN" || e?.code === "UNAUTHORIZED") {
        denied = true;
        return false;
      }
      actionError = e?.message || "the change was not saved";
      if (actionTimer) clearTimeout(actionTimer);
      actionTimer = setTimeout(() => (actionError = ""), 4000);
      return false;
    }
  }

  async function toggle(item: TodoItem): Promise<void> {
    if (!writable || pending[item.uid]) return;
    const gen = generation;
    const status = item.status === "completed" ? "needs_action" : "completed";
    pending = { ...pending, [item.uid]: true };
    // The tick shows at once; the refetch below is what settles it.
    items = items.map((i) => (i.uid === item.uid ? { ...i, status } : i));
    await service("update_item", { item: item.uid, status });
    const settled = { ...pending };
    delete settled[item.uid];
    pending = settled;
    if (gen === generation) await loadItems(gen);
  }

  async function add(event: Event): Promise<void> {
    event.preventDefault();
    const text = draft.trim();
    if (!text || adding || !writable) return;
    const gen = generation;
    adding = true;
    const ok = await service("add_item", { item: text });
    adding = false;
    if (ok) draft = "";
    if (gen === generation) await loadItems(gen);
  }

  // ---- Rendering ----

  let heading = $derived(cfg.title || entityName || cfg.entityId);

  // Open items come first, in Home Assistant's order, which is the order the
  // list's owner arranged them in.
  let ordered = $derived([
    ...items.filter((i) => i.status !== "completed"),
    ...(cfg.showCompleted ? items.filter((i) => i.status === "completed") : []),
  ]);
  let shown = $derived(cfg.maxItems > 0 ? ordered.slice(0, cfg.maxItems) : ordered);
  let hidden = $derived(ordered.length - shown.length);
  let openCount = $derived(items.filter((i) => i.status !== "completed").length);
</script>

<div class="frame">
  <div class="todo">
    {#if !bound}
      <span class="hint center">Pick a to-do list in the widget settings.</span>
    {:else}
      <header>
        <span class="heading">{heading}</span>
        {#if loaded}<span class="count">{openCount}</span>{/if}
      </header>

      {#if error}
        <span class="error">{error}</span>
      {:else if !loaded}
        <span class="hint">Loading the list</span>
      {:else if shown.length === 0}
        <span class="hint">Nothing to do.</span>
      {:else}
        <ul>
          {#each shown as item (item.uid)}
            <li class:done={item.status === "completed"}>
              {#if writable}
                <button type="button" class="tick" aria-label={item.status === "completed" ? `Reopen ${item.summary}` : `Complete ${item.summary}`}
                        disabled={!!pending[item.uid]} onclick={() => toggle(item)}>
                  {#if item.status === "completed"}
                    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5" /></svg>
                  {/if}
                </button>
              {:else}
                <span class="tick static" aria-hidden="true">
                  {#if item.status === "completed"}
                    <svg viewBox="0 0 24 24"><path d="M5 12.5l4.5 4.5L19 7.5" /></svg>
                  {/if}
                </span>
              {/if}
              <span class="text">
                <span class="summary">{item.summary}</span>
                {#if item.due}<span class="due">{dueLabel(item.due)}</span>{/if}
              </span>
            </li>
          {/each}
        </ul>
        {#if hidden > 0}
          <span class="more">{hidden} more</span>
        {/if}
      {/if}

      {#if actionError}
        <span class="error small">{actionError}</span>
      {/if}

      {#if writable && cfg.allowAdd && loaded && !error}
        <form class="add" onsubmit={add}>
          <input class="add-input" type="text" placeholder="Add an item" bind:value={draft}
                 disabled={adding} aria-label="New item" />
          <button type="submit" class="add-button" disabled={adding || !draft.trim()} aria-label="Add">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          </button>
        </form>
      {/if}
    {/if}
  </div>
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  /* Text scales with the cell's width so the list reads from across a room
     on a wall panel and still fits a phone. */
  .frame {
    width: 100%;
    height: 100%;
    container-type: size;
    overflow: hidden;
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
  }

  .todo {
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: 0.5em;
    padding: 0.8em;
    font-size: clamp(14px, 5.5cqw, 26px);
  }

  header {
    display: flex;
    align-items: baseline;
    gap: 0.5em;
    flex-shrink: 0;
  }

  .heading {
    flex: 1;
    min-width: 0;
    font-size: 1.15em;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .count {
    font-size: 0.85em;
    color: var(--theme-subtext, #a6adc8);
    font-variant-numeric: tabular-nums;
  }

  ul {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    margin: 0;
    padding: 0;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 0.15em;
  }

  li {
    display: flex;
    align-items: center;
    gap: 0.6em;
    padding: 0.25em 0;
    border-bottom: 1px solid color-mix(in srgb, var(--theme-border, #45475a) 50%, transparent);
  }

  li.done .summary {
    text-decoration: line-through;
    color: var(--theme-subtext, #a6adc8);
  }

  .tick {
    flex-shrink: 0;
    width: 1.3em;
    height: 1.3em;
    padding: 0.1em;
    border: 2px solid var(--theme-border, #45475a);
    border-radius: 50%;
    background: transparent;
    color: var(--theme-green, var(--theme-success, #a6e3a1));
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
  }

  .tick:hover:not(:disabled):not(.static) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .tick:disabled {
    opacity: 0.5;
  }

  .tick.static {
    cursor: default;
  }

  li.done .tick {
    border-color: var(--theme-green, var(--theme-success, #a6e3a1));
  }

  svg {
    width: 100%;
    height: 100%;
    fill: none;
    stroke: currentColor;
    stroke-width: 3;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .summary {
    overflow-wrap: anywhere;
  }

  .due {
    font-size: 0.7em;
    color: var(--theme-subtext, #a6adc8);
  }

  .more {
    flex-shrink: 0;
    font-size: 0.75em;
    color: var(--theme-subtext, #a6adc8);
  }

  .add {
    display: flex;
    gap: 0.4em;
    flex-shrink: 0;
  }

  .add-input {
    flex: 1;
    min-width: 0;
    padding: 0.35em 0.55em;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 6px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font: inherit;
    font-size: 0.85em;
    outline: none;
  }

  .add-input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .add-button {
    flex-shrink: 0;
    width: 2em;
    padding: 0.35em;
    border: 1px solid var(--theme-accent, #89b4fa);
    border-radius: 6px;
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    cursor: pointer;
  }

  .add-button:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .hint {
    font-size: 0.85em;
    opacity: 0.6;
  }

  .hint.center {
    margin: auto;
    text-align: center;
  }

  .error {
    font-size: 0.85em;
    color: var(--theme-error, #f38ba8);
  }

  .error.small {
    font-size: 0.7em;
  }
</style>
