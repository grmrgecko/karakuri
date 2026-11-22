// Colour resolution: named colours, thresholds, palettes, and
// continuous gradients, drawn from the theme's palette.

import type { Field, FieldConfig, Palette, ThresholdStep, Thresholds } from "./types";
import { reduce, toNumber } from "./reducers";

/** FALLBACK_PALETTE is Catppuccin Mocha, used when the theme names no colour. */
export const FALLBACK_PALETTE: Palette = {
  series: ["#89b4fa", "#a6e3a1", "#fab387", "#cba6f7", "#f38ba8", "#f9e2af", "#94e2d5", "#89dceb", "#b4befe", "#f5c2e7"],
  named: {
    blue: "#89b4fa",
    green: "#a6e3a1",
    orange: "#fab387",
    purple: "#cba6f7",
    red: "#f38ba8",
    yellow: "#f9e2af",
    teal: "#94e2d5",
    sky: "#89dceb",
    lavender: "#b4befe",
    pink: "#f5c2e7",
    text: "#cdd6f4",
  },
  text: "#cdd6f4",
  textMuted: "#a6adc8",
  border: "#45475a",
  background: "#1e1e2e",
  surface: "#313244",
  font: "Inter, sans-serif",
};

// SAFE_COLOR admits hex, rgb(a), hsl(a), and bare names, so a colour from a
// config can never smuggle markup or CSS into a style attribute.
const SAFE_COLOR = /^(#[0-9a-fA-F]{3,8}|(rgb|hsl)a?\([0-9.,%\s/-]+\)|[a-zA-Z]+)$/;

/** safeColor returns c if it is a plain colour value, otherwise the fallback. */
export function safeColor(c: string | undefined, fallback: string): string {
  return c && SAFE_COLOR.test(c.trim()) ? c.trim() : fallback;
}

/**
 * resolveColor turns a colour name such as green, dark-red, or
 * semi-dark-orange into the theme's colour. Hex and CSS colours pass through.
 */
export function resolveColor(name: string | undefined, palette: Palette): string {
  if (!name) return palette.series[0];
  const n = name.trim();
  if (n === "transparent") return "transparent";
  const base = n.replace(/^(super-light|light|semi-dark|dark)-/, "");
  const hit = palette.named[base];
  if (hit) {
    if (base !== n) {
      const shade = n.slice(0, n.length - base.length - 1);
      return shadeColor(hit, shade);
    }
    return hit;
  }
  return safeColor(n, palette.series[0]);
}

// shadeColor lightens or darkens a hex colour for a shade prefix such as dark- or semi-dark-.
function shadeColor(hex: string, shade: string): string {
  const rgb = parseHex(hex);
  if (!rgb) return hex;
  const amount = { "super-light": 0.45, light: 0.25, "semi-dark": -0.15, dark: -0.3 }[shade] ?? 0;
  const mix = (c: number) => Math.round(amount >= 0 ? c + (255 - c) * amount : c * (1 + amount));
  return toHex(rgb.map(mix) as [number, number, number]);
}

/** parseHex reads #rgb or #rrggbb. */
export function parseHex(hex: string): [number, number, number] | null {
  const m = /^#([0-9a-f]{3}|[0-9a-f]{6})([0-9a-f]{2})?$/i.exec(hex.trim());
  if (!m) return null;
  let h = m[1];
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}

function toHex(rgb: [number, number, number]): string {
  return "#" + rgb.map((c) => Math.max(0, Math.min(255, c)).toString(16).padStart(2, "0")).join("");
}

/** withAlpha returns a colour at an opacity from 0 to 1; non-hex colours are returned unchanged. */
export function withAlpha(color: string, alpha: number): string {
  const rgb = parseHex(color);
  if (!rgb) return color;
  return `rgba(${rgb[0]}, ${rgb[1]}, ${rgb[2]}, ${Math.max(0, Math.min(1, alpha))})`;
}

/** mixColors interpolates between two hex colours. */
export function mixColors(a: string, b: string, t: number): string {
  const x = parseHex(a);
  const y = parseHex(b);
  if (!x || !y) return t < 0.5 ? a : b;
  return toHex([0, 1, 2].map((i) => Math.round(x[i] + (y[i] - x[i]) * t)) as [number, number, number]);
}

/** GRADIENTS names the continuous colour schemes by the theme colours they pass through. */
export const GRADIENTS: Record<string, { name: string; stops: string[] }> = {
  "continuous-GrYlRd": { name: "Green-Yellow-Red", stops: ["green", "yellow", "red"] },
  "continuous-RdYlGr": { name: "Red-Yellow-Green", stops: ["red", "yellow", "green"] },
  "continuous-BlYlRd": { name: "Blue-Yellow-Red", stops: ["blue", "yellow", "red"] },
  "continuous-YlRd": { name: "Yellow-Red", stops: ["yellow", "red"] },
  "continuous-BlPu": { name: "Blue-Purple", stops: ["blue", "purple"] },
  "continuous-YlBl": { name: "Yellow-Blue", stops: ["yellow", "blue"] },
  "continuous-blues": { name: "Blues", stops: ["super-light-blue", "dark-blue"] },
  "continuous-reds": { name: "Reds", stops: ["super-light-red", "dark-red"] },
  "continuous-greens": { name: "Greens", stops: ["super-light-green", "dark-green"] },
  "continuous-purples": { name: "Purples", stops: ["super-light-purple", "dark-purple"] },
};

/** gradientStops resolves a gradient's colours against the palette. */
export function gradientStops(mode: string, palette: Palette): string[] {
  const g = GRADIENTS[mode] ?? GRADIENTS["continuous-GrYlRd"];
  return g.stops.map((s) => resolveColor(s, palette));
}

/** gradientAt picks the colour at t (0 to 1) along a list of stops. */
export function gradientAt(stops: string[], t: number): string {
  if (stops.length === 0) return "#888888";
  if (stops.length === 1 || !Number.isFinite(t)) return stops[0];
  const x = Math.max(0, Math.min(1, t)) * (stops.length - 1);
  const i = Math.min(stops.length - 2, Math.floor(x));
  return mixColors(stops[i], stops[i + 1], x - i);
}

/** sortedSteps returns threshold steps with the base first and the rest ascending. */
export function sortedSteps(thresholds: Thresholds | undefined): ThresholdStep[] {
  const steps = thresholds?.steps ?? [];
  const base = steps.find((s) => s.value === null || s.value === undefined);
  const rest = steps
    .filter((s) => s.value !== null && s.value !== undefined && Number.isFinite(Number(s.value)))
    .map((s) => ({ value: Number(s.value), color: s.color }))
    .sort((a, b) => (a.value as number) - (b.value as number));
  return [{ value: null, color: base?.color ?? "green" }, ...rest];
}

/**
 * activeThreshold returns the step a value falls in. In percentage mode a
 * step's value is a percentage of the min to max range.
 */
export function activeThreshold(value: number, thresholds: Thresholds | undefined, min = 0, max = 100): ThresholdStep {
  const steps = sortedSteps(thresholds);
  let v = value;
  if (thresholds?.mode === "percentage") {
    const span = max - min;
    v = span === 0 ? 0 : ((value - min) / span) * 100;
  }
  let active = steps[0];
  for (const step of steps) {
    if (step.value === null || v >= step.value) active = step;
  }
  return active;
}

/** thresholdValue turns a step into the field's units, applying percentage mode. */
export function thresholdValue(step: ThresholdStep, thresholds: Thresholds | undefined, min: number, max: number): number | null {
  if (step.value === null) return null;
  if (thresholds?.mode === "percentage") return min + ((max - min) * step.value) / 100;
  return step.value;
}

/**
 * fieldMinMax returns the range a field's colours and gauges use: the
 * configured min and max, else the data's. A gauge's arc or bar starts at
 * zero for data that never goes below it, so zeroBased asks for that.
 */
export function fieldMinMax(field: Field, zeroBased = false): { min: number; max: number } {
  const c = field.config;
  const dataMin = field.state.range?.min ?? 0;
  const min = c.min ?? (zeroBased ? Math.min(0, dataMin) : dataMin);
  let max = c.max ?? field.state.range?.max ?? 100;
  if (max <= min) max = min + 1;
  return { min, max };
}

/** seriesColor returns the colour a whole series draws with. */
export function seriesColor(field: Field, palette: Palette): string {
  const config: FieldConfig = field.config;
  const mode = config.color?.mode ?? "palette-classic";
  const index = field.state.seriesIndex ?? 0;
  if (mode === "fixed" || mode === "shades") return resolveColor(config.color?.fixedColor, palette);
  if (mode === "thresholds" || mode.startsWith("continuous-")) {
    const by = config.color?.seriesBy ?? "last";
    const v = toNumber(reduce(field.values, by === "last" ? "lastNotNull" : by));
    if (v === null) {
      return mode === "thresholds" ? resolveColor(sortedSteps(config.thresholds)[0].color, palette) : gradientStops(mode, palette)[0];
    }
    return valueColor(field, v, palette);
  }
  return palette.series[index % palette.series.length];
}

/** valueColor returns the colour of one value under the field's colour mode. */
export function valueColor(field: Field, value: number | null, palette: Palette): string {
  const config = field.config;
  const mode = config.color?.mode ?? "palette-classic";
  if (value === null || !Number.isFinite(value)) return seriesFallback(field, palette);
  const { min, max } = fieldMinMax(field);
  if (mode === "thresholds") return resolveColor(activeThreshold(value, config.thresholds, min, max).color, palette);
  if (mode.startsWith("continuous-")) return gradientAt(gradientStops(mode, palette), (value - min) / (max - min));
  if (mode === "fixed" || mode === "shades") return resolveColor(config.color?.fixedColor, palette);
  return palette.series[(field.state.seriesIndex ?? 0) % palette.series.length];
}

function seriesFallback(field: Field, palette: Palette): string {
  const mode = field.config.color?.mode ?? "palette-classic";
  if (mode === "fixed" || mode === "shades") return resolveColor(field.config.color?.fixedColor, palette);
  if (mode === "thresholds") return resolveColor(sortedSteps(field.config.thresholds)[0].color, palette);
  return palette.series[(field.state.seriesIndex ?? 0) % palette.series.length];
}
