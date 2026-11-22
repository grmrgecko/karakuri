// Field option resolution. A field's own config (its source's unit, a
// transformation's rename) wins over the panel defaults, and
// every matching override applies on top in order, later ones winning.

import type { Field, FieldConfig, FieldConfigSource, Frame, Override } from "./types";
import { displayName } from "./frames";
import { parseRegex } from "./mappings";
import { toNumber } from "./reducers";

/** MATCHERS lists the override matchers for the editor. */
export const MATCHERS = [
  { id: "byName", name: "Fields with name" },
  { id: "byRegexp", name: "Fields with name matching regex" },
  { id: "byFrameRefID", name: "Fields returned by query" },
  { id: "byType", name: "Fields with type" },
];

/** matches reports whether an override's matcher selects a field. */
export function matches(override: Override, field: Field, frame: Frame): boolean {
  const m = override?.matcher;
  if (!m) return false;
  switch (m.id) {
    case "byName":
      return typeof m.options === "string" && (displayName(field, frame) === m.options || field.name === m.options);
    case "byNames": {
      const list: string[] = Array.isArray(m.options?.names) ? m.options.names : [];
      return list.includes(displayName(field, frame)) || list.includes(field.name);
    }
    case "byRegexp": {
      const re = typeof m.options === "string" && m.options ? parseRegex(m.options) : null;
      return !!re && re.test(displayName(field, frame));
    }
    case "byFrameRefID":
      return !!m.options && (field.state.refId === m.options || frame.refId === m.options);
    case "byType":
      return field.type === m.options;
  }
  return false;
}

// setProperty applies one override property. custom.a.b sets a nested
// visualization option; anything else is a standard option.
function setProperty(config: FieldConfig, id: string, value: unknown): void {
  if (id.startsWith("custom.")) {
    const path = id.slice(7).split(".");
    config.custom = { ...(config.custom ?? {}) };
    let target: Record<string, any> = config.custom;
    for (let i = 0; i < path.length - 1; i++) {
      target[path[i]] = { ...(target[path[i]] ?? {}) };
      target = target[path[i]];
    }
    target[path[path.length - 1]] = value;
    return;
  }
  (config as Record<string, unknown>)[id] = value;
}

function defined<T extends object>(o: T): Partial<T> {
  const out: Partial<T> = {};
  for (const [k, v] of Object.entries(o)) {
    if (v !== undefined && v !== null && v !== "") (out as any)[k] = v;
  }
  return out;
}

/** resolveConfig returns a field's config with defaults and matching overrides applied. */
export function resolveConfig(field: Field, frame: Frame, source: FieldConfigSource): FieldConfig {
  const defaults = source.defaults ?? {};
  const own = defined(field.config);
  // What the source reports about a field (its unit, for
  // one) wins over the panel defaults, and overrides win over both. Units
  // only label values, so the source's own unit is the truthful one.
  const config: FieldConfig = { ...defined(defaults), ...own };
  config.custom = { ...(defaults.custom ?? {}), ...(field.config.custom ?? {}) };
  for (const override of source.overrides ?? []) {
    if (!matches(override, field, frame)) continue;
    for (const p of override.properties ?? []) {
      if (p?.id) setProperty(config, p.id, p.value);
    }
  }
  return config;
}

/**
 * applyFieldConfig resolves every field's config, numbers the drawn series
 * for palette colours, and records the range thresholds and gradients use:
 * the configured min and max, else the range of all number fields.
 */
export function applyFieldConfig(frames: Frame[], source: FieldConfigSource): Frame[] {
  let globalMin = Infinity;
  let globalMax = -Infinity;
  for (const frame of frames) {
    for (const f of frame.fields) {
      if (f.type !== "number") continue;
      for (const v of f.values) {
        const n = toNumber(v);
        if (n === null) continue;
        if (n < globalMin) globalMin = n;
        if (n > globalMax) globalMax = n;
      }
    }
  }
  const range = Number.isFinite(globalMin) ? { min: globalMin, max: globalMax } : undefined;
  let index = 0;
  return frames.map((frame) => ({
    ...frame,
    fields: frame.fields.map((f) => {
      const config = resolveConfig(f, frame, source);
      const seriesIndex = f.type === "time" ? undefined : index++;
      return { ...f, config, state: { ...f.state, seriesIndex, range } };
    }),
  }));
}

/** dropHidden removes the fields of hidden queries once transformations have used them. */
export function dropHidden(frames: Frame[], hidden: Set<string>): Frame[] {
  if (!hidden.size) return frames;
  return frames
    .filter((frame) => !(frame.refId && hidden.has(frame.refId) && frame.fields.every((f) => !f.state.refId || hidden.has(f.state.refId))))
    .map((frame) => ({
      ...frame,
      fields: frame.fields.filter((f) => f.type === "time" || !f.state.refId || !hidden.has(f.state.refId)),
    }))
    .filter((frame) => frame.fields.some((f) => f.type !== "time"));
}

/** hiddenFromViz reports whether an override or default hides a field from drawing. */
export function hiddenFromViz(field: Field): boolean {
  return field.config.custom?.hideFrom?.viz === true;
}
