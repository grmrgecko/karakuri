<svelte:options customElement="history-graph-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { formatValue, numeric, parseConfig, seriesColor, type Series } from "./config";
  import {
    areaPath,
    decimate,
    linePath,
    niceTicks,
    timeLabel,
    timeTicks,
    valueDomain,
    type Point,
  } from "./graph";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  // SeriesData is what a series has gathered: its readings in time order, and
  // what its source says about itself.
  interface SeriesData {
    points: Point[];
    unit: string;
    sourceName: string;
    error: string;
    loading: boolean;
  }

  // The readings live outside Svelte's state: a week of a busy sensor is tens
  // of thousands of points, and proxying each one would cost more than the
  // drawing. version is bumped whenever any of them changes.
  let store = new Map<string, SeriesData>();
  let version = $state(0);
  let now = $state(Date.now());

  let generation = 0;
  let unsubscribes: Array<() => void> = [];
  let minuteTimer: ReturnType<typeof setInterval> | null = null;

  const bridge = () => (window as any).__karakuri;

  // Only what a series reads from rebinds it: renaming or recolouring a line
  // redraws without refetching its history.
  let bindKey = $derived(
    JSON.stringify({
      hours: cfg.hours,
      series: cfg.series.map((s) => [s.id, s.source, s.connectionId, s.entityId, s.controlId, s.deviceId, s.deviceField]),
    }),
  );

  $effect(() => {
    void bindKey;
    untrack(() => bind());
    return () => unbind();
  });

  onDestroy(() => {
    unbind();
    if (minuteTimer) clearInterval(minuteTimer);
    resizeObserver?.disconnect();
  });

  // Points older than the window are dropped as time passes. One per minute
  // is plenty: a pixel of a day-wide graph is already several minutes.
  minuteTimer = setInterval(() => {
    now = Date.now();
    prune();
  }, 60_000);

  function unbind(): void {
    generation += 1;
    for (const off of unsubscribes) off();
    unsubscribes = [];
  }

  function windowStart(): number {
    return now - cfg.hours * 3_600_000;
  }

  function bind(): void {
    const gen = ++generation;
    now = Date.now();
    store = new Map();
    for (const series of cfg.series) {
      store.set(series.id, { points: [], unit: "", sourceName: "", error: "", loading: true });
    }
    version += 1;

    // Each source's list is read once per bind, however many series use it.
    const lists = new Map<string, Promise<any[]>>();
    const list = (path: string): Promise<any[]> => {
      let request = lists.get(path);
      if (!request) {
        request = bridge()
          .api.get(path)
          .then((res: any) => (Array.isArray(res) ? res : (res?.data ?? [])));
        lists.set(path, request!);
      }
      return request!;
    };

    for (const series of cfg.series) {
      switch (series.source) {
        case "ha":
          void bindHA(gen, series, list);
          break;
        case "control":
          void bindControl(gen, series, list);
          break;
        case "mqtt":
          void bindDevice(gen, series, list);
          break;
      }
    }
  }

  function track(gen: number, off: (() => void) | undefined): void {
    if (!off) return;
    if (gen !== generation) off();
    else unsubscribes.push(off);
  }

  function patch(gen: number, id: string, change: Partial<SeriesData>): void {
    if (gen !== generation) return;
    const data = store.get(id);
    if (!data) return;
    Object.assign(data, change);
    version += 1;
  }

  // append adds a live reading. Readings arrive in order, but one stamped
  // before the last kept point (a history read finishing after a live push)
  // is dropped rather than drawn backwards.
  function append(gen: number, id: string, t: number, value: unknown): void {
    if (gen !== generation) return;
    const data = store.get(id);
    const v = numeric(value);
    if (!data || v === null) return;
    const last = data.points[data.points.length - 1];
    if (last && t < last.t) return;
    data.points.push({ t, v });
    now = Math.max(Date.now(), t);
    version += 1;
  }

  // prune keeps the last reading before the window so the line starts at the
  // left edge with the value it held then.
  function prune(): void {
    const start = windowStart();
    let changed = false;
    for (const data of store.values()) {
      let cut = 0;
      while (cut + 1 < data.points.length && data.points[cut + 1].t <= start) cut++;
      if (cut > 0) {
        data.points = data.points.slice(cut);
        changed = true;
      }
    }
    if (changed) version += 1;
  }

  async function bindHA(gen: number, series: Series, list: (path: string) => Promise<any[]>): Promise<void> {
    if (!series.connectionId || !series.entityId) {
      patch(gen, series.id, { loading: false, error: "no entity selected" });
      return;
    }
    const conn = encodeURIComponent(series.connectionId);

    // Live states that arrive while history loads are kept and merged after it.
    track(
      gen,
      bridge()?.subscribeTopic(`ha:${series.connectionId}:state:${series.entityId}`, (data: any) => {
        const state = data?.new_state;
        if (!state) return;
        const at = Date.parse(state.last_changed || state.last_updated || "");
        const unit = state.attributes?.unit_of_measurement;
        const name = state.attributes?.friendly_name;
        if (unit !== undefined || name !== undefined) {
          patch(gen, series.id, {
            ...(unit !== undefined ? { unit: String(unit) } : {}),
            ...(name !== undefined ? { sourceName: String(name) } : {}),
          });
        }
        append(gen, series.id, Number.isFinite(at) ? at : Date.now(), state.state);
      }),
    );

    if (!series.name) {
      list(`/connections/${conn}/ha/states`)
        .then((states) => {
          const entity = states.find((s) => s.entity_id === series.entityId);
          const name = entity?.attributes?.friendly_name;
          if (name) patch(gen, series.id, { sourceName: String(name) });
        })
        .catch(() => {});
    }

    try {
      const res = await bridge().api.get(
        `/connections/${conn}/ha/history/${encodeURIComponent(series.entityId)}?hours=${cfg.hours}`,
      );
      if (gen !== generation) return;
      const body = res && Array.isArray(res.points) ? res : (res?.data ?? res);
      const history: Point[] = [];
      for (const p of Array.isArray(body?.points) ? body.points : []) {
        const t = Number(p?.t);
        const v = numeric(p?.v);
        if (Number.isFinite(t) && v !== null) history.push({ t, v });
      }
      history.sort((a, b) => a.t - b.t);
      const data = store.get(series.id);
      if (!data) return;
      const lastHistory = history.length ? history[history.length - 1].t : -Infinity;
      const live = data.points.filter((p) => p.t > lastHistory);
      patch(gen, series.id, {
        points: [...history, ...live],
        unit: data.unit || String(body?.unit ?? ""),
        loading: false,
        error: "",
      });
    } catch (e: any) {
      patch(gen, series.id, { loading: false, error: e?.message || "cannot read history" });
    }
  }

  async function bindControl(gen: number, series: Series, list: (path: string) => Promise<any[]>): Promise<void> {
    if (!series.controlId) {
      patch(gen, series.id, { loading: false, error: "no control selected" });
      return;
    }
    let control: any;
    try {
      control = (await list("/controls")).find((c) => c.id === series.controlId);
    } catch (e: any) {
      patch(gen, series.id, { loading: false, error: e?.message || "cannot read controls" });
      return;
    }
    if (gen !== generation) return;
    if (!control) {
      patch(gen, series.id, { loading: false, error: "control not found" });
      return;
    }
    // A control keeps no history, so its line starts now.
    patch(gen, series.id, {
      sourceName: String(control.name ?? ""),
      unit: String(control.options?.unit ?? ""),
      loading: false,
    });
    append(gen, series.id, Date.now(), control.value);
    track(
      gen,
      bridge()?.subscribeTopic(`control:${series.controlId}`, (data: any) => {
        if (data?.deleted) {
          patch(gen, series.id, { error: "control deleted" });
          return;
        }
        if (data?.control) append(gen, series.id, Date.now(), data.control.value);
      }),
    );
  }

  // deviceReading picks the series' value out of a device's accumulated state.
  function deviceReading(series: Series, state: unknown): unknown {
    if (!state || typeof state !== "object") return state;
    const values = state as Record<string, unknown>;
    const field = (series.deviceField || "").trim();
    if (field) return values[field];
    const keys = Object.keys(values);
    return keys.length === 1 ? values[keys[0]] : undefined;
  }

  async function bindDevice(gen: number, series: Series, list: (path: string) => Promise<any[]>): Promise<void> {
    if (!series.deviceId) {
      patch(gen, series.id, { loading: false, error: "no device selected" });
      return;
    }
    let device: any;
    try {
      device = (await list("/mqtt_devices")).find((d) => d.id === series.deviceId);
    } catch (e: any) {
      patch(gen, series.id, { loading: false, error: e?.message || "cannot read devices" });
      return;
    }
    if (gen !== generation) return;
    if (!device) {
      patch(gen, series.id, { loading: false, error: "device not found" });
      return;
    }
    const field = (series.deviceField || "").trim();
    patch(gen, series.id, {
      sourceName: field ? `${device.name ?? "Device"} ${field}` : String(device.name ?? ""),
      unit: field ? "" : String(device.options?.unit ?? ""),
      loading: false,
    });
    append(gen, series.id, Date.now(), deviceReading(series, device.state));
    track(
      gen,
      bridge()?.subscribeTopic(`mqttdevice:${series.deviceId}`, (data: any) => {
        if (data?.error) {
          patch(gen, series.id, { error: String(data.error) });
          return;
        }
        if (store.get(series.id)?.error) patch(gen, series.id, { error: "" });
        append(gen, series.id, Date.now(), deviceReading(series, data?.state));
      }),
    );
  }

  // ---- Layout ----

  let plotEl = $state<HTMLElement | null>(null);
  let width = $state(0);
  let height = $state(0);
  let resizeObserver: ResizeObserver | null = null;

  $effect(() => {
    if (!plotEl) return;
    resizeObserver?.disconnect();
    resizeObserver = new ResizeObserver((entries) => {
      const box = entries[0]?.contentRect;
      if (!box) return;
      width = Math.floor(box.width);
      height = Math.floor(box.height);
    });
    resizeObserver.observe(plotEl);
    return () => resizeObserver?.disconnect();
  });

  // fontSize follows the cell so labels read on a wall panel and still fit a
  // phone.
  let fontSize = $derived(Math.max(10, Math.min(18, Math.min(width, height || width) * 0.05)));

  interface Drawn {
    id: string;
    color: string;
    line: string;
    area: string;
  }

  let chart = $derived.by(() => {
    void version;
    const end = now;
    const start = end - cfg.hours * 3_600_000;

    // Each series is clipped to the window, with the reading it held at the
    // left edge moved onto that edge.
    const visible = new Map<string, Point[]>();
    const values: number[] = [];
    for (const series of cfg.series) {
      const points = store.get(series.id)?.points ?? [];
      const kept: Point[] = [];
      for (let i = 0; i < points.length; i++) {
        const p = points[i];
        if (p.t < start) {
          if (i + 1 < points.length && points[i + 1].t <= start) continue;
          kept.push({ t: start, v: p.v });
        } else if (p.t <= end) {
          kept.push(p);
        }
      }
      for (const p of kept) values.push(p.v);
      visible.set(series.id, kept);
    }

    const [domainLo, domainHi] = valueDomain(values, cfg.min, cfg.max);
    const valueTickCount = Math.max(2, Math.floor(height / (fontSize * 3.2)));
    const valueTicks = niceTicks(domainLo, domainHi, valueTickCount).filter((v) => v >= domainLo && v <= domainHi);

    const labelWidth = Math.max(
      ...valueTicks.map((v) => formatValue(v, "").length),
      1,
    ) * fontSize * 0.62 + fontSize * 0.6;
    const left = Math.ceil(labelWidth);
    const right = Math.ceil(fontSize * 0.6);
    const top = Math.ceil(fontSize * 0.6);
    const bottom = Math.ceil(fontSize * 1.8);
    const plotW = Math.max(1, width - left - right);
    const plotH = Math.max(1, height - top - bottom);

    const x = (t: number) => left + ((t - start) / (end - start)) * plotW;
    const y = (v: number) => top + (1 - (v - domainLo) / (domainHi - domainLo)) * plotH;
    const floorY = top + plotH;

    const times = timeTicks(start, end, Math.floor(plotW / (fontSize * 5)));
    const span = end - start;

    const drawn: Drawn[] = cfg.series.map((series, index) => {
      const points = decimate(visible.get(series.id) ?? [], x);
      const step = cfg.style === "step";
      const line = linePath(points, x, y, step, x(end));
      const area =
        cfg.style === "area" && points.length > 0 ? areaPath(line, x(points[0].t), x(end), floorY) : "";
      return { id: series.id, color: seriesColor(series, index), line, area };
    });

    return {
      left,
      top,
      plotW,
      plotH,
      floorY,
      valueTicks: valueTicks.map((v) => ({ v, y: y(v), label: formatValue(v, "") })),
      timeTicks: times.ticks.map((t) => ({ t, x: x(t), label: timeLabel(t, times.step, span) })),
      drawn,
    };
  });

  let legend = $derived.by(() => {
    void version;
    return cfg.series.map((series, index) => {
      const data = store.get(series.id);
      const last = data?.points[data.points.length - 1];
      return {
        id: series.id,
        color: seriesColor(series, index),
        name: series.name || data?.sourceName || series.entityId || "Series",
        current: last ? formatValue(last.v, data?.unit ?? "") : "",
        error: data?.error ?? "",
        loading: !!data?.loading,
      };
    });
  });
</script>

<div class="graph">
  {#if cfg.series.length === 0}
    <span class="hint">Add a series in the widget settings.</span>
  {:else}
    {#if cfg.showLegend}
      <ul class="legend" style:font-size="{fontSize}px">
        {#each legend as item (item.id)}
          <li>
            <span class="swatch" style:background={item.color}></span>
            <span class="name">{item.name}</span>
            {#if item.error}
              <span class="error">{item.error}</span>
            {:else if cfg.showCurrent && item.current}
              <span class="current">{item.current}</span>
            {:else if item.loading}
              <span class="muted">loading</span>
            {/if}
          </li>
        {/each}
      </ul>
    {:else}
      {@const failed = legend.filter((item) => item.error)}
      {#if failed.length > 0}
        <span class="error banner" style:font-size="{fontSize}px">
          {failed.map((item) => `${item.name}: ${item.error}`).join("; ")}
        </span>
      {/if}
    {/if}
    <div class="plot" bind:this={plotEl}>
      {#if width > 0 && height > 0}
        <svg {width} {height} role="img" aria-label="History graph">
          <g class="grid">
            {#each chart.valueTicks as tick (tick.v)}
              <line x1={chart.left} x2={chart.left + chart.plotW} y1={tick.y} y2={tick.y} />
            {/each}
            {#each chart.timeTicks as tick (tick.t)}
              <line x1={tick.x} x2={tick.x} y1={chart.top} y2={chart.floorY} />
            {/each}
          </g>
          <g class="labels" style:font-size="{fontSize * 0.8}px">
            {#each chart.valueTicks as tick (tick.v)}
              <text x={chart.left - fontSize * 0.4} y={tick.y} text-anchor="end" dominant-baseline="middle">
                {tick.label}
              </text>
            {/each}
            {#each chart.timeTicks as tick (tick.t)}
              <text x={tick.x} y={chart.floorY + fontSize * 1.2} text-anchor="middle">{tick.label}</text>
            {/each}
          </g>
          {#each chart.drawn as series (series.id)}
            {#if series.area}
              <path class="area" d={series.area} style:fill={series.color} />
            {/if}
            {#if series.line}
              <path class="line" d={series.line} style:stroke={series.color} />
            {/if}
          {/each}
        </svg>
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
    box-sizing: border-box;
  }

  .graph {
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
    padding: 0.5rem;
    overflow: hidden;
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
  }

  .legend {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25em 1.1em;
    margin: 0;
    padding: 0;
    list-style: none;
    flex-shrink: 0;
  }

  .legend li {
    display: flex;
    align-items: center;
    gap: 0.4em;
    min-width: 0;
  }

  .swatch {
    flex-shrink: 0;
    width: 0.8em;
    height: 0.8em;
    border-radius: 50%;
  }

  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .current {
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }

  .muted {
    opacity: 0.5;
  }

  .error {
    color: var(--theme-error, #f38ba8);
  }

  .error.banner {
    flex-shrink: 0;
  }

  .plot {
    flex: 1;
    min-height: 0;
    position: relative;
  }

  svg {
    position: absolute;
    inset: 0;
    display: block;
    overflow: visible;
  }

  .grid line {
    stroke: var(--theme-surface1, #45475a);
    stroke-width: 1;
    opacity: 0.5;
    shape-rendering: crispEdges;
  }

  .labels text {
    fill: var(--theme-subtext, #a6adc8);
    font-variant-numeric: tabular-nums;
  }

  .line {
    fill: none;
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
    vector-effect: non-scaling-stroke;
  }

  .area {
    stroke: none;
    opacity: 0.18;
  }

  .hint {
    margin: auto;
    font-size: 0.9rem;
    opacity: 0.6;
    text-align: center;
  }
</style>
