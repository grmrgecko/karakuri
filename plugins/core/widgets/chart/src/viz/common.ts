// Shared pieces of the ECharts visualizations: the context they draw from,
// legend items, tooltips, and axes in the theme's colours. Every text that
// comes from data goes through escapeHtml before it reaches a tooltip, the
// only place ECharts renders HTML.

import type { Field, Frame, Palette, TimeRange } from "../data/types";
import { safeColor, withAlpha } from "../data/colors";
import { formatNumber } from "../data/display";
import { reduce, reducerName } from "../data/reducers";
import { isoDateTime } from "../data/units";

/** VizContext is what a visualization builder draws from. */
export interface VizContext {
  frames: Frame[];
  options: Record<string, any>;
  palette: Palette;
  range: TimeRange;
  animation: boolean;
  width: number;
  height: number;
  /** hidden holds the series names the viewer turned off in the legend. */
  hidden: Set<string>;
  /** nearest picks the tooltip entry closest to the pointer, for single mode. */
  nearest: (params: any[]) => any[];
}

/** LegendItem is one entry of the HTML legend drawn beside a chart. */
export interface LegendItem {
  name: string;
  color: string;
  hidden: boolean;
  /** values are the legend calculations, by calculation ID, as display text. */
  values: Record<string, string>;
}

/** VizResult is a built chart, or the message shown instead of one. */
export interface VizResult {
  option?: Record<string, any>;
  message?: string;
  legend?: LegendItem[];
  /** zoomable charts have a time x axis a drag can zoom. */
  zoomable?: boolean;
}

/** escapeHtml makes text safe to place in tooltip markup. */
export function escapeHtml(text: unknown): string {
  return String(text ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** swatch is a colour dot for tooltip rows. */
export function swatch(color: string): string {
  return `<span style="display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px;background:${safeColor(color, "#888")}"></span>`;
}

/** tooltipRow writes one tooltip line from escaped parts. */
export function tooltipRow(color: string, name: string, value: string): string {
  return `<div style="display:flex;align-items:center;gap:12px;justify-content:space-between"><span>${swatch(color)}${escapeHtml(name)}</span><b>${escapeHtml(value)}</b></div>`;
}

/** tooltipHeader writes the tooltip's first line. */
export function tooltipHeader(text: string): string {
  return `<div style="margin-bottom:4px;opacity:0.8">${escapeHtml(text)}</div>`;
}

/** tooltipBase is the tooltip's look in the theme's colours. */
export function tooltipBase(palette: Palette): Record<string, any> {
  return {
    backgroundColor: palette.surface,
    borderColor: palette.border,
    textStyle: { color: palette.text, fontFamily: palette.font, fontSize: 12 },
    confine: true,
    extraCssText: "box-shadow:0 2px 8px rgba(0,0,0,0.3);",
  };
}

/** textStyle is the chart-wide text style. */
export function textStyle(palette: Palette): Record<string, any> {
  return { color: palette.text, fontFamily: palette.font };
}

/** valueAxis styles a value axis. */
export function axisStyle(palette: Palette): Record<string, any> {
  return {
    axisLine: { lineStyle: { color: palette.border } },
    axisTick: { lineStyle: { color: palette.border } },
    axisLabel: { color: palette.textMuted, fontFamily: palette.font, fontSize: 11 },
    splitLine: { lineStyle: { color: withAlpha(palette.border, 0.6) } },
    nameTextStyle: { color: palette.textMuted, fontFamily: palette.font },
  };
}

/** timeAxis returns an x axis spanning the chart's range. */
export function timeAxis(ctx: VizContext, extra: Record<string, any> = {}): Record<string, any> {
  const style = axisStyle(ctx.palette);
  return {
    type: "time",
    min: ctx.range.from,
    max: ctx.range.to,
    ...style,
    axisLabel: { ...style.axisLabel, hideOverlap: true },
    splitLine: { show: false },
    ...extra,
  };
}

/** grid leaves room for axis labels. */
export function grid(extra: Record<string, any> = {}): Record<string, any> {
  return { left: 8, right: 16, top: 12, bottom: 8, containLabel: true, ...extra };
}

/** legendItem builds a legend entry with the legend's calculations. */
export function legendItem(name: string, color: string, field: Field | undefined, ctx: VizContext): LegendItem {
  const calcs: string[] = Array.isArray(ctx.options.legend?.calcs) ? ctx.options.legend.calcs : [];
  const values: Record<string, string> = {};
  if (field) for (const calc of calcs) values[calc] = formatCalc(field, calc);
  return { name, color, hidden: ctx.hidden.has(name), values };
}

function formatCalc(field: Field, calc: string): string {
  const v = reduce(field.values, calc);
  if (calc === "count" || calc === "distinctCount" || calc === "changeCount") return String(v ?? "-");
  if (typeof v === "boolean") return v ? "true" : "false";
  return formatNumber(field, v);
}

/** calcName names a legend calculation column. */
export { reducerName as calcName };

/** timeText writes a tooltip's time. */
export function timeText(ms: unknown): string {
  return isoDateTime(Number(ms));
}

/** contrastText picks dark or light text for a fill colour. */
export function contrastText(fill: string, palette: Palette): string {
  const m = /^#([0-9a-f]{6})$/i.exec(fill) ?? /^#([0-9a-f]{3})$/i.exec(fill);
  if (!m) return palette.text;
  let h = m[1];
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255);
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  return lum > 0.55 ? "#11111b" : "#f5f5f5";
}

/** tooltipMode reads the tooltip option: single, multi, or none. */
export function tooltipMode(options: Record<string, any>): "single" | "multi" | "none" {
  const m = options.tooltip?.mode;
  return m === "multi" || m === "none" ? m : "single";
}

/** showValue reads a show-values option against how crowded the chart is. */
export function showValues(mode: unknown, crowded: boolean): boolean {
  if (mode === "always") return true;
  if (mode === "never") return false;
  return !crowded;
}
