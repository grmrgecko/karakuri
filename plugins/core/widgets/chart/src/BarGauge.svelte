<script lang="ts">
  import type { Frame, Palette } from "./data/types";
  import { fieldDisplays, type FieldDisplay } from "./data/display";
  import { activeThreshold, fieldMinMax, resolveColor, safeColor, sortedSteps, thresholdValue, withAlpha } from "./data/colors";

  // BarGauge is the bar gauge: one bar per reduced value, drawn basic,
  // as a gradient through the threshold colours, or as LCD cells.
  let {
    frames,
    options,
    palette,
    width,
    height,
  }: { frames: Frame[]; options: Record<string, any>; palette: Palette; width: number; height: number } = $props();

  let values = $derived(fieldDisplays(frames, options.reduceOptions, palette));
  let vertical = $derived(options.orientation === "vertical" || (options.orientation === "auto" && height > width * 1.2));
  let mode = $derived(options.displayMode ?? "gradient");
  let valueMode = $derived(options.valueMode ?? "color");
  let showUnfilled = $derived(options.showUnfilled !== false);
  let namePlacement = $derived(options.namePlacement ?? "auto");
  let minSize = $derived(Math.max(8, Number(options.minVizHeight ?? options.minVizWidth ?? 16) || 16));
  let maxSize = $derived(Math.max(minSize, Number(options.maxVizHeight ?? 300) || 300));
  let barSize = $derived.by(() => {
    const n = Math.max(1, values.length);
    const label = !vertical && namePlacement !== "left" && nameShown() ? 18 : 0;
    const room = (vertical ? width : height) / n - 8 - label;
    return Math.max(minSize, Math.min(maxSize, room));
  });

  function fraction(v: FieldDisplay): number {
    const { min, max } = fieldMinMax(v.field, true);
    const n = v.display.numeric;
    if (!Number.isFinite(n)) return 0;
    return Math.max(0, Math.min(1, (n - min) / (max - min)));
  }

  function valueText(v: FieldDisplay): string {
    return `${v.display.prefix ?? ""}${v.display.text}${v.display.suffix ?? ""}`;
  }

  // gradient runs through the threshold colours up to the value, so the bar's
  // end shows the colour the value has reached.
  function gradient(v: FieldDisplay, f: number): string {
    const { min, max } = fieldMinMax(v.field, true);
    const steps = sortedSteps(v.field.config.thresholds);
    const mode = v.field.config.color?.mode ?? "thresholds";
    const end = safeColor(v.display.color, palette.series[0]);
    if (mode !== "thresholds" || steps.length < 2 || f <= 0) return `linear-gradient(${vertical ? "0deg" : "90deg"}, ${withAlpha(end, 0.4)}, ${end})`;
    const stops: string[] = [];
    for (const s of steps) {
      const t = thresholdValue(s, v.field.config.thresholds, min, max);
      const at = t === null ? 0 : (t - min) / (max - min) / f;
      if (at > 1) break;
      stops.push(`${safeColor(resolveColor(s.color, palette), end)} ${Math.max(0, at * 100).toFixed(1)}%`);
    }
    stops.push(`${end} 100%`);
    return `linear-gradient(${vertical ? "0deg" : "90deg"}, ${stops.join(", ")})`;
  }

  const CELLS = 20;

  function cellColor(v: FieldDisplay, i: number): string {
    const { min, max } = fieldMinMax(v.field, true);
    const at = min + ((i + 0.5) / CELLS) * (max - min);
    const mode = v.field.config.color?.mode ?? "thresholds";
    const c = mode === "thresholds" ? resolveColor(activeThreshold(at, v.field.config.thresholds, min, max).color, palette) : v.display.color;
    return safeColor(c, palette.series[0]);
  }

  function nameShown(): boolean {
    return namePlacement !== "hidden" && (values.length > 1 || namePlacement === "top" || namePlacement === "left");
  }
</script>

<div class="bars" class:vertical>
  {#each values as v, i (i)}
    {@const f = fraction(v)}
    <div class="row" class:left={!vertical && namePlacement === "left"} style:--size="{barSize}px">
      {#if nameShown()}<div class="name">{v.name}</div>{/if}
      <div class="track-wrap">
        {#if mode === "lcd"}
          <div class="track lcd">
            {#each Array(CELLS) as _, c}
              <div class="cell" style:background={cellColor(v, c)} style:opacity={(c + 0.5) / CELLS <= f ? 1 : showUnfilled ? 0.15 : 0}></div>
            {/each}
          </div>
        {:else}
          <div class="track" style:background={showUnfilled ? withAlpha(palette.border, 0.5) : "transparent"}>
            <div class="fill"
                 style:width={vertical ? "100%" : `${f * 100}%`}
                 style:height={vertical ? `${f * 100}%` : "100%"}
                 style:background={mode === "gradient" ? gradient(v, f) : safeColor(v.display.color, palette.series[0])}></div>
          </div>
        {/if}
        {#if valueMode !== "hidden"}
          <div class="value" style:color={valueMode === "color" ? safeColor(v.display.color, palette.text) : palette.text}
               style:font-size="{Math.max(10, Math.min(barSize * 0.6, 28))}px">{valueText(v)}</div>
        {/if}
      </div>
    </div>
  {/each}
</div>

<style>
  .bars {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 8px;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .bars.vertical {
    flex-direction: row;
    align-items: stretch;
  }

  .row {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .row.left {
    flex-direction: row;
    align-items: center;
    gap: 8px;
  }

  .row.left .name {
    width: 25%;
    flex: none;
  }

  .vertical .row {
    width: var(--size);
    flex-direction: column-reverse;
    align-items: center;
  }

  .name {
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .track-wrap {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
    flex: 1;
  }

  .vertical .track-wrap {
    flex-direction: column-reverse;
    width: 100%;
  }

  .track {
    position: relative;
    flex: 1;
    height: var(--size);
    border-radius: 2px;
    overflow: hidden;
    display: flex;
    align-items: flex-end;
  }

  .vertical .track {
    height: auto;
    width: 100%;
    min-height: 20px;
  }

  .fill {
    border-radius: 2px;
  }

  .lcd {
    display: flex;
    gap: 2px;
    align-items: stretch;
  }

  .vertical .lcd {
    flex-direction: column-reverse;
  }

  .cell {
    flex: 1;
    border-radius: 1px;
  }

  .value {
    flex: none;
    font-weight: 500;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
</style>
