<svelte:options customElement="chart-widget" />

<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { parseConfig } from "./config";
  import { placementIds } from "../../shared/dashboardLive";
  import { currentVariables, hasReference, resolve, setVariable, watchVariables, type BridgeVariables } from "../../shared/variables";
  import type { ChartResponse, Palette, TimeRange } from "./data/types";
  import { prepare, type Prepared } from "./data/pipeline";
  import { responseRange } from "./data/frames";
  import { chartRange, parseDuration, parseRange, zoomOut } from "./data/time";
  import { FALLBACK_PALETTE } from "./data/colors";
  import { readPalette, watchTheme } from "./theme";
  import { MIN_REFRESH_MS } from "./config";
  import ChartView from "./ChartView.svelte";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  // The IDs are strings so an edit to any other field does not refetch.
  let dashboardId = $derived(placementIds(configAttr).dashboardId);
  let widgetId = $derived(placementIds(configAttr).widgetId);
  // references are the variables the stored config names, from the host.
  let references = $derived(placementIds(configAttr).references);

  const bridge = () => (window as any).__karakuri;
  // A render is photographed once, so it draws without animation and never
  // refreshes.
  const renderMode = !!bridge()?.isRenderMode?.();

  let hostEl: HTMLElement | null = null;
  let response = $state<ChartResponse | null>(null);
  let loadError = $state("");
  let loaded = $state(false);
  let zoom = $state<TimeRange | null>(null);
  let palette = $state<Palette>(FALLBACK_PALETTE);
  let variables = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  let generation = 0;
  let busy = true;

  // ---- Readiness ----

  onMount(() => {
    hostEl = $host();
    // A render waits for every busy widget, so the chart holds it until its
    // first data has drawn or failed.
    hostEl.setAttribute("data-widget-busy", "");
    palette = readPalette(hostEl);
    const stopTheme = watchTheme(() => {
      if (hostEl) palette = readPalette(hostEl);
    });
    return stopTheme;
  });

  function settle(): void {
    if (!busy) return;
    busy = false;
    hostEl?.removeAttribute("data-widget-busy");
  }

  function drawn(): void {
    if (loaded) settle();
  }

  // ---- Loading ----

  async function load(): Promise<void> {
    const gen = ++generation;
    const dash = dashboardId;
    const widget = widgetId;
    if (!bridge() || !dash || !widget) {
      loaded = true;
      return;
    }
    try {
      const body: Record<string, unknown> = {
        vars: currentVariables().values,
        width: Math.round(hostEl?.clientWidth || 800),
      };
      if (zoom) {
        body.from = String(zoom.from);
        body.to = String(zoom.to);
      }
      const res: any = await bridge().api.post(
        `/dashboards/${encodeURIComponent(dash)}/widgets/${encodeURIComponent(widget)}/chart`,
        body,
      );
      if (gen !== generation) return;
      response = res && typeof res === "object" && "results" in res ? res : (res?.data ?? null);
      loadError = "";
    } catch (e: any) {
      if (gen !== generation) return;
      // The last good data stays on screen; a stale chart beats an empty one.
      loadError = e?.message || "The chart could not be loaded";
    } finally {
      if (gen === generation) loaded = true;
    }
  }

  // schedule runs a load after ms, unless one is already due sooner.
  let timer: ReturnType<typeof setTimeout> | null = null;
  let due = 0;
  function schedule(ms: number): void {
    const at = Date.now() + ms;
    if (timer && due <= at) return;
    if (timer) clearTimeout(timer);
    due = at;
    timer = setTimeout(() => {
      timer = null;
      void load();
    }, ms);
  }

  // The server reads queries from the saved layout, and the editor saves a
  // moment after the panel changes them, so a config edit refetches late.
  let configKey = $derived(
    JSON.stringify([cfg.queries, cfg.timeRange, cfg.relativeTime, cfg.timeShift, cfg.maxDataPoints, dashboardId, widgetId]),
  );
  let firstLoad = true;
  $effect(() => {
    void configKey;
    schedule(firstLoad ? 0 : 1500);
    firstLoad = false;
  });

  // Only the variables the config names change the data, plus time ranges,
  // which an interval's auto value is sized from. The host reports the
  // names; a host that does not falls back to every variable.
  let liveKey = $derived(
    JSON.stringify([
      references.length
        ? variables.variables
            .filter((v) => references.includes(v.name) || (v.type as string) === "time_range")
            .map((v) => variables.values[v.name])
        : variables.values,
      zoom,
    ]),
  );
  let liveSeen = false;
  $effect(() => {
    void liveKey;
    if (!liveSeen) {
      liveSeen = true;
      return;
    }
    schedule(0);
  });

  const stopVariables = watchVariables((next) => (variables = next));

  // ---- Refresh ----

  let refreshMs = $derived.by(() => {
    let text = cfg.refresh.trim();
    if (hasReference(text)) text = resolve(text, variables);
    if (text === "" || text === "off" || hasReference(text)) return 0;
    const ms = parseDuration(text);
    return ms && ms > 0 ? Math.max(MIN_REFRESH_MS, ms) : 0;
  });

  $effect(() => {
    if (renderMode || !refreshMs) return;
    const ms = refreshMs;
    const interval = setInterval(() => {
      if (!document.hidden) void load();
    }, ms);
    return () => clearInterval(interval);
  });

  // A tab coming back after missing refreshes draws current data at once.
  let hiddenAt = 0;
  function onVisibility(): void {
    if (document.hidden) {
      hiddenAt = Date.now();
    } else if (refreshMs && hiddenAt && Date.now() - hiddenAt > refreshMs) {
      schedule(0);
    }
  }
  document.addEventListener("visibilitychange", onVisibility);

  // Points written while the socket was down arrive with the next read.
  const stopReconnect = renderMode ? undefined : bridge()?.onReconnect?.(() => schedule(0));

  onDestroy(() => {
    generation++;
    if (timer) clearTimeout(timer);
    stopVariables();
    stopReconnect?.();
    document.removeEventListener("visibilitychange", onVisibility);
  });

  // ---- Zoom ----

  // boundVariable names the time range variable the chart follows. The host
  // substitutes references before the widget reads its config but reports
  // the names the stored config used, so the bound variable is the time
  // range among them whose value the range now holds.
  function boundVariable(): string | null {
    const raw = cfg.timeRange.trim();
    const m = /^\$\{([A-Za-z][A-Za-z0-9_]{0,31})\}$/.exec(raw);
    if (m) return m[1];
    for (const v of variables.variables) {
      if ((v.type as string) !== "time_range" || !references.includes(v.name)) continue;
      if ((variables.values[v.name] ?? v.default) === raw) return v.name;
    }
    return null;
  }

  function onZoom(from: number, to: number): void {
    const name = boundVariable();
    if (name) setVariable(name, `${from},${to}`);
    else zoom = { from, to };
  }

  function onZoomOut(): void {
    if (zoom) {
      zoom = null;
      return;
    }
    const name = boundVariable();
    if (name) {
      const current = parseRange(variables.values[name] ?? cfg.timeRange) ?? range;
      const next = zoomOut(current);
      setVariable(name, `${next.from},${next.to}`);
    } else {
      zoom = zoomOut(range);
    }
  }

  // ---- Drawing ----

  let prepared = $derived.by((): Prepared => {
    try {
      return prepare(response, {
        transformations: cfg.transformations,
        fieldConfig: cfg.fieldConfig,
        hidden: cfg.queries.filter((q) => q.hide).map((q) => q.refId),
      });
    } catch (e: any) {
      return { frames: [], failures: [{ refId: "", message: `Transformations failed: ${e?.message ?? e}` }] };
    }
  });

  let range = $derived(
    responseRange(response) ??
      chartRange({
        zoom,
        timeRange: hasReference(cfg.timeRange) ? resolve(cfg.timeRange, variables) : cfg.timeRange,
        relativeTime: cfg.relativeTime,
        timeShift: cfg.timeShift,
      }),
  );

  let failures = $derived(
    loadError
      ? [...prepared.failures, { refId: response ? "Not refreshed" : "Error", message: loadError }]
      : prepared.failures,
  );
</script>

<div class="root">
  <ChartView
    viz={cfg.viz}
    options={cfg.options}
    frames={prepared.frames}
    {failures}
    {palette}
    {range}
    animation={!renderMode}
    noValue={cfg.fieldConfig.defaults.noValue || "No data"}
    {loaded}
    onzoom={onZoom}
    onzoomout={onZoomOut}
    ondrawn={drawn}
  />
</div>

<style>
  :hostEl {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    box-sizing: border-box;
  }

  .root {
    width: 100%;
    height: 100%;
    padding: 0.4rem;
    box-sizing: border-box;
    overflow: hidden;
  }
</style>
