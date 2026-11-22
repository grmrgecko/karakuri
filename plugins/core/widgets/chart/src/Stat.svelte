<script lang="ts">
  import type { Palette } from "./data/types";
  import { fieldDisplays, type FieldDisplay } from "./data/display";
  import { safeColor, withAlpha } from "./data/colors";
  import type { Frame } from "./data/types";

  // Stat is the stat panel: a big value per field with its name, an
  // optional sparkline, and the colour on the text or the background.
  let {
    frames,
    options,
    palette,
    width,
    height,
  }: { frames: Frame[]; options: Record<string, any>; palette: Palette; width: number; height: number } = $props();

  let values = $derived(fieldDisplays(frames, options.reduceOptions, palette));
  let horizontal = $derived(
    options.orientation === "horizontal" || (options.orientation !== "vertical" && width >= height),
  );
  let cols = $derived.by(() => {
    const n = values.length;
    if (n <= 1) return 1;
    if (options.orientation === "horizontal") return n;
    if (options.orientation === "vertical") return 1;
    return Math.max(1, Math.min(n, Math.round(Math.sqrt((n * width) / Math.max(height, 1)))));
  });
  let rows = $derived(Math.max(1, Math.ceil(values.length / cols)));
  let tileW = $derived(width / cols);
  let tileH = $derived(height / rows);

  let colorMode = $derived(options.colorMode ?? "value");
  let graphMode = $derived(options.graphMode ?? "area");
  let textMode = $derived(options.textMode ?? "auto");

  function showName(v: FieldDisplay): boolean {
    if (textMode === "value" || textMode === "none") return false;
    if (textMode === "name" || textMode === "value_and_name") return true;
    return values.length > 1 && !!v.name;
  }

  function valueText(v: FieldDisplay): string {
    return `${v.display.prefix ?? ""}${v.display.text}${v.display.suffix ?? ""}`;
  }

  function valueSize(v: FieldDisplay): number {
    const text = textMode === "name" ? v.name : valueText(v);
    const h = tileH * (graphMode === "area" && v.sparkline ? 0.45 : 0.6) * (showName(v) ? 0.75 : 1);
    return Math.max(10, Math.min(h, (tileW * 0.9) / Math.max(1, text.length * 0.58)));
  }

  function background(v: FieldDisplay): string {
    const c = safeColor(v.display.color, palette.series[0]);
    if (colorMode === "background_solid") return c;
    if (colorMode === "background") return `linear-gradient(120deg, ${withAlpha(c, 0.75)}, ${c})`;
    return "transparent";
  }

  function textColor(v: FieldDisplay): string {
    if (colorMode === "background" || colorMode === "background_solid") return "#ffffff";
    if (colorMode === "none") return palette.text;
    return safeColor(v.display.color, palette.text);
  }

  // sparkPath draws a field's points as an SVG path across the tile.
  function sparkPath(v: FieldDisplay, w: number, h: number, area: boolean): string {
    const s = v.sparkline;
    if (!s) return "";
    const pts: [number, number][] = [];
    let tMin = Infinity, tMax = -Infinity, vMin = Infinity, vMax = -Infinity;
    s.times.forEach((t, i) => {
      const y = s.values[i];
      if (!Number.isFinite(t) || y === null) return;
      tMin = Math.min(tMin, t); tMax = Math.max(tMax, t);
      vMin = Math.min(vMin, y); vMax = Math.max(vMax, y);
    });
    if (!Number.isFinite(tMin) || tMax === tMin) return "";
    const vSpan = vMax - vMin || 1;
    s.times.forEach((t, i) => {
      const y = s.values[i];
      if (!Number.isFinite(t) || y === null) return;
      pts.push([((t - tMin) / (tMax - tMin)) * w, h - ((y - vMin) / vSpan) * h * 0.9]);
    });
    if (pts.length < 2) return "";
    const line = pts.map((p, i) => `${i ? "L" : "M"}${p[0].toFixed(1)},${p[1].toFixed(1)}`).join("");
    return area ? `${line}L${pts[pts.length - 1][0].toFixed(1)},${h}L${pts[0][0].toFixed(1)},${h}Z` : line;
  }
</script>

<div class="stat" style:grid-template-columns="repeat({cols}, 1fr)" style:grid-template-rows="repeat({rows}, 1fr)">
  {#each values as v, i (i)}
    <div class="tile" style:background={background(v)} style:color={textColor(v)}
         class:center={options.justifyMode === "center" || !horizontal || (graphMode === "area" && !!v.sparkline)}>
      {#if graphMode === "area" && v.sparkline}
        {@const sw = Math.max(10, tileW)}
        {@const sh = Math.max(10, tileH * 0.4)}
        <svg class="spark" viewBox="0 0 {sw} {sh}" preserveAspectRatio="none" aria-hidden="true">
          <path d={sparkPath(v, sw, sh, true)}
                fill={colorMode === "value" || colorMode === "none" ? withAlpha(safeColor(v.display.color, palette.series[0]), 0.2) : "rgba(255,255,255,0.18)"} />
          <path d={sparkPath(v, sw, sh, false)} fill="none" stroke-width="1.5"
                stroke={colorMode === "value" || colorMode === "none" ? safeColor(v.display.color, palette.series[0]) : "rgba(255,255,255,0.6)"} />
        </svg>
      {/if}
      <div class="text">
        {#if showName(v)}
          <div class="name" style:font-size="{Math.max(10, valueSize(v) * 0.35)}px">{v.name}</div>
        {/if}
        {#if textMode !== "name" && textMode !== "none"}
          <div class="value" style:font-size="{valueSize(v)}px">
            {#if v.display.prefix}<span class="unit">{v.display.prefix}</span>{/if}{v.display.text}{#if v.display.suffix}<span class="unit">{v.display.suffix}</span>{/if}
          </div>
        {/if}
        {#if options.showPercentChange && v.percentChange !== undefined}
          <div class="change" style:font-size="{Math.max(10, valueSize(v) * 0.3)}px">
            {v.percentChange >= 0 ? "▲" : "▼"} {Math.abs(v.percentChange).toFixed(1)}%
          </div>
        {/if}
      </div>
    </div>
  {/each}
</div>

<style>
  .stat {
    display: grid;
    width: 100%;
    height: 100%;
    gap: 4px;
  }

  .tile {
    position: relative;
    display: flex;
    align-items: center;
    padding: 0 0.6rem;
    border-radius: var(--theme-radius-sm, 4px);
    overflow: hidden;
    min-width: 0;
    min-height: 0;
  }

  .tile.center {
    justify-content: center;
    text-align: center;
  }

  .text {
    position: relative;
    min-width: 0;
    max-width: 100%;
  }

  .name {
    opacity: 0.85;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .value {
    font-weight: 500;
    line-height: 1.1;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  .unit {
    font-size: 0.7em;
    opacity: 0.9;
  }

  .change {
    opacity: 0.85;
  }

  .spark {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    width: 100%;
    height: 40%;
  }
</style>
