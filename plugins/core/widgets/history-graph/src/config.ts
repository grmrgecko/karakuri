// Each series is one host binding, so the manifest declares them with
// `bindings: "series"` and placing the graph is what lets a dashboard's
// viewers read those readings. A graph never presses anything, so every
// series is written display only.

import { parseAttr } from "../../shared/dashboardLive";

export type SeriesSource = "ha" | "control" | "mqtt";

export interface Series {
  id: string;
  source: SeriesSource;
  connectionId?: string;
  entityId?: string;
  controlId?: string;
  deviceId?: string;
  /** deviceField picks one of an MQTT device's values; empty reads its single value. */
  deviceField?: string;
  /** name labels the line; empty uses the source's own name. */
  name?: string;
  /** color is any CSS colour; empty takes the next theme colour. */
  color?: string;
  displayOnly: true;
}

export interface GraphConfig {
  series: Series[];
  hours: number;
  style: "line" | "area" | "step";
  /** min and max fix the value axis; null fits it to the data. */
  min: number | null;
  max: number | null;
  showLegend: boolean;
  showCurrent: boolean;
}

export const MIN_HOURS = 1;
export const MAX_HOURS = 168;

/** newSeriesId returns an ID for a series row. */
export function newSeriesId(): string {
  return Math.random().toString(36).slice(2, 10);
}

function optionalNumber(value: unknown): number | null {
  if (value === null || value === undefined || value === "") return null;
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

/**
 * hoursReference returns the stored hours when they are a dashboard variable
 * reference such as ${range}. The host substitutes it before the widget runs,
 * so only the settings panel, which edits the stored template, sees it.
 */
export function hoursReference(attr: string | Record<string, unknown>): string | null {
  const hours = parseAttr(attr).hours;
  return typeof hours === "string" && /^\$\{[A-Za-z][A-Za-z0-9_]{0,31}\}$/.test(hours) ? hours : null;
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): GraphConfig {
  const fields = parseAttr(attr);
  const hours = Number(fields.hours);
  const seen = new Set<string>();
  const series: Series[] = (Array.isArray(fields.series) ? fields.series : [])
    .filter((s: any) => s && typeof s === "object")
    .map((s: any) => {
      // A row without an ID, or repeating one, still needs a key of its own;
      // the panel writes unique IDs, so this only covers hand-edited configs.
      let id = String(s.id ?? "");
      if (!id || seen.has(id)) id = newSeriesId();
      seen.add(id);
      const source: SeriesSource = s.source === "control" || s.source === "mqtt" ? s.source : "ha";
      return { ...s, id, source, displayOnly: true } as Series;
    });
  const style = fields.style === "area" || fields.style === "step" ? fields.style : "line";
  return {
    series,
    hours: Number.isFinite(hours) ? Math.min(Math.max(Math.round(hours), MIN_HOURS), MAX_HOURS) : 24,
    style,
    min: optionalNumber(fields.min),
    max: optionalNumber(fields.max),
    showLegend: fields.showLegend !== false,
    showCurrent: fields.showCurrent !== false,
  };
}

// THEME_COLORS are the defaults series take in order, as theme variables with
// the default theme's values behind them.
const THEME_COLORS = [
  "var(--theme-blue, #89b4fa)",
  "var(--theme-green, #a6e3a1)",
  "var(--theme-peach, #fab387)",
  "var(--theme-mauve, #cba6f7)",
  "var(--theme-red, #f38ba8)",
];

// THEME_HEX are the same defaults as literals, for the colour input, which
// cannot show a variable.
export const THEME_HEX = ["#89b4fa", "#a6e3a1", "#fab387", "#cba6f7", "#f38ba8"];

/** seriesColor is the colour a series draws with. */
export function seriesColor(series: Series, index: number): string {
  return series.color?.trim() || THEME_COLORS[index % THEME_COLORS.length];
}

/**
 * numeric reads a state as a number, or null when it is not one. A binary
 * sensor's on and off graph as 1 and 0; anything else that is not a number,
 * including "unavailable", is skipped rather than drawn as zero.
 */
export function numeric(value: unknown): number | null {
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  if (typeof value === "boolean") return value ? 1 : 0;
  if (typeof value !== "string") return null;
  const text = value.trim().toLowerCase();
  if (text === "on" || text === "true") return 1;
  if (text === "off" || text === "false") return 0;
  if (text === "") return null;
  const n = Number(text);
  return Number.isFinite(n) ? n : null;
}

/** formatValue renders a reading with at most two decimals. */
export function formatValue(value: number, unit: string): string {
  const text = Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)));
  return unit ? `${text} ${unit}` : text;
}
