// Display values: one cell turned into the text, unit parts, and colour a
// visualization draws, and fields reduced to the values stat, gauge, bar
// gauge, and pie chart show.

import type { Field, Frame, Palette } from "./types";
import { displayName, timeField } from "./frames";
import { mapValue, parseRegex } from "./mappings";
import { reduce, toNumber } from "./reducers";
import { formatValue, isoDateTime } from "./units";
import { resolveColor, valueColor } from "./colors";
import { hiddenFromViz } from "./fieldConfig";

export interface DisplayValue {
  text: string;
  prefix?: string;
  suffix?: string;
  color: string;
  /** numeric is the value as a number, or NaN when it is not one. */
  numeric: number;
}

/** displayText joins a display value's parts. */
export function displayText(d: DisplayValue): string {
  return `${d.prefix ?? ""}${d.text}${d.suffix ?? ""}`;
}

/** display formats one value of a field: mapping first, then unit and decimals. */
export function display(field: Field, value: unknown, palette: Palette): DisplayValue {
  const n = toNumber(value);
  const numeric = n ?? NaN;
  const mapped = mapValue(value, field.config.mappings);
  const color = mapped?.color ? resolveColor(mapped.color, palette) : valueColor(field, n, palette);
  if (mapped?.text !== undefined && mapped.text !== "") return { text: mapped.text, color, numeric };
  if (value === null || value === undefined || (typeof value === "number" && Number.isNaN(value))) {
    return { text: field.config.noValue ?? "-", color, numeric };
  }
  if (field.type === "time") {
    const t = toNumber(value);
    return { text: t === null ? String(value) : isoDateTime(t), color, numeric };
  }
  if (field.type === "number" && n !== null) {
    const f = formatValue(n, field.config.unit, field.config.decimals);
    return { text: f.text, prefix: f.prefix, suffix: f.suffix, color, numeric };
  }
  if (typeof value === "object") return { text: JSON.stringify(value), color, numeric };
  return { text: String(value), color, numeric };
}

/** formatNumber formats a bare number in a field's unit, for axes and legends. */
export function formatNumber(field: Field | undefined, value: unknown): string {
  const n = toNumber(value);
  if (n === null) return field?.config.noValue ?? "-";
  const f = formatValue(n, field?.config.unit, field?.config.decimals);
  return `${f.prefix ?? ""}${f.text}${f.suffix ?? ""}`;
}

/** ReduceOptions are the value options for single-value visualizations. */
export interface ReduceOptions {
  /** values shows every row instead of a calculation. */
  values?: boolean;
  calcs?: string[];
  /** fields selects fields: "" for numeric fields, a name, or a /regex/. */
  fields?: string;
  limit?: number;
}

/** FieldDisplay is one reduced value ready for a single-value visualization. */
export interface FieldDisplay {
  name: string;
  field: Field;
  frame: Frame;
  display: DisplayValue;
  /** sparkline holds the field's points over time when it has a time field. */
  sparkline?: { times: number[]; values: (number | null)[] };
  /** percentChange is the change from the first to the last value. */
  percentChange?: number;
}

function selectField(field: Field, frame: Frame, selector: string | undefined): boolean {
  if (field.type === "time" || hiddenFromViz(field)) return false;
  if (!selector || selector === "Numeric Fields") return field.type === "number";
  if (selector === "All Fields") return true;
  const re = selector.startsWith("/") ? parseRegex(selector) : null;
  const name = displayName(field, frame);
  return re ? re.test(name) : name === selector || field.name === selector;
}

/**
 * fieldDisplays reduces the selected fields with the first calculation, or
 * lists each row when values is set, up to the limit.
 */
export function fieldDisplays(frames: Frame[], options: ReduceOptions | undefined, palette: Palette): FieldDisplay[] {
  const calc = options?.calcs?.[0] ?? "lastNotNull";
  const limit = options?.limit && options.limit > 0 ? options.limit : 5000;
  const out: FieldDisplay[] = [];
  for (const frame of frames) {
    const time = timeField(frame);
    for (const field of frame.fields) {
      if (!selectField(field, frame, options?.fields)) continue;
      const name = displayName(field, frame);
      if (options?.values) {
        for (let r = 0; r < field.values.length && out.length < limit; r++) {
          const rowName = time ? isoDateTime(Number(time.values[r])) : name;
          out.push({ name: field.config.displayName ? name : rowName, field, frame, display: display(field, field.values[r], palette) });
        }
        continue;
      }
      if (out.length >= limit) break;
      const value = reduce(field.values, calc);
      const fd: FieldDisplay = { name, field, frame, display: display(field, value, palette) };
      if (time && field.type === "number") {
        fd.sparkline = {
          times: time.values.map((t) => Number(t)),
          values: field.values.map((v) => toNumber(v)),
        };
        const first = toNumber(reduce(field.values, "firstNotNull"));
        const last = toNumber(reduce(field.values, "lastNotNull"));
        if (first !== null && last !== null && first !== 0) fd.percentChange = ((last - first) / Math.abs(first)) * 100;
      }
      out.push(fd);
    }
  }
  return out;
}
