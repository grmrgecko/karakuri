<script lang="ts" module>
  /** Probe shares the chart and the pointer with tooltip formatters built before the chart existed. */
  export interface Probe {
    chart: any;
    x: number;
    y: number;
  }
</script>

<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { echarts } from "./viz/echarts";

  let {
    option,
    zoomable = false,
    probe,
    onzoom,
    onzoomout,
    ondrawn,
  }: {
    option: Record<string, any>;
    zoomable?: boolean;
    probe: Probe;
    onzoom?: (from: number, to: number) => void;
    onzoomout?: () => void;
    ondrawn?: () => void;
  } = $props();

  let el: HTMLDivElement;
  let chart: any = null;
  let observer: ResizeObserver | null = null;

  onMount(() => {
    chart = echarts.init(el, null, { renderer: "svg" });
    probe.chart = chart;
    chart.getZr().on("mousemove", (e: any) => {
      probe.x = e.offsetX;
      probe.y = e.offsetY;
    });
    chart.getZr().on("dblclick", () => onzoomout?.());
    chart.on("brushEnd", (e: any) => {
      const range = e?.areas?.[0]?.coordRange;
      chart.dispatchAction({ type: "brush", areas: [] });
      if (Array.isArray(range) && range.length === 2) {
        const [from, to] = range.map((v: number) => Math.round(v));
        if (to - from > 1000) onzoom?.(from, to);
      }
    });
    chart.on("finished", () => ondrawn?.());
    // Resizing on the next frame keeps a resize from re-entering the observer.
    observer = new ResizeObserver(() => requestAnimationFrame(() => chart?.resize()));
    observer.observe(el);
  });

  onDestroy(() => {
    observer?.disconnect();
    chart?.dispose();
    chart = null;
    probe.chart = null;
  });

  $effect(() => {
    draw(option, zoomable);
  });

  function draw(next: Record<string, any>, zoom: boolean): void {
    if (!chart) return;
    const full = zoom
      ? {
          ...next,
          brush: {
            xAxisIndex: "all",
            brushType: "lineX",
            brushMode: "single",
            transformable: false,
            throttleType: "debounce",
            brushStyle: { color: "rgba(137, 180, 250, 0.2)", borderColor: "rgba(137, 180, 250, 0.6)", borderWidth: 1 },
            outOfBrush: { colorAlpha: 1 },
          },
          toolbox: { show: false },
        }
      : next;
    chart.setOption(full, { notMerge: true, lazyUpdate: false });
    if (zoom) {
      // Dragging across the time axis selects a range.
      chart.dispatchAction({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
    }
    // A chart with nothing to animate may never fire finished; the next
    // frame after a synchronous SVG render is drawn either way.
    requestAnimationFrame(() => requestAnimationFrame(() => ondrawn?.()));
  }
</script>

<div class="echart" bind:this={el}></div>

<style>
  /* Out of flow, so the drawn SVG never sizes the widget around it. */
  .echart {
    position: absolute;
    inset: 0;
  }
</style>
