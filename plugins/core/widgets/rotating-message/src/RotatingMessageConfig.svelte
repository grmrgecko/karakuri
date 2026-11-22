<svelte:options customElement="rotating-message-config" />

<script lang="ts">
  import type { EntitySource, SourceKind } from "../../shared/entitySource";
  import SourceSelect from "../../shared/SourceSelect.svelte";
  import { hasReference, resolve } from "../../shared/variables";
  import { cleanLists, parseConfig, type RotatingConfig } from "./config";
  import { placementIds } from "../../shared/dashboardLive";
  import { isCronKey, isDateKey, type Lists } from "./schedule";

  interface Named {
    id: string;
    name: string;
    kind?: string;
  }
  interface EntityEntry {
    entity_id: string;
    friendly_name?: string;
  }

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<RotatingConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);
  let listsError = $state("");

  let connections = $state<Named[]>([]);
  let controls = $state<Named[]>([]);
  let devices = $state<Named[]>([]);
  let entities = $state<EntityEntry[]>([]);
  let entitiesFor = "";

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const { widgetId } = placementIds(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
    if (cfg.weather?.source === "ha" && cfg.weather.connectionId) void loadEntities(resolve(cfg.weather.connectionId));
  });

  const bridge = () => (window as any).__karakuri;

  function list(res: any): any[] {
    return Array.isArray(res) ? res : Array.isArray(res?.data) ? res.data : [];
  }

  let loaded = false;
  $effect(() => {
    if (loaded) return;
    loaded = true;
    void loadSources();
  });

  // Each list is read on its own: a viewer of one kind of source may lack
  // permission for the others, and that should empty one picker, not all.
  async function loadSources(): Promise<void> {
    try {
      connections = list(await bridge().api.get("/connections")).filter((c) => c.kind === "homeassistant");
    } catch {
      connections = [];
    }
    try {
      controls = list(await bridge().api.get("/controls"));
    } catch {
      controls = [];
    }
    try {
      devices = list(await bridge().api.get("/mqtt_devices"));
    } catch {
      devices = [];
    }
  }

  async function loadEntities(connectionId: string): Promise<void> {
    if (!connectionId || hasReference(connectionId) || connectionId === entitiesFor) return;
    entitiesFor = connectionId;
    try {
      const all = list(await bridge().api.get(`/connections/${connectionId}/ha/entities`)) as EntityEntry[];
      // Weather entities come first because they are what this field is for,
      // but any entity whose state is a word can drive a condition key.
      entities = [
        ...all.filter((e) => e.entity_id.startsWith("weather.")),
        ...all.filter((e) => !e.entity_id.startsWith("weather.")),
      ];
    } catch {
      entities = [];
    }
  }

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function dispatch(): void {
    const detail = $state.snapshot(cfg) as Record<string, unknown>;
    // The host reads weather as a binding and refuses a null one, so an unset
    // binding is left out entirely.
    if (!detail.weather) delete detail.weather;
    host?.dispatchEvent(new CustomEvent("configchange", { detail, bubbles: true, composed: true }));
  }

  function update(patch: Partial<RotatingConfig>): void {
    cfg = { ...cfg, ...patch };
    dispatch();
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

  // ---- Weather binding ----

  function setWeatherSource(source: SourceKind | ""): void {
    if (!source) {
      cfg = { ...cfg, weather: undefined };
    } else {
      cfg = { ...cfg, weather: { source, displayOnly: true } };
    }
    dispatch();
  }

  function updateWeather(patch: Partial<EntitySource>): void {
    if (!cfg.weather) return;
    cfg = { ...cfg, weather: { ...cfg.weather, ...patch, displayOnly: true } };
    dispatch();
  }

  // ---- Lists ----

  // yamlKey quotes a key unless it is a plain word, which a date pattern,
  // with its dots and dashes, and a cron key, with its spaces, are not.
  function yamlKey(key: string): string {
    return /^[A-Za-z_][A-Za-z0-9_-]*$/.test(key) ? key : JSON.stringify(key);
  }

  // listsYAML writes the lists for the editor. A JSON string is a valid YAML
  // double-quoted scalar, so every line round trips whatever it contains.
  function listsYAML(lists: Lists): string {
    const out: string[] = [];
    for (const [key, lines] of Object.entries(lists)) {
      if (lines.length === 0) {
        out.push(`${yamlKey(key)}: []`);
        continue;
      }
      out.push(`${yamlKey(key)}:`);
      for (const line of lines) out.push(`  - ${JSON.stringify(line)}`);
    }
    return out.join("\n") + "\n";
  }

  async function editLists(): Promise<void> {
    listsError = "";
    const b = bridge();
    if (!b?.editDocument) return;
    const text = await b.editDocument({
      title: "Message lists",
      value: listsYAML(cfg.lists),
      format: "yaml",
      requireObject: true,
      hint: "Keys: morning, afternoon, evening, anytime, YYYY-MM-DD with . wildcards, cron, or a weather word",
    });
    if (text === null) return;
    try {
      const parsed = await b.parseDocument(text, "yaml");
      update({ lists: cleanLists(parsed ?? {}) });
    } catch (e: any) {
      listsError = e?.message || "The lists do not parse";
    }
  }

  function keyKind(key: string): string {
    if (["morning", "afternoon", "evening", "anytime"].includes(key)) return "time of day";
    if (isDateKey(key)) return "date";
    if (isCronKey(key)) return "cron";
    return "weather";
  }

  let listKeys = $derived(Object.entries(cfg.lists).map(([key, lines]) => ({ key, count: lines.length, kind: keyKind(key) })));
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Message lists</span>
    {#if listKeys.length === 0}
      <span class="hint">No lists. The widget shows nothing until one is added or a flow sends lines.</span>
    {:else}
      <ul class="keys">
        {#each listKeys as entry (entry.key)}
          <li>
            <span class="mono">{entry.key}</span>
            <span class="meta">{entry.kind}, {entry.count} {entry.count === 1 ? "line" : "lines"}</span>
          </li>
        {/each}
      </ul>
    {/if}
    <button type="button" class="button" onclick={editLists}>Edit lists</button>
    {#if listsError}
      <span class="hint error">{listsError}</span>
    {/if}
    <span class="hint">
      A date key such as <span class="mono">....-12-25</span> matches any year. A cron key such as
      <span class="mono">* 6-9 * * mon-fri</span> matches while its minute does. Any other key is a
      weather condition, matched against the weather source.
    </span>
  </div>

  <div class="field">
    <span class="label">Time of day</span>
    <div class="grid">
      <label class="small" for="rm-ms">Morning from</label>
      <input id="rm-ms" class="input" type="number" min="0" max="24" value={cfg.morningStart}
             onchange={(e: Event) => update({ morningStart: numberInput(e, cfg.morningStart, 0, 24, true) })} />
      <label class="small" for="rm-me">to</label>
      <input id="rm-me" class="input" type="number" min="0" max="24" value={cfg.morningEnd}
             onchange={(e: Event) => update({ morningEnd: numberInput(e, cfg.morningEnd, 0, 24, true) })} />
      <label class="small" for="rm-as">Afternoon from</label>
      <input id="rm-as" class="input" type="number" min="0" max="24" value={cfg.afternoonStart}
             onchange={(e: Event) => update({ afternoonStart: numberInput(e, cfg.afternoonStart, 0, 24, true) })} />
      <label class="small" for="rm-ae">to</label>
      <input id="rm-ae" class="input" type="number" min="0" max="24" value={cfg.afternoonEnd}
             onchange={(e: Event) => update({ afternoonEnd: numberInput(e, cfg.afternoonEnd, 0, 24, true) })} />
    </div>
    <span class="hint">Hours 0 to 24. Evening is every other hour.</span>
  </div>

  <div class="field checkbox-field">
    <label class="checkbox-label">
      <input type="checkbox" checked={cfg.specialDayUnique}
             onchange={(e: Event) => update({ specialDayUnique: (e.target as HTMLInputElement).checked })} />
      <span>Show only date and cron lines when one matches</span>
    </label>
  </div>

  <div class="row">
    <div class="field">
      <label class="label" for="rm-interval">Interval (seconds)</label>
      <input id="rm-interval" class="input" type="number" min="2" value={cfg.interval}
             onchange={(e: Event) => update({ interval: numberInput(e, cfg.interval, 2) })} />
    </div>
    <div class="field">
      <label class="label" for="rm-fade">Fade (ms)</label>
      <input id="rm-fade" class="input" type="number" min="0" step="100" value={cfg.fade}
             onchange={(e: Event) => update({ fade: numberInput(e, cfg.fade, 0) })} />
    </div>
  </div>

  <div class="row">
    <div class="field">
      <label class="label" for="rm-align">Alignment</label>
      <select id="rm-align" class="input" value={cfg.align}
              onchange={(e: Event) => update({ align: (e.target as HTMLSelectElement).value as RotatingConfig["align"] })}>
        <option value="left">Left</option>
        <option value="center">Center</option>
        <option value="right">Right</option>
      </select>
    </div>
    <div class="field">
      <label class="label" for="rm-size">Text size</label>
      <select id="rm-size" class="input" value={cfg.size}
              onchange={(e: Event) => update({ size: (e.target as HTMLSelectElement).value as RotatingConfig["size"] })}>
        <option value="small">Small</option>
        <option value="medium">Medium</option>
        <option value="large">Large</option>
        <option value="fit">Fit to widget</option>
      </select>
    </div>
  </div>

  <div class="field">
    <label class="label" for="rm-weather">Weather source</label>
    <select id="rm-weather" class="input" value={cfg.weather?.source ?? ""}
            onchange={(e: Event) => setWeatherSource((e.target as HTMLSelectElement).value as SourceKind | "")}>
      <option value="">None</option>
      <option value="ha">Home Assistant entity</option>
      <option value="control">Control</option>
      <option value="mqtt">MQTT device</option>
    </select>
  </div>

  {#if cfg.weather?.source === "ha"}
    <div class="field">
      <label class="label" for="rm-ha-conn">Connection</label>
      <SourceSelect id="rm-ha-conn" value={cfg.weather.connectionId ?? ""} placeholder="Select a connection..."
                    options={connections.map((c) => ({ value: c.id, label: c.name }))}
                    onchange={(id) => {
                      updateWeather({ connectionId: id, entityId: hasReference(cfg.weather?.entityId) ? cfg.weather?.entityId : "" });
                      void loadEntities(resolve(id));
                    }} />
    </div>
    <div class="field">
      <label class="label" for="rm-ha-entity">Entity</label>
      <input id="rm-ha-entity" class="input mono" list="rm-ha-entities" autocomplete="off"
             placeholder="weather.home" value={cfg.weather.entityId ?? ""}
             disabled={!cfg.weather.connectionId}
             oninput={(e: Event) => updateWeather({ entityId: (e.target as HTMLInputElement).value.trim() })} />
      <datalist id="rm-ha-entities">
        {#each entities as entity (entity.entity_id)}
          <option value={entity.entity_id}>{entity.friendly_name ?? ""}</option>
        {/each}
      </datalist>
      <span class="hint">The entity's state, such as rainy or clear-night, picks the list with that key.</span>
    </div>
  {:else if cfg.weather?.source === "control"}
    <div class="field">
      <label class="label" for="rm-control">Control</label>
      <SourceSelect id="rm-control" value={cfg.weather.controlId ?? ""} placeholder="Select a control..."
                    options={controls.map((c) => ({ value: c.id, label: c.name }))}
                    onchange={(id) => updateWeather({ controlId: id })} />
      <span class="hint">The control's value picks the list with that key.</span>
    </div>
  {:else if cfg.weather?.source === "mqtt"}
    <div class="field">
      <label class="label" for="rm-device">Device</label>
      <SourceSelect id="rm-device" value={cfg.weather.deviceId ?? ""} placeholder="Select a device..."
                    options={devices.map((d) => ({ value: d.id, label: d.name }))}
                    onchange={(id) => updateWeather({ deviceId: id })} />
      <span class="hint">The device's value picks the list with that key.</span>
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
    min-width: 0;
  }

  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .grid {
    display: grid;
    grid-template-columns: auto 1fr auto 1fr;
    gap: 0.35rem 0.4rem;
    align-items: center;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
  }

  .small {
    font-size: 0.75rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .hint.error {
    color: var(--theme-error, #f38ba8);
    opacity: 1;
  }

  .mono {
    font-family: var(--theme-font-mono, monospace);
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
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .button {
    align-self: flex-start;
    padding: 0.35rem 0.75rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
  }

  .button:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .keys {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    font-size: 0.8rem;
  }

  .keys li {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .meta {
    color: var(--theme-text-muted, #a6adc8);
    font-size: 0.75rem;
    white-space: nowrap;
  }

  .checkbox-field {
    flex-direction: row;
    align-items: center;
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
