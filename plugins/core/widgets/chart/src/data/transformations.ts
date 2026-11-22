// Transformations, applied in order to the query results before field
// options and drawing. A transformation that names a field the data lacks
// leaves the data as it was.

import type { Field, FieldType, Frame, Transformation } from "./types";
import { cloneField, displayName, frameLength, timeField } from "./frames";
import { parseRegex } from "./mappings";
import { reduce, reducerName, toNumber } from "./reducers";

/** TransformationInfo describes one transformation for the editor. */
export interface TransformationInfo {
  id: string;
  name: string;
  description: string;
}

export const TRANSFORMATIONS: TransformationInfo[] = [
  { id: "organize", name: "Organize fields", description: "Reorder, hide, or rename fields" },
  { id: "renameByRegex", name: "Rename by regex", description: "Rename series with a regular expression" },
  { id: "filterFieldsByName", name: "Filter fields by name", description: "Keep or drop fields by name or pattern" },
  { id: "filterByValue", name: "Filter data by values", description: "Keep or drop rows whose values match" },
  { id: "reduce", name: "Reduce", description: "Reduce each field to calculated values" },
  { id: "calculateField", name: "Add field from calculation", description: "Binary operation, row reduction, or unary function" },
  { id: "joinByField", name: "Join by field", description: "Join frames on a shared field, time by default" },
  { id: "merge", name: "Merge", description: "Merge frames into one table" },
  { id: "seriesToRows", name: "Series to rows", description: "One row per time and series" },
  { id: "labelsToFields", name: "Labels to fields", description: "Turn labels into columns or rows" },
  { id: "groupBy", name: "Group by", description: "Group rows and aggregate the rest" },
  { id: "sortBy", name: "Sort by", description: "Sort rows by a field" },
  { id: "limit", name: "Limit", description: "Keep the first rows" },
  { id: "convertFieldType", name: "Convert field type", description: "Read a field as number, string, time, or boolean" },
];

/** applyTransformations runs the enabled transformations in order. */
export function applyTransformations(frames: Frame[], list: Transformation[] | undefined): Frame[] {
  let out = frames;
  for (const t of list ?? []) {
    if (!t || t.disabled) continue;
    const run = RUNNERS[t.id];
    if (!run) continue;
    out = run(out, t.options ?? {});
  }
  return out;
}

type Runner = (frames: Frame[], options: any) => Frame[];

const RUNNERS: Record<string, Runner> = {
  organize,
  renameByRegex,
  filterFieldsByName,
  filterByValue,
  reduce: reduceTransform,
  calculateField,
  joinByField,
  merge,
  seriesToRows,
  labelsToFields,
  groupBy,
  sortBy,
  limit,
  convertFieldType,
};

function names(field: Field, frame: Frame): string[] {
  return [displayName(field, frame), field.name];
}

function matchesName(field: Field, frame: Frame, name: string): boolean {
  return names(field, frame).includes(name);
}

function withRows(frame: Frame, rows: number[]): Frame {
  return { ...frame, fields: frame.fields.map((f) => cloneField(f, rows.map((r) => f.values[r]))) };
}

// ---- Organize, rename, filter ----

function organize(frames: Frame[], o: any): Frame[] {
  const exclude: Record<string, boolean> = o.excludeByName ?? {};
  const index: Record<string, number> = o.indexByName ?? {};
  const rename: Record<string, string> = o.renameByName ?? {};
  return frames.map((frame) => {
    const kept = frame.fields
      .map((field, i) => ({ field, i, name: displayName(field, frame) }))
      .filter((x) => !exclude[x.name]);
    kept.sort((a, b) => (index[a.name] ?? 1000 + a.i) - (index[b.name] ?? 1000 + b.i));
    return {
      ...frame,
      fields: kept.map(({ field, name }) => {
        const f = cloneField(field, field.values);
        if (rename[name]) f.config.displayName = rename[name];
        return f;
      }),
    };
  });
}

function renameByRegex(frames: Frame[], o: any): Frame[] {
  const re = parseRegex(o.regex ?? "");
  if (!re || !o.regex) return frames;
  const pattern = String(o.renamePattern ?? "");
  return frames.map((frame) => ({
    ...frame,
    fields: frame.fields.map((field) => {
      const name = displayName(field, frame);
      re.lastIndex = 0;
      if (!re.test(name)) return field;
      re.lastIndex = 0;
      const f = cloneField(field, field.values);
      f.config.displayName = name.replace(re, pattern);
      return f;
    }),
  }));
}

function nameMatcher(spec: any): ((field: Field, frame: Frame) => boolean) | null {
  if (!spec) return null;
  const list: string[] = Array.isArray(spec.names) ? spec.names : [];
  const re = spec.pattern ? parseRegex(spec.pattern) : null;
  if (!list.length && !re) return null;
  return (field, frame) => names(field, frame).some((n) => list.includes(n) || (re ? re.test(n) : false));
}

function filterFieldsByName(frames: Frame[], o: any): Frame[] {
  const include = nameMatcher(o.include);
  const exclude = nameMatcher(o.exclude);
  return frames
    .map((frame) => ({
      ...frame,
      fields: frame.fields.filter((f) => (!include || include(f, frame)) && !(exclude && exclude(f, frame))),
    }))
    .filter((frame) => frame.fields.length > 0);
}

function valueMatches(value: unknown, config: any): boolean {
  const id = config?.id;
  const opts = config?.options ?? {};
  const n = toNumber(value);
  const target = toNumber(opts.value);
  switch (id) {
    case "isNull":
      return value === null || value === undefined;
    case "isNotNull":
      return value !== null && value !== undefined;
    case "greater":
      return n !== null && target !== null && n > target;
    case "greaterOrEqual":
      return n !== null && target !== null && n >= target;
    case "lower":
      return n !== null && target !== null && n < target;
    case "lowerOrEqual":
      return n !== null && target !== null && n <= target;
    case "equal":
      return n !== null && target !== null ? n === target : String(value) === String(opts.value);
    case "notEqual":
      return n !== null && target !== null ? n !== target : String(value) !== String(opts.value);
    case "range": {
      const from = toNumber(opts.from);
      const to = toNumber(opts.to);
      return n !== null && (from === null || n >= from) && (to === null || n <= to);
    }
    case "regex": {
      const re = parseRegex(String(opts.value ?? ""));
      return re !== null && value !== null && value !== undefined && re.test(String(value));
    }
  }
  return false;
}

function filterByValue(frames: Frame[], o: any): Frame[] {
  const filters: any[] = Array.isArray(o.filters) ? o.filters : [];
  if (!filters.length) return frames;
  const include = o.type !== "exclude";
  const all = o.match === "all";
  return frames.map((frame) => {
    const checks = filters
      .map((f) => ({ field: frame.fields.find((x) => matchesName(x, frame, f.fieldName)), config: f.config }))
      .filter((c) => c.field);
    if (!checks.length) return frame;
    const rows: number[] = [];
    const n = frameLength(frame);
    for (let r = 0; r < n; r++) {
      const results = checks.map((c) => valueMatches(c.field!.values[r], c.config));
      const hit = all ? results.every(Boolean) : results.some(Boolean);
      if (hit === include) rows.push(r);
    }
    return withRows(frame, rows);
  });
}

// ---- Reduce and calculations ----

function reduceTransform(frames: Frame[], o: any): Frame[] {
  const reducers: string[] = Array.isArray(o.reducers) && o.reducers.length ? o.reducers : ["lastNotNull"];
  if (o.mode === "reduceFields") {
    return frames.map((frame) => ({
      ...frame,
      fields: frame.fields.flatMap((field) => {
        if (field.type === "time" && !o.includeTimeField) return [];
        if (field.type !== "number" && field.type !== "time") return [cloneField(field, [field.values[field.values.length - 1]])];
        return reducers.map((id) => {
          const f = cloneField(field, [reduce(field.values, id)]);
          if (reducers.length > 1) f.state.displayName = `${displayName(field, frame)} ${reducerName(id)}`;
          return f;
        });
      }),
    }));
  }
  // seriesToRows, the default: one row per number field.
  const fieldCol: unknown[] = [];
  const labelCols = new Map<string, unknown[]>();
  const calcCols: unknown[][] = reducers.map(() => []);
  let row = 0;
  for (const frame of frames) {
    for (const field of frame.fields) {
      if (field.type !== "number") continue;
      fieldCol.push(displayName(field, frame));
      if (o.labelsToFields && field.labels) {
        for (const [k, v] of Object.entries(field.labels)) {
          if (!labelCols.has(k)) labelCols.set(k, new Array(row).fill(null));
          labelCols.get(k)!.push(v);
        }
      }
      for (const col of labelCols.values()) if (col.length <= row) col.push(null);
      reducers.forEach((id, i) => calcCols[i].push(reduce(field.values, id)));
      row++;
    }
  }
  if (!row) return frames;
  const fields: Field[] = [field("Field", "string", fieldCol)];
  for (const [k, col] of labelCols) fields.push(field(k, "string", col));
  reducers.forEach((id, i) => fields.push(field(reducerName(id), "number", calcCols[i])));
  return [{ fields }];
}

function field(name: string, type: FieldType, values: unknown[]): Field {
  return { name, type, values, config: {}, state: {} };
}

const BINARY: Record<string, (a: number, b: number) => number> = {
  "+": (a, b) => a + b,
  "-": (a, b) => a - b,
  "*": (a, b) => a * b,
  "/": (a, b) => a / b,
  "%": (a, b) => a % b,
  "^": (a, b) => Math.pow(a, b),
};

const UNARY: Record<string, (a: number) => number> = {
  abs: Math.abs,
  exp: Math.exp,
  ln: Math.log,
  floor: Math.floor,
  ceil: Math.ceil,
};

// operand reads a binary operand: a field of the frame or a number literal.
function operand(frame: Frame, name: unknown): ((row: number) => number | null) | null {
  if (typeof name === "number") return () => name;
  if (typeof name !== "string" || name === "") return null;
  const f = frame.fields.find((x) => matchesName(x, frame, name));
  if (f) return (r) => toNumber(f.values[r]);
  const n = Number(name);
  return Number.isFinite(n) ? () => n : null;
}

function calculateField(frames: Frame[], o: any): Frame[] {
  const mode = o.mode ?? "reduceRow";
  return frames.map((frame) => {
    const n = frameLength(frame);
    const values: (number | null)[] = [];
    let alias = o.alias as string | undefined;

    if (mode === "binary") {
      const b = o.binary ?? {};
      const left = operand(frame, b.left);
      const right = operand(frame, b.right);
      const op = BINARY[b.operator ?? "+"];
      if (!left || !right || !op) return frame;
      for (let r = 0; r < n; r++) {
        const x = left(r);
        const y = right(r);
        const v = x === null || y === null ? null : op(x, y);
        values.push(v === null || Number.isFinite(v) ? v : null);
      }
      alias ||= `${b.left} ${b.operator ?? "+"} ${b.right}`;
    } else if (mode === "unary") {
      const u = o.unary ?? {};
      const src = frame.fields.find((x) => matchesName(x, frame, u.fieldName));
      const op = UNARY[u.operator ?? "abs"];
      if (!src || !op) return frame;
      for (let r = 0; r < n; r++) {
        const x = toNumber(src.values[r]);
        const v = x === null ? null : op(x);
        values.push(Number.isFinite(v) ? v : null);
      }
      alias ||= `${u.operator ?? "abs"}(${u.fieldName})`;
    } else {
      const rr = o.reduce ?? {};
      const include: string[] = Array.isArray(rr.include) ? rr.include : [];
      const reducer = rr.reducer ?? "sum";
      const sources = frame.fields.filter(
        (f) => f.type === "number" && (!include.length || include.some((name) => matchesName(f, frame, name))),
      );
      if (!sources.length) return frame;
      for (let r = 0; r < n; r++) values.push(toNumber(reduce(sources.map((f) => f.values[r]), reducer)));
      alias ||= reducerName(reducer);
    }

    const added = field(alias, "number", values);
    if (o.replaceFields) {
      const time = timeField(frame);
      return { ...frame, fields: time ? [time, added] : [added] };
    }
    return { ...frame, fields: [...frame.fields, added] };
  });
}

// ---- Join and merge ----

function joinByField(frames: Frame[], o: any): Frame[] {
  if (frames.length < 2) return frames;
  const by: string | undefined = o.byField;
  const keyOf = (frame: Frame): Field | undefined =>
    by ? frame.fields.find((f) => matchesName(f, frame, by)) : timeField(frame);
  const keyed = frames.map((frame) => ({ frame, key: keyOf(frame) })).filter((x) => x.key);
  if (keyed.length < 2) return frames;

  const inner = o.mode === "inner";
  const index = keyed.map(({ key }) => {
    const m = new Map<string, number>();
    key!.values.forEach((v, r) => {
      if (v !== null && v !== undefined && !m.has(String(v))) m.set(String(v), r);
    });
    return m;
  });
  const keys = new Map<string, unknown>();
  for (const { key } of keyed) for (const v of key!.values) if (v !== null && v !== undefined) keys.set(String(v), v);
  let order = [...keys.keys()].filter((k) => !inner || index.every((m) => m.has(k)));
  const numeric = keyed[0].key!.type === "time" || keyed[0].key!.type === "number";
  if (numeric) order.sort((a, b) => Number(keys.get(a)) - Number(keys.get(b)));

  const joinField = cloneField(keyed[0].key!, order.map((k) => keys.get(k)));
  const fields: Field[] = [joinField];
  keyed.forEach(({ frame, key }, i) => {
    for (const f of frame.fields) {
      if (f === key) continue;
      const out = cloneField(f, order.map((k) => {
        const r = index[i].get(k);
        return r === undefined ? null : f.values[r];
      }));
      // The joined frame has no name of its own, so each field keeps the
      // name its series gave it.
      out.state.displayName = displayName(f, frame);
      fields.push(out);
    }
  });
  const rest = frames.filter((frame) => !keyed.some((k) => k.frame === frame));
  return [{ fields }, ...rest];
}

function merge(frames: Frame[]): Frame[] {
  if (frames.length < 2) return frames;
  const columns: { key: string; template: Field; values: unknown[] }[] = [];
  let rows = 0;
  for (const frame of frames) {
    const n = frameLength(frame);
    for (const f of frame.fields) {
      const key = `${displayName(f, frame)}\u0000${f.type}`;
      let col = columns.find((c) => c.key === key);
      if (!col) {
        col = { key, template: f, values: new Array(rows).fill(null) };
        columns.push(col);
      }
    }
    for (const col of columns) {
      const f = frame.fields.find((x) => `${displayName(x, frame)}\u0000${x.type}` === col.key);
      for (let r = 0; r < n; r++) col.values.push(f ? (f.values[r] ?? null) : null);
    }
    rows += n;
  }
  const fields = columns.map((c) => {
    const f = cloneField(c.template, c.values);
    f.state.displayName = c.key.split("\u0000")[0];
    f.labels = undefined;
    return f;
  });
  const merged: Frame = { fields };
  const time = timeField(merged);
  if (time) {
    const order = time.values.map((_, r) => r).sort((a, b) => Number(time.values[a]) - Number(time.values[b]));
    return [withRows(merged, order)];
  }
  return [merged];
}

function seriesToRows(frames: Frame[]): Frame[] {
  const times: unknown[] = [];
  const metrics: unknown[] = [];
  const values: unknown[] = [];
  for (const frame of frames) {
    const time = timeField(frame);
    if (!time) continue;
    for (const f of frame.fields) {
      if (f.type !== "number") continue;
      const name = displayName(f, frame);
      f.values.forEach((v, r) => {
        times.push(time.values[r]);
        metrics.push(name);
        values.push(v);
      });
    }
  }
  if (!times.length) return frames;
  const order = times.map((_, r) => r).sort((a, b) => Number(times[b]) - Number(times[a]));
  return [
    {
      fields: [
        field("Time", "time", order.map((r) => times[r])),
        field("Metric", "string", order.map((r) => metrics[r])),
        field("Value", "number", order.map((r) => values[r])),
      ],
    },
  ];
}

function labelsToFields(frames: Frame[], o: any): Frame[] {
  const keep: string[] | undefined = Array.isArray(o.keepLabels) && o.keepLabels.length ? o.keepLabels : undefined;
  if (o.mode === "rows") {
    return frames.map((frame) => {
      const labels: Record<string, string> = {};
      for (const f of frame.fields) Object.assign(labels, f.labels ?? {});
      const keys = Object.keys(labels).filter((k) => !keep || keep.includes(k));
      return { ...frame, fields: [field("label", "string", keys), field("value", "string", keys.map((k) => labels[k]))] };
    });
  }
  return frames.map((frame) => {
    const n = frameLength(frame);
    const added = new Map<string, string>();
    const fields: Field[] = [];
    for (const f of frame.fields) {
      if (!f.labels) {
        fields.push(f);
        continue;
      }
      const out = cloneField(f, f.values);
      for (const [k, v] of Object.entries(f.labels)) {
        if (k === o.valueLabel) {
          out.state.displayName = v;
          out.config.displayName = undefined;
        } else if (!keep || keep.includes(k)) {
          added.set(k, v);
        }
      }
      out.labels = undefined;
      if (!o.valueLabel) out.state.displayName = f.name;
      fields.push(out);
    }
    for (const [k, v] of added) fields.push(field(k, "string", new Array(n).fill(v)));
    return { ...frame, fields };
  });
}

// ---- Group, sort, limit, convert ----

function groupBy(frames: Frame[], o: any): Frame[] {
  const spec: Record<string, { operation?: string; aggregations?: string[] }> = o.fields ?? {};
  return frames.map((frame) => {
    const groupFields = frame.fields.filter((f) => spec[displayName(f, frame)]?.operation === "groupby" || spec[f.name]?.operation === "groupby");
    if (!groupFields.length) return frame;
    const aggFields = frame.fields
      .map((f) => ({ f, s: spec[displayName(f, frame)] ?? spec[f.name] }))
      .filter((x) => x.s?.operation === "aggregate" && x.s.aggregations?.length);
    const groups = new Map<string, number[]>();
    const n = frameLength(frame);
    for (let r = 0; r < n; r++) {
      const key = JSON.stringify(groupFields.map((f) => f.values[r] ?? null));
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key)!.push(r);
    }
    const rows = [...groups.values()];
    const fields: Field[] = groupFields.map((f) => cloneField(f, rows.map((rs) => f.values[rs[0]])));
    for (const { f, s } of aggFields) {
      for (const id of s!.aggregations!) {
        const out = field(`${displayName(f, frame)} (${id})`, "number", rows.map((rs) => reduce(rs.map((r) => f.values[r]), id)));
        out.config = { ...f.config, displayName: undefined };
        fields.push(out);
      }
    }
    return { ...frame, fields };
  });
}

function compare(a: unknown, b: unknown): number {
  if (a === b) return 0;
  if (a === null || a === undefined) return 1;
  if (b === null || b === undefined) return -1;
  const x = toNumber(a);
  const y = toNumber(b);
  if (x !== null && y !== null) return x - y;
  return String(a).localeCompare(String(b));
}

function sortBy(frames: Frame[], o: any): Frame[] {
  const sort = Array.isArray(o.sort) ? o.sort[0] : undefined;
  if (!sort?.field) return frames;
  return frames.map((frame) => {
    const f = frame.fields.find((x) => matchesName(x, frame, sort.field));
    if (!f) return frame;
    const rows = f.values.map((_, r) => r).sort((a, b) => compare(f.values[a], f.values[b]) * (sort.desc ? -1 : 1));
    return withRows(frame, rows);
  });
}

function limit(frames: Frame[], o: any): Frame[] {
  const n = Math.floor(Number(o.limitField ?? 10));
  if (!Number.isFinite(n) || n < 0) return frames;
  return frames.map((frame) => ({ ...frame, fields: frame.fields.map((f) => cloneField(f, f.values.slice(0, n))) }));
}

// DATE_TOKENS are the moment.js tokens a date format accepts that
// identify a point in time; other letters must appear literally.
const DATE_TOKENS: [string, string][] = [
  ["YYYY", "(\\d{4})"], ["MM", "(\\d{1,2})"], ["DD", "(\\d{1,2})"], ["HH", "(\\d{1,2})"],
  ["mm", "(\\d{1,2})"], ["ss", "(\\d{1,2})"], ["SSS", "(\\d{1,3})"], ["X", "(\\d+)"],
];

/**
 * parseDate reads text in a moment.js-style format such as
 * "DD/MM/YYYY HH:mm", in the viewer's
 * local time. X is Unix seconds. It returns null when the text does not fit.
 */
export function parseDate(text: string, format: string): number | null {
  let pattern = "";
  const order: string[] = [];
  for (let i = 0; i < format.length; ) {
    const token = DATE_TOKENS.find(([t]) => format.startsWith(t, i));
    if (token) {
      pattern += token[1];
      order.push(token[0]);
      i += token[0].length;
    } else {
      pattern += format[i].replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      i++;
    }
  }
  const m = new RegExp(`^${pattern}$`).exec(text.trim());
  if (!m) return null;
  const part: Record<string, number> = {};
  order.forEach((t, k) => (part[t] = Number(m[k + 1])));
  if (part.X !== undefined) return part.X * 1000;
  const d = new Date(part.YYYY ?? 1970, (part.MM ?? 1) - 1, part.DD ?? 1, part.HH ?? 0, part.mm ?? 0, part.ss ?? 0, part.SSS ?? 0);
  return Number.isNaN(d.getTime()) ? null : d.getTime();
}

function convert(v: unknown, to: string, dateFormat?: string): unknown {
  if (v === null || v === undefined) return null;
  switch (to) {
    case "number":
      return toNumber(v);
    case "string":
      return typeof v === "object" ? JSON.stringify(v) : String(v);
    case "boolean":
      if (typeof v === "string") return ["true", "1", "on", "yes"].includes(v.trim().toLowerCase());
      return Boolean(v);
    case "time": {
      if (typeof v === "number") return v;
      const s = String(v).trim();
      if (dateFormat) return parseDate(s, dateFormat);
      if (/^-?\d+$/.test(s)) return Number(s);
      const t = Date.parse(s);
      return Number.isNaN(t) ? null : t;
    }
  }
  return v;
}

function convertFieldType(frames: Frame[], o: any): Frame[] {
  const conversions: any[] = Array.isArray(o.conversions) ? o.conversions : [];
  return frames.map((frame) => ({
    ...frame,
    fields: frame.fields.map((f) => {
      const c = conversions.find((x) => x?.targetField && matchesName(f, frame, x.targetField));
      if (!c || !["number", "string", "boolean", "time"].includes(c.destinationType)) return f;
      const out = cloneField(f, f.values.map((v) => convert(v, c.destinationType, c.dateFormat || undefined)));
      out.type = c.destinationType;
      return out;
    }),
  }));
}
