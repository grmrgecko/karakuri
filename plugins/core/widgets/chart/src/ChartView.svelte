<script lang="ts">
  import { tick } from "svelte";
  import type { Frame, Palette, TimeRange } from "./data/types";
  import type { QueryFailure } from "./data/frames";
  import { build, HTML_VIZ } from "./viz/index";
  import type { VizContext } from "./viz/common";
  import EChart, { type Probe } from "./EChart.svelte";
  import Legend from "./Legend.svelte";
  import Stat from "./Stat.svelte";
  import BarGauge from "./BarGauge.svelte";
  import Table from "./Table.svelte";

  // ChartView draws prepared frames in the chosen visualization, with its
  // legend and any query failures. The widget and the editor's preview share
  // it.
  let {
    viz,
    options,
    frames,
    failures = [],
    palette,
    range,
    animation = true,
    noValue = "No data",
    loaded = true,
    onzoom,
    onzoomout,
    ondrawn,
  }: {
    viz: string;
    options: Record<string, any>;
    frames: Frame[];
    failures?: QueryFailure[];
    palette: Palette;
    range: TimeRange;
    animation?: boolean;
    noValue?: string;
    loaded?: boolean;
    onzoom?: (from: number, to: number) => void;
    onzoomout?: () => void;
    ondrawn?: () => void;
  } = $props();

  let width = $state(0);
  let height = $state(0);
  let hidden = $state<Set<string>>(new Set());
  const probe: Probe = { chart: null, x: 0, y: 0 };

  // nearest keeps the tooltip entry drawn closest to the pointer, which is
  // what the single tooltip mode shows.
  function nearest(params: any[]): any[] {
    if (!probe.chart || params.length < 2) return params;
    let best = params[0];
    let bestDistance = Infinity;
    for (const p of params) {
      const value = Array.isArray(p.value) ? p.value : [p.axisValue, p.value];
      if (value[1] === null || value[1] === undefined) continue;
      const px = probe.chart.convertToPixel({ seriesIndex: p.seriesIndex }, value);
      const d = Array.isArray(px) ? Math.abs(px[1] - probe.y) : Infinity;
      if (d < bestDistance) {
        bestDistance = d;
        best = p;
      }
    }
    return [best];
  }

  let hasData = $derived(frames.some((f) => f.fields.some((x) => x.type !== "time" && x.values.length > 0)));
  let html = $derived(HTML_VIZ.has(viz));

  let result = $derived.by(() => {
    if (html || !hasData || width <= 0 || height <= 0) return null;
    const ctx: VizContext = { frames, options, palette, range, animation, width, height, hidden, nearest };
    return build(viz, ctx);
  });

  let legendOpts = $derived(options.legend ?? {});
  let legendShown = $derived(
    !!result?.legend?.length && legendOpts.showLegend !== false && legendOpts.displayMode !== "hidden",
  );

  function toggle(name: string, only: boolean): void {
    const names = (result?.legend ?? []).map((l) => l.name);
    if (only) {
      const isolated = names.every((n) => n === name || hidden.has(n)) && !hidden.has(name);
      hidden = isolated ? new Set() : new Set(names.filter((n) => n !== name));
    } else {
      const next = new Set(hidden);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      hidden = next;
    }
  }

  // A view with nothing for ECharts to draw is drawn once Svelte has
  // rendered it.
  $effect(() => {
    if (result?.option) return;
    void loaded;
    void frames;
    void viz;
    void width;
    tick().then(() => requestAnimationFrame(() => ondrawn?.()));
  });
</script>

<div class="view" class:right={legendOpts.placement === "right"}>
  <div class="plot" bind:clientWidth={width} bind:clientHeight={height}>
    {#if !hasData}
      <div class="message">{loaded ? noValue : ""}</div>
    {:else if viz === "stat"}
      <Stat {frames} {options} {palette} {width} {height} />
    {:else if viz === "bargauge"}
      <BarGauge {frames} {options} {palette} {width} {height} />
    {:else if viz === "table"}
      <Table {frames} {options} {palette} {height} />
    {:else if result?.message}
      <div class="message">{result.message}</div>
    {:else if result?.option}
      <EChart option={result.option} zoomable={!!result.zoomable && !!onzoom} {probe} {onzoom} {onzoomout} {ondrawn} />
    {/if}
  </div>
  {#if legendShown && result?.legend}
    <Legend items={result.legend} mode={legendOpts.displayMode} placement={legendOpts.placement}
            calcs={viz === "piechart" ? (legendOpts.values ?? []) : (legendOpts.calcs ?? [])} ontoggle={toggle} />
  {/if}
  {#if failures.length}
    <div class="failures" title={failures.map((f) => `${f.refId}: ${f.message}`).join("\n")}>
      {#each failures as f}
        <div class="failure">{f.refId}: {f.message}</div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .view {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    min-height: 0;
  }

  .view.right {
    flex-direction: row;
  }

  .plot {
    position: relative;
    flex: 1;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .message {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 0.5rem;
    color: var(--theme-text-muted, #a6adc8);
    font-size: 0.85rem;
  }

  .failures {
    position: absolute;
    top: 2px;
    right: 2px;
    max-width: 70%;
    padding: 0.2rem 0.4rem;
    border-radius: 4px;
    background: var(--theme-error-bg-subtle, rgba(243, 139, 168, 0.15));
    border: 1px solid var(--theme-error, #f38ba8);
    color: var(--theme-error, #f38ba8);
    font-size: 0.7rem;
    z-index: 3;
  }

  .failure {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
