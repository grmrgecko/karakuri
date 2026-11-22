<svelte:options customElement="entities-card-config" />

<script lang="ts">
  import SourceSelect from "../../shared/SourceSelect.svelte";
  import AppearanceEditor from "../../shared/AppearanceEditor.svelte";
  import { hasReference, resolve } from "../../shared/variables";
  import { drawsLocation } from "../../shared/entitySource";
  import {
    newRowId,
    parseConfig,
    triggerPresses,
    type CardConfig,
    type Row,
    type RowSource,
    type TriggerPress,
  } from "./rows";
  import {
    filterIcons,
    iconFontStack,
    iconGlyph,
    loadIcons,
    type IconEntry,
  } from "../../shared/icons";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

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

  let cfg = $state<CardConfig>({ title: "", rows: [] });
  let host = $state<HTMLElement | null>(null);

  let connections = $state<ConnectionEntry[]>([]);
  let controls = $state<ControlEntry[]>([]);
  let triggers = $state<TriggerEntry[]>([]);
  let devices = $state<DeviceEntry[]>([]);
  // Entities are fetched per Home Assistant connection and cached, so adding a
  // second row from the same connection costs nothing.
  let entities = $state<Record<string, EntityEntry[]>>({});
  let entityError = $state<string>("");
  // entityOpen is the row whose entity dropdown is showing, if any.
  let entityOpen = $state<string>("");
  let expanded = $state<string>("");

  // The icon pack comes from the host, so a row offers exactly the icons the
  // rest of the UI draws with. iconOpen is the row whose picker is showing.
  const icons: IconEntry[] = loadIcons();
  const fontStack = iconFontStack();
  let iconOpen = $state<string>("");

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
    cfg = parseConfig(configAttr);
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
      // Reading devices needs connection read; without it a row can still be
      // configured against a topic.
      devices = [];
    }
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
  // ID itself, so a saved row reads back immediately whether or not the
  // connection's entity list has arrived — and a list that never arrives costs
  // the author nothing but the completions.
  function filteredEntities(row: Row): EntityEntry[] {
    const list = entities[resolve(row.connectionId || "")] || [];
    // A template filters by the text before its first reference.
    const typed = hasReference(row.entityId) ? (row.entityId || "").split("${")[0] : row.entityId || "";
    const term = typed.trim().toLowerCase();
    // An exact match is the row as already configured, so offer the whole list
    // rather than the one entry the author is trying to replace.
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

  function dispatch(): void {
    const detail = { title: cfg.title, rows: cfg.rows };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail, bubbles: true, composed: true }),
    );
  }

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  function addRow(source: RowSource): void {
    const row: Row = { id: newRowId(), source };
    if (source === "ha" && haConnections.length === 1) row.connectionId = haConnections[0].id;
    if (source === "flow-trigger") row.triggers = [{ id: newRowId(), label: "" }];
    if (source === "mqtt") {
      // A declared device is the better default when there is one: it has
      // already decoded its topics and says what it is.
      if (devices.length > 0) row.deviceId = devices[0].id;
      if (mqttConnections.length === 1) row.connectionId = mqttConnections[0].id;
    }
    cfg.rows = [...cfg.rows, row];
    expanded = row.id;
    dispatch();
  }

  // ---- Rows from an area ----
  // Filling from an area copies the area's entities into rows once; the card
  // does not follow the area afterwards, so the author can trim it freely.

  let areaConnection = $state("");
  let areaId = $state("");
  let areas = $state<{ area_id: string; name: string }[]>([]);
  let areaNote = $state("");
  $effect(() => {
    if (!areaConnection && haConnections.length > 0) areaConnection = haConnections[0].id;
  });
  $effect(() => {
    const conn = areaConnection;
    if (!conn) return;
    void bridge()
      .api.get(`/connections/${conn}/ha/registry`)
      .then((res: any) => {
        areas = (res?.data ?? res)?.areas ?? [];
      })
      .catch(() => (areas = []));
  });

  async function addArea(): Promise<void> {
    if (!areaConnection || !areaId) return;
    areaNote = "";
    let list: any[] = [];
    try {
      const res = await bridge().api.get(`/connections/${areaConnection}/ha/entities`);
      list = Array.isArray(res) ? res : (res?.data ?? []);
    } catch (e: any) {
      areaNote = e?.message || "The area's entities could not be read";
      return;
    }
    const bound = new Set(
      cfg.rows.filter((r) => r.source === "ha" && r.connectionId === areaConnection).map((r) => r.entityId),
    );
    // Diagnostic and configuration entities are a device's plumbing, not what
    // it does; the list arrives ordered by device and name.
    const inArea = list.filter((e) => e.area_id === areaId && !e.category);
    const picked = inArea.filter((e) => !bound.has(e.entity_id));
    if (picked.length === 0) {
      areaNote =
        inArea.length === 0 ? "That area has no entities to add." : "Every entity in that area is already on the card.";
      return;
    }
    cfg.rows = [
      ...cfg.rows,
      ...picked.map((e) => ({ id: newRowId(), source: "ha" as RowSource, connectionId: areaConnection, entityId: e.entity_id })),
    ];
    areaNote = `Added ${picked.length} ${picked.length === 1 ? "row" : "rows"}.`;
    dispatch();
  }

  function updateRow(id: string, patch: Partial<Row>): void {
    cfg.rows = cfg.rows.map((r) => (r.id === id ? { ...r, ...patch } : r));
    dispatch();
  }

  function removeRow(id: string): void {
    cfg.rows = cfg.rows.filter((r) => r.id !== id);
    dispatch();
  }

  // Rows are ordered by hand: a card reads top to bottom, and the order is the
  // author's grouping rather than anything derived.
  function move(id: string, delta: number): void {
    const index = cfg.rows.findIndex((r) => r.id === id);
    const target = index + delta;
    if (index < 0 || target < 0 || target >= cfg.rows.length) return;
    const next = [...cfg.rows];
    const [row] = next.splice(index, 1);
    next.splice(target, 0, row);
    cfg.rows = next;
    dispatch();
  }

  // The summary is what the row shows when collapsed, so a card of ten rows
  // stays readable in a panel this narrow.
  function summary(row: Row): string {
    if (row.name) return row.name;
    switch (row.source) {
      case "control":
        return controls.find((c) => c.id === row.controlId)?.name || "Pick a control";
      case "ha":
        return row.entityId || "Pick an entity";
      case "mqtt":
        return devices.find((d) => d.id === row.deviceId)?.name || "Pick a device";
      case "flow-trigger": {
        const list = presses(row);
        if (list.length === 0) return "Pick a trigger";
        if (list.length === 1) return list[0].label || list[0].trigger || "Pick a trigger";
        return `${list.length} buttons`;
      }
    }
    return "row";
  }

  // preview is what the row will draw for its icon: the pack's glyph, or the
  // value as text when it is not an icon name.
  function preview(row: Row) {
    return iconGlyph(row.icon || "", icons);
  }

  // ---- Flow trigger buttons ----
  //
  // A trigger row is the one place a widget declares its own buttons, because a
  // trigger has no state anywhere else to declare them in: On and Off may be
  // two entry points of one flow or of two, and only the dashboard knows they
  // belong side by side.

  function presses(row: Row): TriggerPress[] {
    return triggerPresses(row);
  }

  function setPresses(row: Row, next: TriggerPress[]): void {
    updateRow(row.id, { triggers: next, flowId: undefined, trigger: undefined });
  }

  function addPress(row: Row): void {
    setPresses(row, [...presses(row), { id: newRowId(), label: "" }]);
  }

  function updatePress(row: Row, id: string, patch: Partial<TriggerPress>): void {
    setPresses(
      row,
      presses(row).map((p) => (p.id === id ? { ...p, ...patch } : p)),
    );
  }

  function removePress(row: Row, id: string): void {
    setPresses(
      row,
      presses(row).filter((p) => p.id !== id),
    );
  }

  // The label defaults to the trigger's name, read from the stored press rather
  // than the one this block was rendered with: typing a label and picking a
  // trigger in the same tick would otherwise overwrite what was just typed.
  function selectPress(row: Row, pressId: string, index: string): void {
    const entry = triggers[Number(index)];
    if (!entry) {
      updatePress(row, pressId, { flowId: "", trigger: "" });
      return;
    }
    const current = presses(row).find((p) => p.id === pressId);
    updatePress(row, pressId, {
      flowId: entry.flow_id,
      trigger: entry.name,
      label: current?.label || entry.name,
    });
  }

  function pressIndex(press: TriggerPress): string {
    return String(triggers.findIndex((t) => t.flow_id === press.flowId && t.name === press.trigger));
  }

  async function editPressPayload(row: Row, press: TriggerPress): Promise<void> {
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
    updatePress(row, press.id, { payload: next });
  }

  let flowNames = $derived([...new Set(triggers.map((t) => t.flow_name))]);
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <label class="label" for="card-title">Card title</label>
    <input
      id="card-title"
      class="input"
      value={cfg.title}
      placeholder="e.g. Living Room"
      oninput={(e: Event) => { cfg.title = (e.target as HTMLInputElement).value; dispatch(); }}
    />
  </div>

  <div class="rows">
    {#each cfg.rows as row, index (row.id)}
      <div class="row-card">
        <div class="row-head">
          <button class="disclosure" type="button"
                  onclick={() => {
                    expanded = expanded === row.id ? "" : row.id;
                    if (expanded === row.id && row.source === "ha") {
                      void loadEntities(resolve(row.connectionId || ""));
                    }
                  }}>
            <span class="row-summary">{summary(row)}</span>
            <span class="row-kind">{row.source}</span>
          </button>
          <button class="mini" type="button" title="Move up" disabled={index === 0}
                  onclick={() => move(row.id, -1)}>↑</button>
          <button class="mini" type="button" title="Move down"
                  disabled={index === cfg.rows.length - 1} onclick={() => move(row.id, 1)}>↓</button>
          <button class="mini danger" type="button" title="Remove"
                  onclick={() => removeRow(row.id)}>✕</button>
        </div>

        {#if expanded === row.id}
          <div class="row-body">
            {#if row.source === "control"}
              <div class="field">
                <label class="label" for={`control-${row.id}`}>Control</label>
                <SourceSelect id={`control-${row.id}`} value={row.controlId || ""} placeholder="Select a control..."
                              options={controls.map((c) => ({ value: c.id, label: c.name }))}
                              onchange={(id) => updateRow(row.id, { controlId: id })} />
                {#if controls.length === 0}
                  <span class="hint">No controls yet; define one under Settings → Controls.</span>
                {/if}
              </div>
            {:else if row.source === "ha"}
              <div class="field">
                <label class="label" for={`ha-conn-${row.id}`}>Connection</label>
                <SourceSelect id={`ha-conn-${row.id}`} value={row.connectionId || ""} placeholder="Select a connection..."
                              options={haConnections.map((c) => ({ value: c.id, label: c.name }))}
                              onchange={(id) => {
                                updateRow(row.id, { connectionId: id, entityId: hasReference(row.entityId) ? row.entityId : "" });
                                void loadEntities(resolve(id));
                              }} />
              </div>
              <div class="field entity-field">
                <label class="label" for={`ha-entity-${row.id}`}>Entity</label>
                <input id={`ha-entity-${row.id}`} class="input mono" autocomplete="off"
                       value={row.entityId || ""} placeholder="light.kitchen"
                       disabled={!row.connectionId}
                       oninput={(e: Event) => {
                         updateRow(row.id, { entityId: (e.target as HTMLInputElement).value });
                         entityOpen = row.id;
                       }}
                       onfocus={() => {
                         entityOpen = row.id;
                         void loadEntities(resolve(row.connectionId || ""));
                       }}
                       onblur={() => {
                         // Delay so an option's mousedown wins over the close.
                         setTimeout(() => {
                           if (entityOpen === row.id) entityOpen = "";
                         }, 150);
                       }} />
                {#if hasReference(row.entityId)}
                  <span class="hint">Now <span class="mono">{resolve(row.entityId || "")}</span></span>
                {/if}
                {#if !row.connectionId}
                  <span class="hint">Select a connection first to browse entities.</span>
                {:else if entityOpen === row.id}
                  {@const options = filteredEntities(row)}
                  {#if options.length > 0}
                    <ul class="entity-dropdown">
                      {#each options as entity (entity.entity_id)}
                        <li>
                          <button type="button" class="entity-option"
                                  onmousedown={(e: Event) => {
                                    e.preventDefault();
                                    updateRow(row.id, { entityId: entity.entity_id });
                                    entityOpen = "";
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
            {:else if row.source === "mqtt"}
              <div class="field">
                <label class="label" for={`mqtt-device-${row.id}`}>Device</label>
                <SourceSelect id={`mqtt-device-${row.id}`} value={row.deviceId || ""} placeholder="Select a device..."
                              options={devices.map((d) => ({ value: d.id, label: d.name }))}
                              onchange={(id) => updateRow(row.id, { deviceId: id, deviceField: "" })} />
                <span class="hint">
                  {#if devices.length === 0}
                    None declared yet — add one in Settings → MQTT Devices.
                  {:else}
                    The device owns its topics, how its messages are read, what it is drawn with,
                    and how a command is built. Declared in Settings → MQTT Devices.
                  {/if}
                </span>
              </div>
              {#if row.deviceId}
                <div class="field">
                  <label class="label" for={`mqtt-field-${row.id}`}>Value</label>
                  <input id={`mqtt-field-${row.id}`} class="input mono"
                         list={`mqtt-fields-${row.id}`}
                         value={row.deviceField || ""} placeholder="all of them"
                         oninput={(e: Event) =>
                           updateRow(row.id, { deviceField: (e.target as HTMLInputElement).value })} />
                  <datalist id={`mqtt-fields-${row.id}`}>
                    {#each deviceFields(row.deviceId) as field (field)}
                      <option value={field}></option>
                    {/each}
                  </datalist>
                  <span class="hint">
                    Leave it empty for the control the device is. Naming one of its other values
                    shows that reading instead, which is how a battery level sits beside a light.
                    The list is what it has decoded so far; a value it has not sent yet can still
                    be named.
                  </span>
                </div>
                {#if row.deviceField}
                  <div class="field pair">
                    <span class="pair-half">
                      <label class="label" for={`mqtt-unit-${row.id}`}>Unit</label>
                      <input id={`mqtt-unit-${row.id}`} class="input" value={row.fieldUnit || ""}
                             placeholder="%"
                             oninput={(e: Event) =>
                               updateRow(row.id, {
                                 fieldUnit: (e.target as HTMLInputElement).value,
                               })} />
                    </span>
                    <span class="pair-half">
                      <label class="label" for={`mqtt-prec-${row.id}`}>Decimals</label>
                      <input id={`mqtt-prec-${row.id}`} class="input" type="number" min="0"
                             value={row.fieldPrecision ?? ""} placeholder="as sent"
                             oninput={(e: Event) => {
                               const raw = (e.target as HTMLInputElement).value;
                               updateRow(row.id, {
                                 fieldPrecision: raw === "" ? undefined : Number(raw),
                               });
                             }} />
                    </span>
                  </div>
                  <span class="hint">
                    A named value is a reading the device happens to report, so it has no settings
                    of its own to be formatted by.
                  </span>
                {/if}
              {/if}
            {:else if row.source === "flow-trigger"}
              <!-- A row of triggers is On and Off side by side. Each button
                   names its own entry point, so the two halves of one action
                   can live in different flows and still read as one control. -->
              <div class="field">
                <span class="label">Buttons</span>
                {#each presses(row) as press (press.id)}
                  <div class="press">
                    <div class="press-head">
                      <input class="input press-label" value={press.label || ""}
                             placeholder="Label"
                             oninput={(e: Event) =>
                               updatePress(row, press.id, {
                                 label: (e.target as HTMLInputElement).value,
                               })} />
                      <button class="mini danger" type="button" title="Remove"
                              onclick={() => removePress(row, press.id)}>✕</button>
                    </div>
                    <select class="input" value={pressIndex(press)}
                            onchange={(e: Event) =>
                              selectPress(row, press.id, (e.target as HTMLSelectElement).value)}>
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
                    <button class="document" type="button"
                            onclick={() => editPressPayload(row, press)}>
                      {press.payload?.trim() ? "Edit parameters" : "No parameters"}
                    </button>
                    <label class="check">
                      <input type="checkbox" checked={!!press.confirmFirst}
                             onchange={(e: Event) =>
                               updatePress(row, press.id, {
                                 confirmFirst: (e.target as HTMLInputElement).checked,
                               })} />
                      <span>Require a second tap</span>
                    </label>
                  </div>
                {/each}
                <button class="document" type="button" onclick={() => addPress(row)}>
                  Add a button
                </button>
                {#if triggers.length === 0}
                  <span class="hint">
                    No widget triggers yet. Add a Widget Trigger node to a flow and deploy it.
                  </span>
                {/if}
              </div>
            {/if}

            <div class="field">
              <label class="label" for={`name-${row.id}`}>Name</label>
              <input id={`name-${row.id}`} class="input" value={row.name || ""}
                     placeholder="from the source"
                     oninput={(e: Event) =>
                       updateRow(row.id, { name: (e.target as HTMLInputElement).value })} />
            </div>

            <div class="field icon-field">
              <label class="label" for={`icon-${row.id}`}>Icon</label>
              <div class="icon-input">
                <span class="icon-preview" class:glyph={preview(row).glyph}
                      style:font-family={preview(row).glyph ? fontStack : null}>{preview(row).text}</span>
                <input id={`icon-${row.id}`} class="input" autocomplete="off"
                       value={row.icon || ""} placeholder={icons.length ? "la-home" : "emoji"}
                       oninput={(e: Event) => {
                         updateRow(row.id, { icon: (e.target as HTMLInputElement).value });
                         iconOpen = row.id;
                       }}
                       onfocus={() => { iconOpen = row.id; }}
                       onblur={() => {
                         // Delay so an option's mousedown wins over the close.
                         setTimeout(() => {
                           if (iconOpen === row.id) iconOpen = "";
                         }, 150);
                       }} />
              </div>
              {#if icons.length === 0}
                <span class="hint">This host offers no icon pack; type any text or emoji.</span>
              {:else if iconOpen === row.id}
                {@const matches = filterIcons(icons, row.icon || "")}
                {#if matches.length > 0}
                  <ul class="icon-dropdown">
                    {#each matches as entry (entry.name)}
                      <li>
                        <button type="button" class="icon-option"
                                onmousedown={(e: Event) => {
                                  e.preventDefault();
                                  updateRow(row.id, { icon: entry.name });
                                  iconOpen = "";
                                }}>
                          <span class="icon-glyph" style:font-family={fontStack}>{entry.char}</span>
                          <span class="icon-name">{entry.name}</span>
                        </button>
                      </li>
                    {/each}
                  </ul>
                {:else}
                  <span class="hint">No icon matches that.</span>
                {/if}
              {/if}
            </div>

            {#if drawsLocation(row, controls, devices)}
              <label class="check">
                <input type="checkbox" checked={!!row.showCoordinates}
                       onchange={(e: Event) => updateRow(row.id, { showCoordinates: (e.target as HTMLInputElement).checked })} />
                <span>Show coordinates</span>
              </label>
              <span class="hint">
                Shows latitude, longitude, and accuracy under the zone when the source reports them.
              </span>
            {/if}

            {#if row.source !== "flow-trigger"}
              <AppearanceEditor states={row.states} format={row.format} precision={row.precision}
                                idPrefix="row-{row.id}" onchange={(patch) => updateRow(row.id, patch)} />

              <label class="check">
                <input type="checkbox" checked={!!row.displayOnly}
                       onchange={(e: Event) =>
                         updateRow(row.id, {
                           displayOnly: (e.target as HTMLInputElement).checked,
                         })} />
                <span>Display only</span>
              </label>
              <span class="hint">
                What this row is drawn with comes from the source: a Home Assistant entity from its
                domain, a control and an MQTT device from the kind they were defined as. Display
                only shows the reading and stops anyone pressing it, here and on the server.
              </span>
            {/if}

          </div>
        {/if}
      </div>
    {/each}
  </div>

  <div class="add-row">
    <span class="label">Add a row</span>
    <div class="add-buttons">
      <button type="button" class="add" onclick={() => addRow("control")}>Control</button>
      <button type="button" class="add" onclick={() => addRow("ha")}>Home Assistant</button>
      <button type="button" class="add" onclick={() => addRow("mqtt")}>MQTT</button>
      <button type="button" class="add" onclick={() => addRow("flow-trigger")}>Flow trigger</button>
    </div>
  </div>

  {#if haConnections.length > 0}
    <div class="add-row">
      <span class="label">Add an area</span>
      <div class="add-buttons">
        {#if haConnections.length > 1}
          <select class="input" aria-label="Home Assistant connection" bind:value={areaConnection}>
            {#each haConnections as c (c.id)}
              <option value={c.id}>{c.name}</option>
            {/each}
          </select>
        {/if}
        <select class="input" aria-label="Area" bind:value={areaId}>
          <option value="">Select an area...</option>
          {#each areas as a (a.area_id)}
            <option value={a.area_id}>{a.name}</option>
          {/each}
        </select>
        <button type="button" class="add" disabled={!areaId} onclick={addArea}>Add its entities</button>
      </div>
      {#if areaNote}
        <span class="hint">{areaNote}</span>
      {/if}
    </div>
  {/if}
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

  .hint.error {
    color: var(--theme-error, #f38ba8);
    opacity: 1;
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

  /* The preview is what the row will draw, beside the name that produced it —
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

  .rows {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }

  .row-card {
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    overflow: hidden;
  }

  .row-head {
    display: flex;
    align-items: stretch;
    gap: 0.15rem;
    background: var(--theme-surface0, #313244);
  }

  .disclosure {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.35rem 0.5rem;
    border: none;
    background: transparent;
    color: inherit;
    font-family: inherit;
    font-size: 0.82rem;
    text-align: left;
    cursor: pointer;
  }

  .row-summary {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .row-kind {
    font-size: 0.68rem;
    opacity: 0.6;
    text-transform: uppercase;
    flex-shrink: 0;
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

  .mini:hover:not(:disabled) {
    color: var(--theme-accent, #89b4fa);
  }

  .mini:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .mini.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  .row-body {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.5rem;
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

  .press-head {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .press-label {
    flex: 1;
    min-width: 0;
  }

  .field.pair {
    flex-direction: row;
    gap: 0.5rem;
  }

  .pair-half {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    flex: 1;
    min-width: 0;
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
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

  .add-row {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .add-buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
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
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }
</style>
