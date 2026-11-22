<svelte:options customElement="chart-config" />

<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { CHART_KINDS, ENERGY_CONNECTION, MAX_QUERIES, VIZ, newQuery, parseConfig, type ChartConfig, type ChartQuery } from "./config";
  import { placementIds } from "../../shared/dashboardLive";
  import { currentVariables, hasReference, reference, resolve, watchVariables, type BridgeVariables } from "../../shared/variables";
  import type { FieldConfig, Palette, QueryResponse } from "./data/types";
  import { FALLBACK_PALETTE } from "./data/colors";
  import { prepare } from "./data/pipeline";
  import { fieldNames as namesOf, framesFromResponse } from "./data/frames";
  import { chartRange, splitRange } from "./data/time";
  import { readPalette, watchTheme } from "./theme";
  import { VIZ_CUSTOM, VIZ_OPTIONS } from "./editor/specs";
  import ChartView from "./ChartView.svelte";
  import Fields from "./editor/Fields.svelte";
  import QueryEditor, { type ConnectionEntry } from "./editor/QueryEditor.svelte";
  import StandardOptions from "./editor/StandardOptions.svelte";
  import ThresholdsEditor from "./editor/ThresholdsEditor.svelte";
  import MappingsEditor from "./editor/MappingsEditor.svelte";
  import OverridesEditor from "./editor/OverridesEditor.svelte";
  import TransformationsEditor from "./editor/TransformationsEditor.svelte";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<ChartConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);
  let connections = $state<ConnectionEntry[]>([]);
  let variables = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  let palette = $state<Palette>(FALLBACK_PALETTE);
  let dashboardId = $state("");

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const ids = placementIds(configAttr);
    if (ids.widgetId === seededFor) return;
    seededFor = ids.widgetId;
    dashboardId = ids.dashboardId;
    cfg = parseConfig(configAttr);
    preview = null;
  });

  const stopVariables = watchVariables((next) => (variables = next));
  let stopTheme: (() => void) | null = null;
  onDestroy(() => {
    stopVariables();
    stopTheme?.();
  });

  const bridge = () => (window as any).__karakuri;

  onMount(() => {
    void loadConnections();
  });

  async function loadConnections(): Promise<void> {
    try {
      const res: any = await bridge().api.get("/connections");
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      // Energy rollups answer on a built-in connection with no row of its
      // own.
      connections = [
        { id: ENERGY_CONNECTION, name: "Energy", kind: "energy" },
        ...list
          .filter((c) => CHART_KINDS.includes(c?.kind))
          .map((c) => ({ id: String(c.id), name: String(c.name ?? c.id), kind: String(c.kind) })),
      ];
    } catch {
      connections = [];
    }
  }

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
    palette = readPalette(host);
    stopTheme = watchTheme(() => {
      if (host) palette = readPalette(host);
    });
  }

  function update(patch: Partial<ChartConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: $state.snapshot(cfg), bubbles: true, composed: true }),
    );
  }

  function setDefaults(defaults: FieldConfig): void {
    update({ fieldConfig: { ...cfg.fieldConfig, defaults } });
  }

  // ---- Queries ----

  function editQuery(i: number, q: ChartQuery): void {
    update({ queries: cfg.queries.map((x, k) => (k === i ? q : x)) });
  }

  function addQuery(): void {
    if (cfg.queries.length >= MAX_QUERIES) return;
    update({ queries: [...cfg.queries, newQuery(cfg.queries)] });
  }

  function duplicateQuery(i: number): void {
    if (cfg.queries.length >= MAX_QUERIES) return;
    const copy = { ...$state.snapshot(cfg.queries[i]), refId: newQuery(cfg.queries).refId } as ChartQuery;
    const next = [...cfg.queries];
    next.splice(i + 1, 0, copy);
    update({ queries: next });
  }

  function removeQuery(i: number): void {
    if (cfg.queries.length <= 1) return;
    update({ queries: cfg.queries.filter((_, k) => k !== i) });
  }

  // ---- Preview ----

  interface PreviewState {
    refId: string;
    response: QueryResponse | null;
    error: string;
    elapsedMs: number;
    from: string;
    to: string;
  }

  let preview = $state<PreviewState | null>(null);
  let previewing = $state<string | null>(null);
  let showRaw = $state(false);

  // previewRange is the chart's range as text ends the preview route parses,
  // with this screen's variable values filled in.
  function previewRange(): { from: string; to: string } {
    const text = hasReference(cfg.timeRange) ? resolve(cfg.timeRange, variables) : cfg.timeRange;
    const ends = splitRange(text) ?? { from: "now-6h", to: "now" };
    const r = chartRange({ timeRange: `${ends.from},${ends.to}`, relativeTime: cfg.relativeTime, timeShift: cfg.timeShift });
    // Relative time and shift are applied here, so the route gets plain ends.
    if (cfg.relativeTime || cfg.timeShift) return { from: String(r.from), to: String(r.to) };
    return ends;
  }

  async function runPreview(q: ChartQuery): Promise<void> {
    if (!dashboardId || previewing) return;
    previewing = q.refId;
    const { from, to } = previewRange();
    const started = performance.now();
    try {
      const res: any = await bridge().api.post(`/dashboards/${encodeURIComponent(dashboardId)}/chart/preview`, {
        query: $state.snapshot(q),
        vars: currentVariables().values,
        from,
        to,
        width: Math.round((host as HTMLElement | null)?.clientWidth || 600),
      });
      const response: QueryResponse = res && typeof res === "object" && ("result" in res || "error" in res || "ref_id" in res) ? res : (res?.data ?? null);
      preview = { refId: q.refId, response, error: "", elapsedMs: Math.round(performance.now() - started), from, to };
    } catch (e: any) {
      preview = { refId: q.refId, response: null, error: e?.message || "The preview failed", elapsedMs: Math.round(performance.now() - started), from, to };
    } finally {
      previewing = null;
    }
  }

  let previewData = $derived.by(() => {
    if (!preview?.response) return null;
    const res = { results: [{ ...preview.response, ref_id: preview.response.ref_id || preview.refId }] };
    try {
      return prepare(res, { transformations: cfg.transformations, fieldConfig: cfg.fieldConfig, hidden: [] });
    } catch (e: any) {
      return { frames: [], failures: [{ refId: preview.refId, message: `Transformations failed: ${e?.message ?? e}` }] };
    }
  });

  let previewRangeMs = $derived(
    preview ? chartRange({ timeRange: `${preview.from},${preview.to}` }) : chartRange({ timeRange: cfg.timeRange }),
  );

  let fieldNames = $derived.by(() => {
    if (!preview?.response) return [];
    const raw = framesFromResponse({ results: [{ ...preview.response, ref_id: preview.refId }] }).frames;
    return [...new Set([...namesOf(raw), ...namesOf(previewData?.frames ?? [])])];
  });

  let rawJson = $derived(preview?.response ? JSON.stringify(preview.response, null, 2) : "");

  // ---- Time and options ----

  let rangeRefs = $derived(variables.variables.filter((v) => (v.type as string) === "time_range").map((v) => reference(v.name)));
  let intervalRefs = $derived(variables.variables.filter((v) => (v.type as string) === "interval").map((v) => reference(v.name)));
  let optionSpecs = $derived(VIZ_OPTIONS[cfg.viz] ?? []);
  let customSpecs = $derived(VIZ_CUSTOM[cfg.viz] ?? []);
  let defaults = $derived(cfg.fieldConfig.defaults ?? {});
</script>

<div class="config-form chart-config" use:setHost>
  <div class="field">
    <label class="label" for="cc-viz">Visualization</label>
    <select id="cc-viz" class="input" value={cfg.viz}
            onchange={(e) => update({ viz: (e.target as HTMLSelectElement).value as ChartConfig["viz"] })}>
      {#each VIZ as v}<option value={v.id}>{v.name}</option>{/each}
    </select>
  </div>

  <details class="section" open>
    <summary>Queries</summary>
    <div class="section-body">
      {#each cfg.queries as q, i (i)}
        <QueryEditor query={q} {connections} {variables}
                     canRemove={cfg.queries.length > 1} canDuplicate={cfg.queries.length < MAX_QUERIES}
                     previewing={previewing === q.refId}
                     onchange={(next) => editQuery(i, next)}
                     onremove={() => removeQuery(i)}
                     onduplicate={() => duplicateQuery(i)}
                     onpreview={() => runPreview(q)} />
      {/each}
      {#if cfg.queries.length < MAX_QUERIES}
        <button type="button" class="add" onclick={addQuery}>+ Add query</button>
      {/if}
      <span class="hint">Viewers can read the query text in the dashboard's layout, as they can any widget setting.</span>
    </div>
  </details>

  {#if preview}
    <div class="preview">
      <div class="row preview-head">
        <span class="label">Preview of {preview.refId}</span>
        <button type="button" class="remove" aria-label="Close preview" onclick={() => (preview = null)}>×</button>
      </div>
      {#if preview.error}
        <div class="error">{preview.error}</div>
      {:else if preview.response?.error}
        <div class="error">{preview.response.error.message}</div>
      {/if}
      {#if previewData}
        <div class="preview-chart">
          <ChartView viz={cfg.viz} options={cfg.options} frames={previewData.frames} failures={previewData.failures}
                     {palette} range={previewRangeMs} noValue={defaults.noValue || "No data"} />
        </div>
      {/if}
      <div class="inspector">
        <div><span class="k">Range</span> <code>{preview.from} to {preview.to}</code></div>
        <div><span class="k">Time</span> {preview.elapsedMs} ms{preview.response?.cached ? " (cached)" : ""}</div>
        {#if preview.response?.result?.interval_ms}
          <div><span class="k">Interval</span> {preview.response.result.interval_ms} ms</div>
        {/if}
        {#if preview.response?.result}
          <div><span class="k">Series</span> {preview.response.result.series?.length ?? 0}{preview.response.result.truncated ? " (truncated)" : ""}</div>
        {/if}
        {#if preview.response?.result?.executed_query}
          <div class="k">Executed query</div>
          <pre class="code">{preview.response.result.executed_query}</pre>
        {/if}
        {#if rawJson}
          <button type="button" class="link" onclick={() => (showRaw = !showRaw)}>{showRaw ? "Hide" : "Show"} response JSON</button>
          {#if showRaw}<pre class="code raw">{rawJson.length > 50000 ? rawJson.slice(0, 50000) + "\n…" : rawJson}</pre>{/if}
        {/if}
      </div>
    </div>
  {/if}

  <details class="section">
    <summary>Time</summary>
    <div class="section-body">
      <div class="field">
        <label class="label" for="cc-range">Time range</label>
        <input id="cc-range" class="input mono" list="cc-range-refs" value={cfg.timeRange} placeholder="now-6h,now"
               onchange={(e) => update({ timeRange: (e.target as HTMLInputElement).value.trim() })} />
        <span class="hint">A time range variable such as ${"{range}"}, or from,to such as now-24h,now. Dragging across a chart bound to a variable sets it.</span>
      </div>
      <div class="row">
        <div class="field">
          <label class="label" for="cc-rel">Relative time</label>
          <input id="cc-rel" class="input" placeholder="1h" value={cfg.relativeTime}
                 onchange={(e) => update({ relativeTime: (e.target as HTMLInputElement).value.trim() })} />
        </div>
        <div class="field">
          <label class="label" for="cc-shift">Time shift</label>
          <input id="cc-shift" class="input" placeholder="1d" value={cfg.timeShift}
                 onchange={(e) => update({ timeShift: (e.target as HTMLInputElement).value.trim() })} />
        </div>
      </div>
      <div class="row">
        <div class="field">
          <label class="label" for="cc-refresh">Refresh</label>
          <input id="cc-refresh" class="input" list="cc-refresh-list" value={cfg.refresh} placeholder="30s"
                 onchange={(e) => update({ refresh: (e.target as HTMLInputElement).value.trim() })} />
        </div>
        <div class="field">
          <label class="label" for="cc-mdp">Max data points</label>
          <input id="cc-mdp" class="input" type="number" min="0" placeholder="Width" value={cfg.maxDataPoints || ""}
                 onchange={(e) => update({ maxDataPoints: Math.max(0, Math.floor(Number((e.target as HTMLInputElement).value) || 0)) })} />
        </div>
      </div>
      <span class="hint">Refresh is at least 5s; off loads once. An interval variable such as ${"{refresh}"} is allowed.</span>
      <datalist id="cc-range-refs">
        {#each rangeRefs as r}<option value={r}></option>{/each}
        {#each ["now-1h,now", "now-6h,now", "now-24h,now", "now-7d,now", "now-30d,now", "now/d,now/d"] as r}<option value={r}></option>{/each}
      </datalist>
      <datalist id="cc-refresh-list">
        {#each ["off", "5s", "10s", "30s", "1m", "5m", "15m", "30m", "1h", ...intervalRefs] as r}<option value={r}></option>{/each}
      </datalist>
    </div>
  </details>

  {#if optionSpecs.length || customSpecs.length}
    <details class="section">
      <summary>{VIZ.find((v) => v.id === cfg.viz)?.name} options</summary>
      <div class="section-body">
        <Fields specs={optionSpecs} value={cfg.options} {fieldNames} onchange={(options) => update({ options })} />
        {#if customSpecs.length}
          <div class="sub">Field options</div>
          <Fields specs={customSpecs} value={defaults.custom ?? {}} {fieldNames}
                  onchange={(custom) => setDefaults({ ...defaults, custom })} />
        {/if}
      </div>
    </details>
  {/if}

  <details class="section">
    <summary>Standard options</summary>
    <div class="section-body">
      <StandardOptions value={defaults} onchange={setDefaults} />
    </div>
  </details>

  <details class="section">
    <summary>Thresholds</summary>
    <div class="section-body">
      <ThresholdsEditor value={defaults.thresholds} onchange={(thresholds) => setDefaults({ ...defaults, thresholds })} />
    </div>
  </details>

  <details class="section">
    <summary>Value mappings</summary>
    <div class="section-body">
      <MappingsEditor value={defaults.mappings ?? []}
                      onchange={(mappings) => setDefaults({ ...defaults, mappings: mappings.length ? mappings : undefined })} />
    </div>
  </details>

  <details class="section">
    <summary>Overrides</summary>
    <div class="section-body">
      <OverridesEditor value={cfg.fieldConfig.overrides} {customSpecs} {fieldNames} refIds={cfg.queries.map((q) => q.refId)}
                       onchange={(overrides) => update({ fieldConfig: { ...cfg.fieldConfig, overrides } })} />
    </div>
  </details>

  <details class="section">
    <summary>Transformations</summary>
    <div class="section-body">
      <TransformationsEditor value={cfg.transformations} {fieldNames} onchange={(transformations) => update({ transformations })} />
    </div>
  </details>
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
    min-width: 0;
  }

  .section {
    border: 1px solid var(--theme-border-translucent, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
  }

  .section > summary {
    cursor: pointer;
    padding: 0.45rem 0.6rem;
    font-size: 0.85rem;
    font-weight: 500;
    user-select: none;
  }

  .section-body {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding: 0 0.6rem 0.6rem;
  }

  .sub {
    font-size: 0.8rem;
    font-weight: 500;
    margin-top: 0.25rem;
    color: var(--theme-text-muted, #a6adc8);
  }

  .preview {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.5rem;
    border: 1px solid var(--theme-accent, #89b4fa);
    border-radius: var(--theme-radius-sm, 4px);
  }

  .preview-head {
    align-items: center;
    justify-content: space-between;
  }

  .preview-chart {
    height: 220px;
    background: var(--theme-widget-bg, var(--theme-surface0, #313244));
    border-radius: var(--theme-radius-sm, 4px);
  }

  .inspector {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.75rem;
  }

  .k {
    color: var(--theme-text-muted, #a6adc8);
  }

  .code {
    margin: 0;
    padding: 0.4rem;
    background: var(--theme-code-bg, var(--theme-mantle, #181825));
    border-radius: 4px;
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.72rem;
    white-space: pre-wrap;
    word-break: break-all;
    max-height: 16rem;
    overflow: auto;
  }

  .error {
    font-size: 0.8rem;
    color: var(--theme-error, #f38ba8);
    white-space: pre-wrap;
  }

  /* Shared by the editor components, which render inside this element's
     shadow root. */
  .chart-config :global(.field) {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
    flex: 1;
  }

  .chart-config :global(.row) {
    display: flex;
    gap: 0.4rem;
    min-width: 0;
  }

  .chart-config :global(.label) {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
  }

  .chart-config :global(.hint) {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .chart-config :global(.input) {
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

  .chart-config :global(.input.mono) {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.8rem;
  }

  .chart-config :global(.input.color) {
    max-width: 8rem;
  }

  .chart-config :global(.input:focus) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .chart-config :global(.card) {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.45rem;
    border: 1px solid var(--theme-border-translucent, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
  }

  .chart-config :global(.remove) {
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
    font-size: 1rem;
  }

  .chart-config :global(.remove:disabled) {
    opacity: 0.4;
    cursor: default;
  }

  .chart-config :global(.add) {
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

  .chart-config :global(.add:disabled) {
    opacity: 0.5;
    cursor: default;
  }

  .chart-config :global(.link) {
    align-self: flex-start;
    background: none;
    border: none;
    padding: 0;
    color: var(--theme-accent, #89b4fa);
    font: inherit;
    font-size: 0.75rem;
    cursor: pointer;
  }

  .chart-config :global(.checkbox-label) {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .chart-config :global(.checkbox-label input[type="checkbox"]) {
    width: 16px;
    height: 16px;
    accent-color: var(--theme-accent, #89b4fa);
  }

  .chart-config :global(.chips) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  .chart-config :global(.chip) {
    padding: 0.15rem 0.5rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 999px;
    background: transparent;
    color: var(--theme-text-muted, #a6adc8);
    font: inherit;
    font-size: 0.72rem;
    cursor: pointer;
  }

  .chart-config :global(.chip.on) {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .chart-config :global(.json-preview) {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
