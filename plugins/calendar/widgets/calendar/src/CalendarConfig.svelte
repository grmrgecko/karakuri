<svelte:options customElement="calendar-config" />

<script lang="ts">
  import { untrack } from "svelte";

  // A calendar connection the widget can show, as the plugin lists it.
  interface CalendarConnection {
    id: string;
    name: string;
    kind: string;
  }

  const KIND_LABELS: Record<string, string> = {
    "calendar-ics": "ICS",
    "calendar-caldav": "CalDAV",
    "calendar-google": "Google",
  };

  let { config: configAttr = "{}" } = $props();

  // Sources are not part of the placement's config: the plugin stores them
  // next to the widget. Each names a calendar connection, which holds the
  // feed and its credentials.
  const DEFAULTS = {
    layout: "monthly",
    startOfWeek: 0,
    showTags: "",
    dashboardId: "",
    dashboardWidgetId: "",
  };

  let cfg = $state<Record<string, any>>({ ...DEFAULTS });
  let sources = $state<Record<string, any>[]>([]);
  let sourcesFor = "";
  let host = $state<Element | null>(null);

  // Calendar connections the author can choose from.
  let connections = $state<CalendarConnection[]>([]);
  let connectionsLoaded = $state(false);
  let newConnection = $state<string>("");
  let saveError = $state<string>("");

  // New source form state.
  let newName = $state<string>("");
  let newCalendars = $state<string>("");
  let newColor = $state<string>("#89b4fa");
  let newTag = $state<string>("");

  // Variables offered for the tag filter, such as ${person}.
  let variableRefs = $state<string[]>([]);
  $effect(() => {
    const watch = (window as any).__karakuri?.watchVariables;
    if (!watch) return;
    return watch((state: { variables: { name: string }[] }) => {
      variableRefs = state.variables.map((v) => "${" + v.name + "}");
    });
  });
  let saving = $state<boolean>(false);

  // Fetch the calendar connections on mount.
  $effect(() => {
    (async () => {
      try {
        const resp = await (window as any).__karakuri?.api?.get("/plugins/calendar/connections");
        connections = Array.isArray(resp) ? resp : [];
        if (connections.length > 0 && !newConnection) newConnection = connections[0].id;
      } catch {
        connections = [];
      } finally {
        connectionsLoaded = true;
      }
    })();
  });

  function connectionName(id: string): string {
    return connections.find((c) => c.id === id)?.name ?? "";
  }

  function connectionKind(id: string): string {
    const kind = connections.find((c) => c.id === id)?.kind ?? "";
    return KIND_LABELS[kind] ?? kind;
  }

  $effect(() => {
    let parsed: Record<string, any> = {};
    try {
      parsed = (typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr) ?? {};
    } catch {
      parsed = {};
    }
    // A config saved by an older panel carries the sources, keys and all;
    // saving once without them takes them out of what viewers are served.
    const { sources: stale, ...rest } = parsed;
    cfg = { ...DEFAULTS, ...rest };
    if (Array.isArray(stale)) staleSources = true;
  });

  // staleSources is set when the attribute still carries sources; the save
  // that drops them waits for the host element the event is sent from.
  let staleSources = $state(false);
  $effect(() => {
    if (!staleSources || !host) return;
    staleSources = false;
    untrack(dispatch);
  });

  // Load the stored sources once per placement.
  $effect(() => {
    const key = `${cfg.dashboardId}/${cfg.dashboardWidgetId}`;
    if (!cfg.dashboardId || !cfg.dashboardWidgetId || key === sourcesFor) return;
    sourcesFor = key;
    (async () => {
      try {
        const list = await (window as any).__karakuri?.api?.get(`/plugins/calendar/${key}/sources`);
        if (sourcesFor === key) sources = Array.isArray(list) ? list : [];
      } catch {
        if (sourcesFor === key) sources = [];
      }
    })();
  });

  function dispatch(): void {
    const event = new CustomEvent("configchange", {
      detail: { ...cfg },
      bubbles: true,
      composed: true,
    });
    host?.dispatchEvent(event);
  }

  function setHost(node: Element): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  async function saveSources(): Promise<void> {
    const { dashboardId, dashboardWidgetId } = cfg;
    if (!dashboardId || !dashboardWidgetId) return;

    saving = true;
    saveError = "";
    try {
      const path = `/plugins/calendar/${dashboardId}/${dashboardWidgetId}/set_source`;
      await (window as any).__karakuri?.api?.post(path, sources);
    } catch (err: unknown) {
      saveError = err instanceof Error ? err.message : "The sources could not be saved.";
    } finally {
      saving = false;
    }
  }

  function addSource(): void {
    if (!newConnection) return;
    const source: Record<string, unknown> = {
      connection_id: newConnection,
      name: newName.trim() || connectionName(newConnection),
      color: newColor,
    };
    if (newTag.trim()) source.tag = newTag.trim();
    const calendars = newCalendars.split(",").map((c) => c.trim()).filter(Boolean);
    if (calendars.length > 0) source.calendars = calendars;
    sources = [...sources, source];
    newName = "";
    newTag = "";
    newCalendars = "";
    saveSources();
  }

  function setTag(index: number, tag: string): void {
    sources = sources.map((s: Record<string, unknown>, i: number) => {
      if (i !== index) return s;
      const next = { ...s };
      if (tag.trim()) next.tag = tag.trim();
      else delete next.tag;
      return next;
    });
    saveSources();
  }

  function removeSource(index: number): void {
    sources = sources.filter((_: unknown, i: number) => i !== index);
    saveSources();
  }
</script>

<div class="config-form" use:setHost>
  <!-- Layout -->
  <div class="field">
    <label class="label" for="layout-select">Layout</label>
    <select
      id="layout-select"
      class="input"
      value={cfg.layout}
      onchange={(e: Event) => { cfg.layout = (e.target as HTMLSelectElement).value; dispatch(); }}
    >
      <option value="monthly">Monthly Grid</option>
      <option value="weekly">Weekly</option>
      <option value="agenda">Agenda</option>
      <option value="dayfocus">Day Focus</option>
      <option value="timeline">Timeline</option>
    </select>
  </div>

  <!-- Start of Week -->
  <div class="field">
    <label class="label" for="week-start-select">Start of Week</label>
    <select
      id="week-start-select"
      class="input"
      value={String(cfg.startOfWeek ?? 0)}
      onchange={(e: Event) => { cfg.startOfWeek = parseInt((e.target as HTMLSelectElement).value, 10); dispatch(); }}
    >
      <option value="0">Sunday</option>
      <option value="1">Monday</option>
    </select>
  </div>

  <!-- Tag filter -->
  <div class="field">
    <label class="label" for="show-tags-input">Show only tags</label>
    <input id="show-tags-input" class="input" list="show-tags-refs" placeholder="All sources"
           value={cfg.showTags ?? ""}
           onchange={(e: Event) => { cfg.showTags = (e.target as HTMLInputElement).value.trim(); dispatch(); }} />
    <datalist id="show-tags-refs">
      {#each variableRefs as ref (ref)}<option value={ref}></option>{/each}
    </datalist>
    <span class="hint">Comma-separated source tags. A variable such as {"${person}"} shows one person's calendars; empty shows every source.</span>
  </div>

  <!-- Sources List -->
  <div class="field">
    <span class="label">Calendar Sources</span>
    {#if sources.length > 0}
      <div class="source-list">
        {#each sources as source, i}
          <div class="source-item">
            <span class="source-color" style="background:{source.color || '#89b4fa'}"></span>
            <div class="source-info">
              <span class="source-name">{source.name}</span>
              {#if connectionName(source.connection_id)}
                <span class="source-badge" title={connectionName(source.connection_id)}>{connectionKind(source.connection_id)}</span>
              {:else if connectionsLoaded}
                <span class="source-badge missing" title="The connection is missing or disabled; this source shows nothing">not running</span>
              {/if}
              {#if source.calendars?.length}<span class="source-badge" title="Calendars shown">{source.calendars.join(", ")}</span>{/if}
            </div>
            <input class="input tag-input" placeholder="Tag" aria-label="Tag for {source.name}"
                   value={source.tag ?? ""}
                   onchange={(e: Event) => setTag(i, (e.target as HTMLInputElement).value)} />
            <button class="remove-btn" onclick={() => removeSource(i)} aria-label="Remove source">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
                <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        {/each}
      </div>
    {:else}
      <div class="no-sources">No sources added yet</div>
    {/if}
  </div>

  <!-- Add Source -->
  <div class="add-section">
    <span class="label">Add Source</span>

    <div class="field">
      <label class="sublabel" for="connection-select">Calendar connection</label>
      <select id="connection-select" class="input" bind:value={newConnection}>
        {#each connections as c (c.id)}
          <option value={c.id}>{c.name} ({KIND_LABELS[c.kind] ?? c.kind})</option>
        {/each}
        {#if connectionsLoaded && connections.length === 0}
          <option value="" disabled>No calendar connections</option>
        {/if}
      </select>
      {#if connectionsLoaded && connections.length === 0}
        <span class="hint">Add an ICS, CalDAV, or Google calendar under Settings, Connections, then choose it here.</span>
      {/if}
    </div>

    <div class="field">
      <label class="sublabel" for="calendars-input">Only calendars</label>
      <input id="calendars-input" class="input" placeholder="All calendars" bind:value={newCalendars} />
      <span class="hint">Comma-separated calendar names, for a connection that reads several. The connection's Test lists them.</span>
    </div>

    <div class="field">
      <label class="sublabel" for="display-name-input">Display Name</label>
      <input
        id="display-name-input"
        type="text"
        class="input"
        placeholder="The connection's name"
        bind:value={newName}
      />
    </div>

    <div class="field">
      <label class="sublabel" for="tag-input">Tag</label>
      <input id="tag-input" class="input" placeholder="james" bind:value={newTag} />
    </div>

    <div class="field">
      <label class="sublabel" for="color-picker">Color</label>
      <div class="color-row">
        <input id="color-picker" type="color" class="color-picker" bind:value={newColor} />
        <span class="color-preview" style="background:{newColor}">{newColor}</span>
      </div>
    </div>

    <button class="add-btn" onclick={addSource} disabled={saving || !newConnection}>
      {saving ? "Saving..." : "Add Source"}
    </button>
    {#if saveError}<span class="hint error">{saveError}</span>{/if}
  </div>
</div>

<style>
  .source-badge.missing {
    color: var(--theme-error, #f38ba8);
  }

  .hint.error {
    color: var(--theme-error, #f38ba8);
  }

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
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .sublabel {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
  }

  .tag-input {
    width: 6rem;
    padding: 0.2rem 0.4rem;
    font-size: 0.75rem;
  }

  .source-list {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .source-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0.5rem;
    background: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
  }

  .source-color {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .source-info {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex: 1;
    min-width: 0;
  }

  .source-name {
    font-size: 0.8rem;
    font-weight: 500;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .source-badge {
    font-size: 0.6rem;
    padding: 0.1rem 0.3rem;
    border-radius: 3px;
    background: var(--theme-surface-1, #45475a);
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    flex-shrink: 0;
  }

  .remove-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border: none;
    background: transparent;
    color: var(--theme-subtext, #a6adc8);
    cursor: pointer;
    border-radius: 3px;
    padding: 0;
    flex-shrink: 0;
  }

  .remove-btn:hover {
    color: var(--theme-error, #f38ba8);
    background: var(--theme-surface-1, #45475a);
  }

  .remove-btn svg {
    width: 14px;
    height: 14px;
  }

  .no-sources {
    font-size: 0.8rem;
    color: var(--theme-subtext, #a6adc8);
    padding: 0.5rem;
    text-align: center;
    background: var(--theme-surface0, #313244);
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: 4px;
  }

  .add-section {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding-top: 0.5rem;
    border-top: 1px solid var(--theme-border, #45475a);
  }

  .color-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .color-picker {
    width: 32px;
    height: 28px;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: none;
    cursor: pointer;
    padding: 0;
  }

  .color-preview {
    font-size: 0.75rem;
    color: var(--theme-subtext, #a6adc8);
    padding: 0.2rem 0.4rem;
    border-radius: 3px;
    font-family: monospace;
  }

  .add-btn {
    padding: 0.45rem 1rem;
    border: 1px solid var(--theme-accent, #89b4fa);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    font-family: inherit;
    transition: background 0.2s, color 0.2s;
  }

  .add-btn:hover:not(:disabled) {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
  }

  .add-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }
</style>
