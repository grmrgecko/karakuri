import { applyFormat } from "./formats";

// The control kind catalog, as a widget sees it. It is fetched from the host
// rather than restated here: the same list decides what a karakuri control
// holds, what an MQTT device declares itself as, and what a flow node's port
// promises, and a second copy in TypeScript would drift the first time a kind
// gained an attribute.
//
// Every source resolves to the same three things: which kind it is, that
// kind's settings, and whether it can be written. A widget draws from those
// and never asks the author what it is looking at.

export interface ControlAttribute {
  id: string;
  label: string;
  type: string;
  primary?: boolean;
  read_only?: boolean;
  /** write_only marks an action, such as a player's skip: written, never held. */
  write_only?: boolean;
  requires_option?: string;
}

export interface ControlKindDef {
  name: string;
  label: string;
  description: string;
  icon: string;
  value_type: string;
  attributes: ControlAttribute[];
  options: Array<Record<string, unknown>>;
  press_only?: boolean;
}

const bridge = () => (window as any).__karakuri;

// The catalog is the same for every widget on the page and never changes
// between deploys, so one request serves them all. A failure leaves the list
// empty rather than throwing: a widget with no catalog draws its value as text,
// which is wrong but readable, and retrying per widget would hammer the host.
let catalog: ControlKindDef[] = [];
let pending: Promise<ControlKindDef[]> | null = null;

export function loadControlKinds(): Promise<ControlKindDef[]> {
  if (catalog.length > 0) return Promise.resolve(catalog);
  if (pending) return pending;
  pending = (async () => {
    try {
      const res = await bridge().api.get("/controls/kinds");
      const list = Array.isArray(res) ? res : (res?.data ?? []);
      catalog = list as ControlKindDef[];
    } catch {
      catalog = [];
    }
    pending = null;
    return catalog;
  })();
  return pending;
}

/** findKind returns one kind's definition, or undefined before the catalog arrives. */
export function findKind(name: string): ControlKindDef | undefined {
  return catalog.find((k) => k.name === name);
}

// FEATURE_DEFAULTS mirror the options the catalog declares on by default, for
// the sources that never stored settings at all: a Home Assistant light is
// derived on the spot, and a device saved before a feature existed should keep
// what it had.
const FEATURE_DEFAULTS: Record<string, boolean> = {
  brightness: true,
  stop: true,
  speed: true,
  volume: true,
  mute: true,
  skip: true,
};

/** hasFeature reports whether a kind option gating an attribute is switched on. */
export function hasFeature(options: Record<string, unknown>, key: string): boolean {
  const value = options?.[key];
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  if (typeof value === "string") return value === "true";
  return FEATURE_DEFAULTS[key] ?? false;
}

/**
 * attributesOf lists the parts a control actually has, dropping the ones whose
 * feature is off. A light without colour offers no colour anywhere: not in the
 * face, not in the flow picker, not in a command the device would refuse.
 */
export function attributesOf(
  kind: string,
  options: Record<string, unknown>,
): ControlAttribute[] {
  const def = findKind(kind);
  if (!def) return [];
  return def.attributes.filter((a) => !a.requires_option || hasFeature(options, a.requires_option));
}

/** hasAttribute reports whether a control offers one named part. */
export function hasAttribute(
  kind: string,
  options: Record<string, unknown>,
  id: string,
): boolean {
  return attributesOf(kind, options).some((a) => a.id === id);
}

/** primaryAttribute is what a face with one thing to press commands. */
export function primaryAttribute(kind: string): string {
  const def = findKind(kind);
  return def?.attributes.find((a) => a.primary)?.id ?? def?.attributes[0]?.id ?? "value";
}

/** isComposite reports whether a kind's value is an object of attributes. */
export function isComposite(kind: string): boolean {
  return findKind(kind)?.value_type === "object";
}

// ---- Choices ----

export interface Choice {
  value: string;
  label: string;
}

/**
 * choices reads a value/label list out of a kind's settings, tolerating the
 * plain strings a control defined before labels existed still holds.
 */
export function choices(options: Record<string, unknown>, key: string): Choice[] {
  const raw = options?.[key];
  if (!Array.isArray(raw)) return [];
  return raw
    .map((item) => {
      if (item && typeof item === "object") {
        const entry = item as Record<string, unknown>;
        const value = String(entry.value ?? "");
        return { value, label: String(entry.label ?? "") || value };
      }
      const value = String(item ?? "");
      return { value, label: value };
    })
    .filter((c) => c.value !== "" || c.label !== "");
}

/** numberOption reads a numeric setting with a fallback for one never stored. */
export function numberOption(
  options: Record<string, unknown>,
  key: string,
  fallback: number,
): number {
  const raw = options?.[key];
  const n = typeof raw === "number" ? raw : Number(raw);
  return Number.isFinite(n) ? n : fallback;
}

/** textOption reads a text setting. */
export function textOption(options: Record<string, unknown>, key: string): string {
  const raw = options?.[key];
  return typeof raw === "string" ? raw : "";
}

// ---- Value shaping ----

/**
 * attributeValue pulls one part out of a control's value. A scalar kind is its
 * own value however it is asked for, so a face can read the same way whether it
 * is drawing a switch or a light's on state.
 */
export function attributeValue(kind: string, value: unknown, attribute: string): unknown {
  if (!attribute) return value;
  if (!isComposite(kind)) return value;
  if (!value || typeof value !== "object") return undefined;
  return (value as Record<string, unknown>)[attribute];
}

/** truthy is what a switch, a light, and a fan all read as on. */
export function truthy(value: unknown): boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "number") return value !== 0;
  const text = String(value ?? "")
    .trim()
    .toLowerCase();
  return ["true", "on", "1", "yes", "open", "opening", "unlocked", "playing", "home", "active"].includes(
    text,
  );
}

// ---- Display formatting ----
//
// Formatters exist for the sources with nowhere else to format. A Home
// Assistant sensor and a karakuri control have no script between the value and
// the screen; an MQTT device formats in its state script and leaves these
// unset.

/** formatValue renders a reading using the kind's own display settings. */
export function formatValue(value: unknown, options: Record<string, unknown>): string {
  if (value === null || value === undefined || value === "") return "—";

  const labels = options?.labels;
  if (labels && typeof labels === "object" && !Array.isArray(labels)) {
    const mapped = (labels as Record<string, unknown>)[String(value)];
    if (mapped !== undefined) return String(mapped);
  }

  // A binding's format is folded into the options by the resolver, so it
  // reaches every caller. It applies before labels: a format names a number,
  // and a label maps a word.
  const precision = options?.precision;
  const formatted = applyFormat(
    value,
    options?.format,
    typeof precision === "number" ? precision : precision === undefined ? undefined : Number(precision),
    textOption(options, "unit"),
  );
  if (formatted !== null) return `${textOption(options, "prefix")}${formatted}`;

  let text: string;
  if (typeof value === "boolean") {
    text = value ? "on" : "off";
  } else if (typeof value === "object") {
    text = JSON.stringify(value);
  } else {
    text = String(value);
  }

  // Precision applies to whatever reads as a number, which is what a sensor
  // reporting "21.4083" over MQTT arrives as.
  const digits = typeof precision === "number" ? precision : Number(precision);
  if (Number.isFinite(digits) && digits >= 0) {
    const n = Number(text);
    if (Number.isFinite(n)) text = n.toFixed(digits);
  }

  const prefix = textOption(options, "prefix");
  const unit = textOption(options, "unit");
  return `${prefix}${text}${unit ? ` ${unit}` : ""}`;
}
