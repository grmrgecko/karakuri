<script lang="ts" generics="T extends EntitySource">
  import type { Snippet } from "svelte";
  import SourceSelect from "./SourceSelect.svelte";
  import AppearanceEditor from "./AppearanceEditor.svelte";
  import { hasReference, resolve } from "./variables";
  import {
    drawsLocation,
    triggerPresses,
    type EntitySource,
    type SourceKind,
    type TriggerPress,
  } from "./entitySource";
  import {
    filterIcons,
    iconFontStack,
    iconGlyph,
    loadIcons,
    type IconEntry,
  } from "./icons";

  // BindingPanel is the settings panel of a widget that is one binding: its
  // source, the fields that source needs, its icon, and its appearance. Entity
  // Control and Button wrap it in their own config elements and add their own
  // switches through `extras`, so a binding is set up the same way in both.
  let {
    config: configAttr = "{}",
    parse,
    keep = [],
    extras,
  }: {
    config: string | Record<string, unknown>;
    /** parse reads the host's attribute into the widget's config with its defaults. */
    parse: (attr: string | Record<string, unknown>) => T;
    /** keep names the widget's own fields that survive a change of source. */
    keep?: (keyof T)[];
    extras?: Snippet<[T, (patch: Partial<T>) => void]>;
  } = $props();

  interface ConnectionEntry {
    id: string;
    name: string;
    kind: string;
  }
  interface EntityEntry {
    entity_id: string;
    friendly_name?: string;
    state?: string;
  }
  interface ControlEntry {
    id: string;
    name: string;
    kind: string;
  }
  interface TriggerEntry {
    flow_id: string;
    flow_name: string;
    name: string;
  }
  interface DeviceEntry {
    id: string;
    name: string;
    control?: string;
    state?: Record<string, unknown>;
  }

  let cfg = $state<T>(parse("{}"));
  let host = $state<HTMLElement | null>(null);

  let connections = $state<ConnectionEntry[]>([]);
  let controls = $state<ControlEntry[]>([]);
  let triggers = $state<TriggerEntry[]>([]);
  let devices = $state<DeviceEntry[]>([]);
  // Entities are fetched per Home Assistant connection and cached.
  let entities = $state<Record<string, EntityEntry[]>>({});
  let entityError = $state<string>("");
  let entityOpen = $state<boolean>(false);

  // The icon pack comes from the host, so the picker offers exactly the icons
  // the rest of the UI draws with. It completes like the entity field: the
  // input holds the class name, and the list is the pack filtered by it.
  const icons: IconEntry[] = loadIcons();
  const fontStack = iconFontStack();
  let iconOpen = $state<boolean>(false);
  let iconMatches = $derived(filterIcons(icons, cfg.icon || ""));
  let iconPreview = $derived(iconGlyph(cfg.icon || "", icons));

  // Every edit round trips through a save and comes back as a new config
  // attribute, one keystroke behind what has been typed since. Adopting those
  // echoes would rewrite the field under the cursor, so the attribute seeds
  // this panel once per widget and the panel owns its state from then on. The
  // host injects the placement's ID, which is what says "this is a different
  // widget now" rather than "this is your own edit coming back".
  let seededFor: string | null = null;

  $effect(() => {
    const widgetId = String(rawConfig(configAttr).dashboardWidgetId ?? "");
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parse(configAttr);
  });

  // rawConfig reads the host's attribute without dropping the injected IDs.
  function rawConfig(attr: string | Record<string, unknown>): Record<string, unknown> {
    if (typeof attr === "string") {
      try {
        return JSON.parse(attr) as Record<string, unknown>;
      } catch {
        return {};
      }
    }
    return (attr ?? {}) as Record<string, unknown>;
  }

  let loaded = false;
  $effect(() => {
    if (loaded) return;
    loaded = true;
    void loadSources();
  });

  const bridge = () => (window as any).__karakuri;

  async function loadSources(): Promise<void> {
    try {
      const res = await bridge().api.get("/connections");
      connections = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      connections = [];
    }
    try {
      const res = await bridge().api.get("/controls");
      controls = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      controls = [];
    }
    try {
      const res = await bridge().api.get("/widget_triggers");
      triggers = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      triggers = [];
    }
    try {
      const res = await bridge().api.get("/mqtt_devices");
      devices = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      // Reading devices needs connection read; without it a binding can still
      // be configured against a topic.
      devices = [];
    }
    if (cfg.source === "ha" && cfg.connectionId) void loadEntities(resolve(cfg.connectionId));
  }

  // deviceFields are the values a device is known to decode, offered as
  // completions. A field the device has not reported yet can still be typed:
  // the list is what has arrived, not what exists.
  function deviceFields(id: string | undefined): string[] {
    const device = devices.find((d) => d.id === id);
    return Object.keys(device?.state ?? {}).sort();
  }

  async function loadEntities(conn: string): Promise<void> {
    if (!conn || hasReference(conn) || entities[conn]) return;
    entityError = "";
    try {
      const res = await bridge().api.get(`/connections/${conn}/ha/entities`);
      entities[conn] = Array.isArray(res) ? res : (res?.data ?? []);
    } catch (e: any) {
      entities[conn] = [];
      entityError = e?.message || "Failed to load entities";
    }
  }

  // The entity field completes rather than selects: the input holds the entity
  // ID itself, so a saved binding reads back immediately whether or not the
  // connection's entity list has arrived.
  function filteredEntities(): EntityEntry[] {
    const list = entities[resolve(cfg.connectionId || "")] || [];
    // A template filters by the text before its first reference.
    const typed = hasReference(cfg.entityId) ? (cfg.entityId || "").split("${")[0] : cfg.entityId || "";
    const term = typed.trim().toLowerCase();
    // An exact match is the binding as already configured, so offer the whole
    // list rather than the one entry the author is trying to replace.
    const exact = term && list.some((e) => e.entity_id.toLowerCase() === term);
    const matches =
      term && !exact
        ? list.filter(
            (e) =>
              e.entity_id.toLowerCase().includes(term) ||
              (e.friendly_name || "").toLowerCase().includes(term),
          )
        : list;
    return matches.slice(0, 50);
  }

  let haConnections = $derived(connections.filter((c) => c.kind === "homeassistant"));
  let mqttConnections = $derived(connections.filter((c) => c.kind === "mqtt"));
  let flowNames = $derived([...new Set(triggers.map((t) => t.flow_name))]);

  function dispatch(): void {
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }),
    );
  }

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  function update(patch: Partial<EntitySource> | Partial<T>): void {
    cfg = { ...cfg, ...patch } as T;
    dispatch();
  }

  // Switching source clears the fields of the one being left: a topic left
  // behind an entity ID would still be read by the binding key.
  function setSource(source: SourceKind): void {
    const kept: Partial<T> = {};
    for (const key of keep) kept[key] = cfg[key];
    cfg = {
      ...parse("{}"),
      ...kept,
      source,
      name: cfg.name,
      icon: cfg.icon,
      states: cfg.states,
      format: cfg.format,
      precision: cfg.precision,
    } as T;
    if (source === "ha" && haConnections.length === 1) {
      cfg.connectionId = haConnections[0].id;
      void loadEntities(resolve(cfg.connectionId));
    }
    if (source === "flow-trigger") cfg.triggers = [{ id: newPressId(), label: "" }];
    if (source === "mqtt") {
      // A declared device is the better default when there is one: it has
      // already decoded its topics and says what it is.
      if (devices.length > 0) cfg.deviceId = devices[0].id;
      if (mqttConnections.length === 1) cfg.connectionId = mqttConnections[0].id;
    }
    dispatch();
  }

  // ---- Flow trigger buttons ----
  //
  // A trigger tile is the one place a widget declares its own buttons, because
  // a trigger has no state anywhere else to declare them in: On and Off may be
  // two entry points of one flow or of two, and only the dashboard knows they
  // belong together.

  function newPressId(): string {
    return Math.random().toString(36).slice(2, 10);
  }

  let presses = $derived(triggerPresses(cfg));

  function setPresses(next: TriggerPress[]): void {
    update({ triggers: next, flowId: undefined, trigger: undefined });
  }

  function addPress(): void {
    setPresses([...presses, { id: newPressId(), label: "" }]);
  }

  function updatePress(id: string, patch: Partial<TriggerPress>): void {
    setPresses(presses.map((p) => (p.id === id ? { ...p, ...patch } : p)));
  }

  function removePress(id: string): void {
    setPresses(presses.filter((p) => p.id !== id));
  }

  // The label defaults to the trigger's name, read from the stored press rather
  // than the one this block was rendered with: typing a label and picking a
  // trigger in the same tick would otherwise overwrite what was just typed.
  function selectPress(pressId: string, index: string): void {
    const entry = triggers[Number(index)];
    if (!entry) {
      updatePress(pressId, { flowId: "", trigger: "" });
      return;
    }
    const current = presses.find((p) => p.id === pressId);
    updatePress(pressId, {
      flowId: entry.flow_id,
      trigger: entry.name,
      label: current?.label || entry.name,
    });
  }

  function pressIndex(press: TriggerPress): string {
    return String(triggers.findIndex((t) => t.flow_id === press.flowId && t.name === press.trigger));
  }

  async function editPressPayload(press: TriggerPress): Promise<void> {
    const edit = bridge()?.editDocument;
    if (!edit) return;
    const next = await edit({
      title: `${press.label || press.trigger || "Trigger"} parameters`,
      value: press.payload || "",
      format: press.payloadFormat || "json",
      saveLabel: "Save parameters",
      requireObject: true,
    });
    if (next === null) return;
    updatePress(press.id, { payload: next });
  }

</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Source</span>
    <div class="source-buttons">
      <button type="button" class="source" class:active={cfg.source === "control"}
              onclick={() => setSource("control")}>Control</button>
      <button type="button" class="source" class:active={cfg.source === "ha"}
              onclick={() => setSource("ha")}>Home Assistant</button>
      <button type="button" class="source" class:active={cfg.source === "mqtt"}
              onclick={() => setSource("mqtt")}>MQTT</button>
      <button type="button" class="source" class:active={cfg.source === "flow-trigger"}
              onclick={() => setSource("flow-trigger")}>Flow trigger</button>
    </div>
  </div>

  {#if cfg.source === "control"}
    <div class="field">
      <label class="label" for="ec-control">Control</label>
      <SourceSelect id="ec-control" value={cfg.controlId || ""} placeholder="Select a control..."
                    options={controls.map((c) => ({ value: c.id, label: c.name }))}
                    onchange={(id) => update({ controlId: id })} />
      {#if controls.length === 0}
        <span class="hint">No controls yet; define one under Settings → Controls.</span>
      {/if}
    </div>
  {:else if cfg.source === "ha"}
    <div class="field">
      <label class="label" for="ec-ha-conn">Connection</label>
      <SourceSelect id="ec-ha-conn" value={cfg.connectionId || ""} placeholder="Select a connection..."
                    options={haConnections.map((c) => ({ value: c.id, label: c.name }))}
                    onchange={(id) => {
                      update({ connectionId: id, entityId: hasReference(cfg.entityId) ? cfg.entityId : "" });
                      void loadEntities(resolve(id));
                    }} />
    </div>
    <div class="field entity-field">
      <label class="label" for="ec-ha-entity">Entity</label>
      <input id="ec-ha-entity" class="input mono" autocomplete="off"
             value={cfg.entityId || ""} placeholder="light.kitchen"
             disabled={!cfg.connectionId}
             oninput={(e: Event) => {
               update({ entityId: (e.target as HTMLInputElement).value });
               entityOpen = true;
             }}
             onfocus={() => {
               entityOpen = true;
               void loadEntities(resolve(cfg.connectionId || ""));
             }}
             onblur={() => {
               // Delay so an option's mousedown wins over the close.
               setTimeout(() => { entityOpen = false; }, 150);
             }} />
      {#if hasReference(cfg.entityId)}
        <span class="hint">Now <span class="mono">{resolve(cfg.entityId || "")}</span></span>
      {/if}
      {#if !cfg.connectionId}
        <span class="hint">Select a connection first to browse entities.</span>
      {:else if entityOpen}
        {@const options = filteredEntities()}
        {#if options.length > 0}
          <ul class="entity-dropdown">
            {#each options as entity (entity.entity_id)}
              <li>
                <button type="button" class="entity-option"
                        onmousedown={(e: Event) => {
                          e.preventDefault();
                          update({ entityId: entity.entity_id });
                          entityOpen = false;
                        }}>
                  <span class="entity-id mono">{entity.entity_id}</span>
                  <span class="entity-meta">
                    {#if entity.friendly_name}
                      <span class="entity-name">{entity.friendly_name}</span>
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
  {:else if cfg.source === "mqtt"}
    <div class="field">
      <label class="label" for="ec-mqtt-device">Device</label>
      <SourceSelect id="ec-mqtt-device" value={cfg.deviceId || ""} placeholder="Select a device..."
                    options={devices.map((d) => ({ value: d.id, label: d.name }))}
                    onchange={(id) => update({ deviceId: id, deviceField: "" })} />
      <span class="hint">
        {#if devices.length === 0}
          None declared yet — add one in Settings → MQTT Devices.
        {:else}
          The device owns its topics, how its messages are read, what it is drawn with, and how a
          command is built. Declared in Settings → MQTT Devices.
        {/if}
      </span>
    </div>
    {#if cfg.deviceId}
      <div class="field">
        <label class="label" for="ec-mqtt-field">Value</label>
        <input id="ec-mqtt-field" class="input mono" list="ec-mqtt-fields"
               value={cfg.deviceField || ""} placeholder="all of them"
               oninput={(e: Event) =>
                 update({ deviceField: (e.target as HTMLInputElement).value })} />
        <datalist id="ec-mqtt-fields">
          {#each deviceFields(cfg.deviceId) as field (field)}
            <option value={field}></option>
          {/each}
        </datalist>
        <span class="hint">
          Which of the device's values this tile shows. The list is what it has decoded so far; a
          value it has not sent yet can still be named.
        </span>
      </div>
    {/if}
  {:else if cfg.source === "flow-trigger"}
    <!-- A tile of triggers is On and Off side by side. Each button names its
         own entry point, so the two halves of one action can live in different
         flows and still read as one control. -->
    <div class="field">
      <span class="label">Buttons</span>
      {#each presses as press (press.id)}
        <div class="press">
          <div class="field pair">
            <span class="half">
              <input class="input" value={press.label || ""} placeholder="Label"
                     oninput={(e: Event) =>
                       updatePress(press.id, { label: (e.target as HTMLInputElement).value })} />
            </span>
            <button class="mini danger" type="button" title="Remove"
                    onclick={() => removePress(press.id)}>×</button>
          </div>
          <select class="input" value={pressIndex(press)}
                  onchange={(e: Event) =>
                    selectPress(press.id, (e.target as HTMLSelectElement).value)}>
            <option value="-1">Select a trigger...</option>
            {#each flowNames as flowName (flowName)}
              <optgroup label={flowName}>
                {#each triggers as entry, i (entry.flow_id + "/" + entry.name)}
                  {#if entry.flow_name === flowName}
                    <option value={String(i)}>{entry.name}</option>
                  {/if}
                {/each}
              </optgroup>
            {/each}
          </select>
          <button class="document" type="button" onclick={() => editPressPayload(press)}>
            {press.payload?.trim() ? "Edit parameters" : "No parameters"}
          </button>
          <label class="check">
            <input type="checkbox" checked={!!press.confirmFirst}
                   onchange={(e: Event) =>
                     updatePress(press.id, {
                       confirmFirst: (e.target as HTMLInputElement).checked,
                     })} />
            <span>Require a second tap</span>
          </label>
        </div>
      {/each}
      <button class="add" type="button" onclick={() => addPress()}>Add a button</button>
      {#if triggers.length === 0}
        <span class="hint">
          No widget triggers yet. Add a Widget Trigger node to a flow and deploy it.
        </span>
      {/if}
    </div>
  {/if}

  <div class="field">
    <label class="label" for="ec-name">Name</label>
    <input id="ec-name" class="input" value={cfg.name || ""} placeholder="from the source"
           oninput={(e: Event) => update({ name: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field icon-field">
    <label class="label" for="ec-icon">Icon</label>
    <div class="icon-input">
      <span class="icon-preview" class:glyph={iconPreview.glyph}
            style:font-family={iconPreview.glyph ? fontStack : null}>{iconPreview.text}</span>
      <input id="ec-icon" class="input" autocomplete="off" value={cfg.icon || ""}
             placeholder={icons.length ? "la-home" : "emoji"}
             oninput={(e: Event) => {
               update({ icon: (e.target as HTMLInputElement).value });
               iconOpen = true;
             }}
             onfocus={() => { iconOpen = true; }}
             onblur={() => {
               // Delay so an option's mousedown wins over the close.
               setTimeout(() => { iconOpen = false; }, 150);
             }} />
    </div>
    {#if icons.length === 0}
      <span class="hint">This host offers no icon pack; type any text or emoji.</span>
    {:else if iconOpen && iconMatches.length > 0}
      <ul class="icon-dropdown">
        {#each iconMatches as entry (entry.name)}
          <li>
            <button type="button" class="icon-option"
                    onmousedown={(e: Event) => {
                      e.preventDefault();
                      update({ icon: entry.name });
                      iconOpen = false;
                    }}>
              <span class="icon-glyph" style:font-family={fontStack}>{entry.char}</span>
              <span class="icon-name">{entry.name}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else if iconOpen}
      <span class="hint">No icon matches that.</span>
    {/if}
  </div>

  {#if drawsLocation(cfg, controls, devices)}
    <label class="check">
      <input type="checkbox" checked={!!cfg.showCoordinates}
             onchange={(e: Event) => update({ showCoordinates: (e.target as HTMLInputElement).checked })} />
      <span>Show coordinates</span>
    </label>
    <span class="hint">
      Shows latitude, longitude, and accuracy under the zone when the source reports them.
    </span>
  {/if}

  {#if cfg.source !== "flow-trigger"}
    <label class="check">
      <input type="checkbox" checked={!!cfg.displayOnly}
             onchange={(e: Event) =>
               update({ displayOnly: (e.target as HTMLInputElement).checked })} />
      <span>Display only</span>
    </label>
    <span class="hint">
      What this tile is drawn with comes from the source: a Home Assistant entity from its domain,
      a control and an MQTT device from the kind they were defined as. Display only shows the
      reading and stops anyone pressing it, here and on the server.
    </span>
  {/if}

  <AppearanceEditor states={cfg.states} format={cfg.format} precision={cfg.precision} idPrefix="tile"
                    onchange={(patch) => update(patch)} />

  {@render extras?.(cfg, update)}
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
  }

  .field.pair {
    flex-direction: row;
    gap: 0.5rem;
  }

  /* Each press is its own block: a label, the trigger it starts, and its
     parameters. Stacked without a boundary they read as one long form. */
  .press {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.5rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
  }

  .half {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    flex: 1;
    min-width: 0;
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

  .source-buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }

  .source {
    padding: 0.3rem 0.55rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.78rem;
    cursor: pointer;
  }

  .source:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .source.active {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  /* The entity and icon fields complete as they are typed, so the dropdown
     floats over the fields below rather than pushing them down. */
  .entity-field,
  .icon-field {
    position: relative;
  }

  .icon-input {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  /* The preview is what the tile will draw, beside the name that produced it —
     the pack's names are close enough to each other that the glyph is the only
     honest confirmation. */
  .icon-preview {
    flex-shrink: 0;
    width: 1.6rem;
    height: 1.6rem;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.1rem;
    color: var(--theme-accent, #89b4fa);
  }

  .icon-preview.glyph,
  .icon-glyph {
    font-weight: 900;
    font-synthesis: none;
  }

  .icon-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 30;
    max-height: 240px;
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

  .icon-option {
    display: flex;
    align-items: center;
    gap: 0.5rem;
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

  .icon-option:hover {
    background: var(--theme-surface1, #45475a);
  }

  .icon-glyph {
    flex-shrink: 0;
    width: 1.2rem;
    font-size: 1rem;
    text-align: center;
    color: var(--theme-accent, #89b4fa);
  }

  .icon-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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

  /* An entity ID is longer than this panel is wide, so an option reads as two
     lines rather than a row that clips. */
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

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }

  .mini {
    width: 1.7rem;
    border: none;
    background: transparent;
    color: var(--theme-subtext, #a6adc8);
    font-family: inherit;
    font-size: 0.8rem;
    cursor: pointer;
  }

  .mini.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  .add {
    padding: 0.3rem 0.55rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.78rem;
    cursor: pointer;
    align-self: flex-start;
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .document {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-crust, #11111b);
    color: var(--theme-subtext, #a6adc8);
    font-family: inherit;
    font-size: 0.78rem;
    text-align: left;
    cursor: pointer;
  }

  .document:hover {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-text, #cdd6f4);
  }
</style>
