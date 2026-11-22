// Frames are the series the server returns, held as columns the
// transformations and visualizations read.

import type { ChartResponse, Field, FieldType, Frame, ServerSeries, TimeRange } from "./types";
import { unitFromSource } from "./units";

/** QueryFailure is a query that returned an error instead of series. */
export interface QueryFailure {
  refId: string;
  message: string;
}

function fieldType(t: string): FieldType {
  return t === "time" || t === "number" || t === "string" || t === "boolean" ? t : "other";
}

/** frameFromSeries turns one server series into a frame. */
export function frameFromSeries(s: ServerSeries, refId: string): Frame {
  const fields = Array.isArray(s.fields) ? s.fields : [];
  const valueFields = fields.filter((f) => f.type !== "time").length;
  const frame: Frame = { name: s.name || undefined, refId: s.ref_id || refId, fields: [] };
  for (const f of fields) {
    const type = fieldType(f.type);
    const labels = type === "time" ? undefined : (f.labels ?? s.labels);
    const unit = unitFromSource(f.unit ?? (type === "number" ? s.unit : undefined));
    frame.fields.push({
      name: f.name,
      type,
      values: Array.isArray(f.values) ? f.values : [],
      labels: labels && Object.keys(labels).length ? { ...labels } : undefined,
      config: unit ? { unit } : {},
      state: {
        refId: frame.refId,
        seriesName: s.name,
        // A series with one value column is known by the series' name, which
        // already carries the query's legend format.
        displayName: type !== "time" && valueFields === 1 && s.name ? s.name : undefined,
      },
    });
  }
  return frame;
}

/** framesFromResponse reads the data route's answer into frames and failures. */
export function framesFromResponse(res: ChartResponse | null | undefined): { frames: Frame[]; failures: QueryFailure[] } {
  const frames: Frame[] = [];
  const failures: QueryFailure[] = [];
  for (const r of res?.results ?? []) {
    if (r.error) {
      failures.push({ refId: r.ref_id, message: r.error.message || r.error.class || "query failed" });
      continue;
    }
    for (const s of r.result?.series ?? []) frames.push(frameFromSeries(s, r.ref_id));
  }
  return { frames, failures };
}

/** responseRange reads the range the server used, if it reported one. */
export function responseRange(res: ChartResponse | null | undefined): TimeRange | null {
  const from = toMillis(res?.range?.from);
  const to = toMillis(res?.range?.to);
  return from !== null && to !== null && from < to ? { from, to } : null;
}

/** toMillis reads a time as Unix milliseconds from a number, digits, or a date string. */
export function toMillis(v: unknown): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  if (typeof v === "string" && v.trim() !== "") {
    if (/^-?\d+$/.test(v.trim())) return Number(v);
    const t = Date.parse(v);
    return Number.isNaN(t) ? null : t;
  }
  if (v instanceof Date) return v.getTime();
  return null;
}

/** frameLength returns a frame's row count. */
export function frameLength(frame: Frame): number {
  return frame.fields.reduce((n, f) => Math.max(n, f.values.length), 0);
}

/** labelText writes labels the way Prometheus does: {a="1", b="2"}. */
export function labelText(labels: Record<string, string> | undefined): string {
  if (!labels) return "";
  const keys = Object.keys(labels).sort();
  if (!keys.length) return "";
  return "{" + keys.map((k) => `${k}="${labels[k]}"`).join(", ") + "}";
}

const TEMPLATE = /\$\{__(field|series)\.(name|displayName|refId|labels(?:\.([A-Za-z0-9_.-]+))?)\}/g;

function interpolate(text: string, field: Field, frame: Frame | undefined): string {
  return text.replace(TEMPLATE, (_m, scope: string, key: string, label: string | undefined) => {
    if (scope === "series") return key === "refId" ? (field.state.refId ?? "") : (frame?.name ?? field.state.seriesName ?? "");
    if (key === "name") return field.name;
    if (key === "displayName") return field.state.displayName ?? field.name;
    if (key === "refId") return field.state.refId ?? "";
    if (label !== undefined) return field.labels?.[label] ?? "";
    return labelText(field.labels);
  });
}

/**
 * displayName returns the name a field is drawn under: the configured display
 * name with ${__field.labels.x} templates filled, else the series' name, else
 * the field's own name with its labels.
 */
export function displayName(field: Field, frame?: Frame): string {
  if (field.config.displayName) return interpolate(field.config.displayName, field, frame);
  if (field.state.displayName) return field.state.displayName;
  if (field.type === "time") return field.name === "time" ? "Time" : field.name;
  if (field.labels && Object.keys(field.labels).length) {
    const keys = Object.keys(field.labels);
    if (keys.length === 1) return field.labels[keys[0]];
    return field.name === "value" ? labelText(field.labels) : `${field.name} ${labelText(field.labels)}`;
  }
  if (frame?.name && field.name === "value") return frame.name;
  return field.name;
}

/** findField finds a field by display name or name across frames. */
export function findField(frames: Frame[], name: string): { frame: Frame; field: Field } | null {
  for (const frame of frames) {
    for (const field of frame.fields) if (displayName(field, frame) === name) return { frame, field };
  }
  for (const frame of frames) {
    for (const field of frame.fields) if (field.name === name) return { frame, field };
  }
  return null;
}

/** timeField returns a frame's first time field. */
export function timeField(frame: Frame): Field | undefined {
  return frame.fields.find((f) => f.type === "time");
}

/** numberFields returns a frame's number fields. */
export function numberFields(frame: Frame): Field[] {
  return frame.fields.filter((f) => f.type === "number");
}

/** cloneField copies a field with its own values array. */
export function cloneField(field: Field, values: unknown[] = [...field.values]): Field {
  return {
    ...field,
    values,
    labels: field.labels ? { ...field.labels } : undefined,
    config: { ...field.config },
    state: { ...field.state },
  };
}

/** fieldNames lists every distinct display name across frames, for editor pickers. */
export function fieldNames(frames: Frame[]): string[] {
  const out = new Set<string>();
  for (const frame of frames) for (const field of frame.fields) out.add(displayName(field, frame));
  return [...out];
}
