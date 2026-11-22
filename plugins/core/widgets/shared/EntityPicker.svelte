<script lang="ts" module>
  // Connections and entity lists are read once per page and shared by every
  // picker on it: a history graph with several series opens one per row.
  interface ConnectionEntry {
    id: string;
    name: string;
    kind: string;
  }
  interface EntityEntry {
    entity_id: string;
    friendly_name?: string;
    state?: string;
    area?: string;
    floor?: string;
    device?: string;
    labels?: string[];
    label_names?: string[];
  }

  let connectionsRequest: Promise<ConnectionEntry[]> | null = null;
  const entityRequests = new Map<string, Promise<EntityEntry[]>>();

  function unwrap<T>(res: any): T[] {
    return Array.isArray(res) ? res : (res?.data ?? []);
  }

  function loadConnections(): Promise<ConnectionEntry[]> {
    if (!connectionsRequest) {
      connectionsRequest = (window as any).__karakuri.api
        .get("/connections")
        .then((res: any) => unwrap<ConnectionEntry>(res).filter((c) => c.kind === "homeassistant"))
        .catch(() => {
          connectionsRequest = null;
          return [];
        });
    }
    return connectionsRequest as Promise<ConnectionEntry[]>;
  }

  function loadEntities(conn: string, hidden = false): Promise<EntityEntry[]> {
    const key = `${conn}|${hidden}`;
    let request = entityRequests.get(key);
    if (!request) {
      request = (window as any).__karakuri.api
        .get(`/connections/${encodeURIComponent(conn)}/ha/entities${hidden ? "?hidden=1" : ""}`)
        .then((res: any) => unwrap<EntityEntry>(res))
        .catch((e: any) => {
          entityRequests.delete(key);
          throw e;
        });
      entityRequests.set(key, request as Promise<EntityEntry[]>);
    }
    return request as Promise<EntityEntry[]>;
  }
</script>

<script lang="ts">
  import { onDestroy } from "svelte";
  import SourceSelect from "./SourceSelect.svelte";
  import { hasReference, resolve, watchVariables, type BridgeVariables } from "./variables";

  // EntityPicker chooses a Home Assistant connection and one of its entities.
  // Either may be a dashboard variable reference: suggestions then come from
  // the connection the current values name, and the field says what the
  // entity reference stands for.
  // The entity field completes rather than selects, so a saved binding reads
  // back before the entity list arrives and an entity outside the suggested
  // domain can still be typed.
  let {
    connectionId = "",
    entityId = "",
    domain = "",
    placeholder = "",
    idPrefix = "picker",
    onchange,
  }: {
    connectionId?: string;
    entityId?: string;
    /** domain narrows the suggestions to entity IDs starting with "<domain>.". */
    domain?: string;
    placeholder?: string;
    idPrefix?: string;
    onchange: (patch: { connectionId?: string; entityId?: string }) => void;
  } = $props();

  let connections = $state<ConnectionEntry[]>([]);
  let entities = $state<EntityEntry[]>([]);
  let entityError = $state("");
  let open = $state(false);
  // showHidden lists the entities Home Assistant hides from its own pickers,
  // such as the parts of a group.
  let showHidden = $state(false);
  let loadedFor = "";

  let variableState = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  const stopVariables = watchVariables((next) => (variableState = next));
  onDestroy(stopVariables);
  let liveConnection = $derived(resolve(connectionId, variableState));
  let resolvedEntity = $derived(hasReference(entityId) ? resolve(entityId, variableState) : "");

  $effect(() => {
    void loadConnections().then((list) => {
      connections = list;
      // A house with one Home Assistant is the common case, so the only
      // choice is made for the author.
      if (!connectionId && list.length === 1) onchange({ connectionId: list[0].id, entityId: "" });
    });
  });

  $effect(() => {
    const conn = liveConnection;
    const key = `${conn}|${showHidden}`;
    if (!conn || hasReference(conn) || key === loadedFor) return;
    loadedFor = key;
    entityError = "";
    loadEntities(conn, showHidden)
      .then((list) => {
        if (loadedFor === key) entities = list;
      })
      .catch((e: any) => {
        if (loadedFor !== key) return;
        entities = [];
        entityError = e?.message || "Failed to load entities";
      });
  });

  let matches = $derived.by(() => {
    const prefix = domain ? `${domain}.` : "";
    const list = prefix ? entities.filter((e) => e.entity_id.startsWith(prefix)) : entities;
    // A template filters by the text before its first reference, so
    // person.${person} still suggests the person entities.
    const typed = hasReference(entityId) ? (entityId || "").split("${")[0] : entityId || "";
    const term = typed.trim().toLowerCase();
    // An exact match is the binding as already configured, so offer the whole
    // list rather than the one entry the author is trying to replace.
    const exact = term && list.some((e) => e.entity_id.toLowerCase() === term);
    const filtered =
      term && !exact
        ? list.filter((e) =>
            [e.entity_id, e.friendly_name, e.area, e.floor, e.device, ...(e.labels ?? []), ...(e.label_names ?? [])].some((v) =>
              (v || "").toLowerCase().includes(term),
            ),
          )
        : list;
    return filtered.slice(0, 80);
  });

  // groupOf is the heading an entity sits under; the server orders the list
  // by floor and area, so headings change only between groups.
  function groupOf(e: EntityEntry): string {
    if (!e.area) return "No area";
    return e.floor ? `${e.floor} · ${e.area}` : e.area;
  }
</script>

<div class="field">
  <label class="label" for="{idPrefix}-conn">Connection</label>
  <SourceSelect id="{idPrefix}-conn" value={connectionId} placeholder="Select a connection..."
                options={connections.map((c) => ({ value: c.id, label: c.name }))}
                onchange={(id) => onchange({ connectionId: id, entityId: hasReference(entityId) ? entityId : "" })} />
</div>
<div class="field entity-field">
  <label class="label" for="{idPrefix}-entity">Entity</label>
  <input id="{idPrefix}-entity" class="input mono" autocomplete="off"
         value={entityId} placeholder={placeholder || (domain ? `${domain}.example` : "sensor.example")}
         disabled={!connectionId}
         oninput={(e: Event) => {
           onchange({ entityId: (e.target as HTMLInputElement).value });
           open = true;
         }}
         onfocus={() => (open = true)}
         onblur={() => {
           // Delay so an option's mousedown wins over the close.
           setTimeout(() => (open = false), 150);
         }} />
  <label class="check">
    <input type="checkbox" checked={showHidden}
           onchange={(e: Event) => (showHidden = (e.target as HTMLInputElement).checked)} />
    <span>Show hidden</span>
  </label>
  {#if resolvedEntity && resolvedEntity !== entityId}
    <span class="hint">Now <span class="mono">{resolvedEntity}</span></span>
  {/if}
  {#if !connectionId}
    <span class="hint">Select a connection first to browse entities.</span>
  {:else if open}
    {#if matches.length > 0}
      <ul class="entity-dropdown">
        {#each matches as entity, i (entity.entity_id)}
          {#if i === 0 || groupOf(matches[i - 1]) !== groupOf(entity)}
            <li class="entity-group">{groupOf(entity)}</li>
          {/if}
          <li>
            <button type="button" class="entity-option"
                    onmousedown={(e: Event) => {
                      e.preventDefault();
                      onchange({ entityId: entity.entity_id });
                      open = false;
                    }}>
              <span class="entity-id mono">{entity.entity_id}</span>
              <span class="entity-meta">
                {#if entity.friendly_name}
                  <span class="entity-name">{entity.friendly_name}</span>
                {/if}
                {#if entity.device && entity.device !== entity.friendly_name}
                  <span class="entity-name">{entity.device}</span>
                {/if}
                {#if entity.state}
                  <span class="entity-state">{entity.state}</span>
                {/if}
              </span>
            </button>
          </li>
        {/each}
      </ul>
    {:else if entityError}
      <span class="hint error">{entityError}</span>
    {:else}
      <span class="hint">No entity matches that.</span>
    {/if}
  {/if}
</div>

<style>
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

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .hint.error {
    color: var(--theme-error, #f38ba8);
    opacity: 1;
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

  .mono {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.78rem;
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .entity-group {
    padding: 0.3rem 0.45rem 0.1rem;
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--theme-subtext, #a6adc8);
  }

  /* The dropdown floats over the fields below rather than pushing them down. */
  .entity-field {
    position: relative;
  }

  .entity-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 30;
    max-height: 220px;
    overflow-x: hidden;
    overflow-y: auto;
    margin: 2px 0 0;
    padding: 2px;
    list-style: none;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-mantle, #181825);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.35);
  }

  .entity-option {
    display: flex;
    flex-direction: column;
    width: 100%;
    padding: 0.25rem 0.4rem;
    border: none;
    border-radius: 3px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.75rem;
    text-align: left;
    cursor: pointer;
  }

  .entity-option:hover {
    background: var(--theme-surface1, #45475a);
  }

  .entity-id,
  .entity-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .entity-meta {
    display: flex;
    align-items: baseline;
    gap: 0.4rem;
    font-size: 0.68rem;
  }

  .entity-name {
    min-width: 0;
    opacity: 0.6;
  }

  .entity-state {
    margin-left: auto;
    flex-shrink: 0;
    opacity: 0.4;
  }
</style>
