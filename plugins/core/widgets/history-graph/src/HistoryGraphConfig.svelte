<svelte:options customElement="history-graph-config" />

<script lang="ts">
  import { onDestroy } from "svelte";
  import EntityPicker from "../../shared/EntityPicker.svelte";
  import SourceSelect from "../../shared/SourceSelect.svelte";
  import { reference, resolve, watchVariables, type BridgeVariables } from "../../shared/variables";
  import {
    hoursReference,
    MAX_HOURS,
    MIN_HOURS,
    THEME_HEX,
    newSeriesId,
    parseConfig,
    type GraphConfig,
    type Series,
    type SeriesSource,
  } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  interface ControlEntry {
    id: string;
    name: string;
  }
  interface DeviceEntry {
    id: string;
    name: string;
    state?: Record<string, unknown>;
  }

  let cfg = $state<GraphConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);
  let controls = $state<ControlEntry[]>([]);
  let devices = $state<DeviceEntry[]>([]);

  // Every edit round trips through a save and comes back as a new attribute a
  // keystroke behind, so the attribute seeds the panel once per placement and
  // the panel owns its state from then on.
  let seededFor: string | null = null;
  $effect(() => {
    const widgetId = placementId(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
    hoursRef = hoursReference(configAttr);
  });

  // hoursRef keeps a variable reference for the hours, such as ${range}; the
  // parsed config holds only a number, which the reference would lose.
  let hoursRef = $state<string | null>(null);
  let variableState = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  const stopVariables = watchVariables((next) => (variableState = next));
  onDestroy(stopVariables);
  let hoursReferences = $derived(variableState.variables.map((v) => reference(v.name)));

  function placementId(attr: string | Record<string, unknown>): string {
    if (typeof attr !== "string") return String((attr as any)?.dashboardWidgetId ?? "");
    try {
      return String(JSON.parse(attr)?.dashboardWidgetId ?? "");
    } catch {
      return "";
    }
  }

  const bridge = () => (window as any).__karakuri;

  let loaded = false;
  $effect(() => {
    if (loaded) return;
    loaded = true;
    void loadSources();
  });

  async function loadSources(): Promise<void> {
    try {
      const res = await bridge().api.get("/controls");
      controls = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      controls = [];
    }
    try {
      const res = await bridge().api.get("/mqtt_devices");
      devices = Array.isArray(res) ? res : (res?.data ?? []);
    } catch {
      // Reading devices needs connection read; the other sources still work.
      devices = [];
    }
  }

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  // dispatch writes the config with every series display only and the unset
  // axis ends left out, which is how the widget reads "fit to the data".
  function dispatch(): void {
    const out: Record<string, unknown> = {
      ...$state.snapshot(cfg),
      series: cfg.series.map((s) => ({ ...$state.snapshot(s), displayOnly: true })),
    };
    if (cfg.min === null) delete out.min;
    if (cfg.max === null) delete out.max;
    if (hoursRef) out.hours = hoursRef;
    host?.dispatchEvent(new CustomEvent("configchange", { detail: out, bubbles: true, composed: true }));
  }

  function update(patch: Partial<GraphConfig>): void {
    cfg = { ...cfg, ...patch };
    dispatch();
  }

  function updateSeries(id: string, patch: Partial<Series>): void {
    update({ series: cfg.series.map((s) => (s.id === id ? { ...s, ...patch, displayOnly: true } : s)) });
  }

  // Switching a row's source clears the fields of the one being left, so a
  // stale entity ID is never read as part of the new binding.
  function setSource(id: string, source: SeriesSource): void {
    update({
      series: cfg.series.map((s) =>
        s.id === id ? { id: s.id, source, name: s.name, color: s.color, displayOnly: true } : s,
      ),
    });
  }

  function addSeries(): void {
    const last = cfg.series[cfg.series.length - 1];
    const next: Series = { id: newSeriesId(), source: "ha", displayOnly: true };
    // A second line is most often another sensor on the same Home Assistant.
    if (last?.source === "ha" && last.connectionId) next.connectionId = last.connectionId;
    update({ series: [...cfg.series, next] });
  }

  function removeSeries(id: string): void {
    update({ series: cfg.series.filter((s) => s.id !== id) });
  }

  function deviceFields(id: string | undefined): string[] {
    const device = devices.find((d) => d.id === id);
    return Object.keys(device?.state ?? {}).sort();
  }

  function axisValue(e: Event): number | null {
    const text = (e.target as HTMLInputElement).value.trim();
    if (text === "") return null;
    const n = Number(text);
    return Number.isFinite(n) ? n : null;
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Series</span>
    {#each cfg.series as series, index (series.id)}
      <div class="series">
        <div class="row">
          <select class="input" value={series.source} aria-label="Source"
                  onchange={(e: Event) =>
                    setSource(series.id, (e.target as HTMLSelectElement).value as SeriesSource)}>
            <option value="ha">Home Assistant</option>
            <option value="control">Control</option>
            <option value="mqtt">MQTT device</option>
          </select>
          <button class="mini danger" type="button" title="Remove series" aria-label="Remove series"
                  onclick={() => removeSeries(series.id)}>×</button>
        </div>

        {#if series.source === "ha"}
          <EntityPicker idPrefix="hg-{series.id}" placeholder="sensor.outside_temperature"
                        connectionId={series.connectionId || ""} entityId={series.entityId || ""}
                        onchange={(patch) => updateSeries(series.id, patch)} />
        {:else if series.source === "control"}
          <SourceSelect value={series.controlId || ""} ariaLabel="Control" placeholder="Select a control..."
                        options={controls.map((c) => ({ value: c.id, label: c.name }))}
                        onchange={(id) => updateSeries(series.id, { controlId: id })} />
          <span class="hint">Controls keep no history; the line starts when the page loads.</span>
        {:else}
          <SourceSelect value={series.deviceId || ""} ariaLabel="Device" placeholder="Select a device..."
                        options={devices.map((d) => ({ value: d.id, label: d.name }))}
                        onchange={(id) => updateSeries(series.id, { deviceId: id, deviceField: "" })} />
          {#if series.deviceId}
            <input class="input mono" list="hg-fields-{series.id}" value={series.deviceField || ""}
                   placeholder="value name" aria-label="Device value"
                   oninput={(e: Event) =>
                     updateSeries(series.id, { deviceField: (e.target as HTMLInputElement).value })} />
            <datalist id="hg-fields-{series.id}">
              {#each deviceFields(series.deviceId) as field (field)}
                <option value={field}></option>
              {/each}
            </datalist>
          {/if}
          <span class="hint">
            Devices keep no history; the line starts when the page loads. Leave the value empty for a
            device that reports one value.
          </span>
        {/if}

        <div class="row">
          <input class="input" value={series.name || ""} placeholder="Name from the source"
                 aria-label="Name"
                 oninput={(e: Event) =>
                   updateSeries(series.id, { name: (e.target as HTMLInputElement).value })} />
          <input class="color" type="color" aria-label="Colour"
                 value={series.color || THEME_HEX[index % THEME_HEX.length]}
                 onchange={(e: Event) =>
                   updateSeries(series.id, { color: (e.target as HTMLInputElement).value })} />
          {#if series.color}
            <button class="mini" type="button" title="Use the theme colour"
                    onclick={() => updateSeries(series.id, { color: "" })}>↺</button>
          {/if}
        </div>
      </div>
    {/each}
    <button class="add" type="button" onclick={addSeries}>Add a series</button>
  </div>

  <div class="field">
    <label class="label" for="hg-hours">Hours shown</label>
    <input id="hg-hours" class="input" list="hg-hours-refs" inputmode="numeric"
           value={hoursRef ?? cfg.hours}
           onchange={(e: Event) => {
             const text = (e.target as HTMLInputElement).value.trim();
             if (/^\$\{[A-Za-z][A-Za-z0-9_]{0,31}\}$/.test(text)) {
               hoursRef = text;
               update({});
               return;
             }
             hoursRef = null;
             const n = Math.round(Number(text));
             update({ hours: Number.isFinite(n) ? Math.min(Math.max(n, MIN_HOURS), MAX_HOURS) : 24 });
           }} />
    <datalist id="hg-hours-refs">
      {#each hoursReferences as ref (ref)}<option value={ref}></option>{/each}
    </datalist>
    <span class="hint">
      From 1 to 168 (one week), or a variable such as ${"{"}range{"}"}.
      {#if hoursRef}Now {resolve(hoursRef, variableState)} hours.{/if}
    </span>
  </div>

  <div class="field">
    <label class="label" for="hg-style">Style</label>
    <select id="hg-style" class="input" value={cfg.style}
            onchange={(e: Event) =>
              update({ style: (e.target as HTMLSelectElement).value as GraphConfig["style"] })}>
      <option value="line">Line</option>
      <option value="area">Area</option>
      <option value="step">Steps</option>
    </select>
  </div>

  <div class="field">
    <span class="label">Value axis</span>
    <div class="row">
      <input class="input" type="number" step="any" placeholder="Auto minimum" aria-label="Minimum"
             value={cfg.min ?? ""} onchange={(e: Event) => update({ min: axisValue(e) })} />
      <input class="input" type="number" step="any" placeholder="Auto maximum" aria-label="Maximum"
             value={cfg.max ?? ""} onchange={(e: Event) => update({ max: axisValue(e) })} />
    </div>
    <span class="hint">Leave either end empty to fit it to the data.</span>
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showLegend}
           onchange={(e: Event) => update({ showLegend: (e.target as HTMLInputElement).checked })} />
    <span>Show the legend</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.showCurrent} disabled={!cfg.showLegend}
           onchange={(e: Event) => update({ showCurrent: (e.target as HTMLInputElement).checked })} />
    <span>Show current values in the legend</span>
  </label>
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

  /* Each series is its own block: a source, its picker, and how it is drawn.
     Stacked without a boundary they read as one long form. */
  .series {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.5rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.4rem;
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
    min-width: 0;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .mono {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.78rem;
  }

  .color {
    flex-shrink: 0;
    width: 2.2rem;
    height: 1.9rem;
    padding: 0.1rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    cursor: pointer;
  }

  .mini {
    flex-shrink: 0;
    width: 1.7rem;
    border: none;
    background: transparent;
    color: var(--theme-subtext, #a6adc8);
    font-family: inherit;
    font-size: 0.9rem;
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

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
