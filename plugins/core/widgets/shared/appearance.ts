// A binding's states give it a look per reading: a muted microphone on red, an
// open door in orange. They compare the binding's own value only, so they
// need no grant beyond the binding's and cannot reveal another source.

import { attributeValue, formatValue, isComposite, primaryAttribute } from "./controlKinds";
import type { Resolved } from "./entitySource";

export type StateOperator = "equals" | "not_equals" | "contains" | "above" | "below";

/** StateLook is one entry of a binding's states: when it applies and what it changes. */
export interface StateLook {
  /** attribute compares one part of a composite reading; empty uses the primary part. */
  attribute?: string;
  /** operator with no value set matches nothing; no operator always matches. */
  operator?: StateOperator;
  value?: string;
  icon?: string;
  iconColor?: string;
  background?: string;
  /** color is the text color. */
  color?: string;
  /** label replaces the name; {value} and {name} are filled in. */
  label?: string;
}

/** MAX_STATES is how many states a binding may hold. */
export const MAX_STATES = 16;

/** STATE_OPERATORS lists the comparisons a state may use, with panel labels. */
export const STATE_OPERATORS: { value: StateOperator; label: string }[] = [
  { value: "equals", label: "equals" },
  { value: "not_equals", label: "does not equal" },
  { value: "contains", label: "contains" },
  { value: "above", label: "is above" },
  { value: "below", label: "is below" },
];

/**
 * readingOf is the value a state compares: the named attribute, else a
 * composite kind's primary attribute, else the value itself. A switch reads as
 * on or off, which is the word an author writes.
 */
function readingOf(look: StateLook, resolved: Resolved): unknown {
  const attribute = (look.attribute ?? "").trim();
  let value: unknown;
  if (attribute) value = attributeValue(resolved.kind, resolved.value, attribute);
  else if (isComposite(resolved.kind)) value = attributeValue(resolved.kind, resolved.value, primaryAttribute(resolved.kind));
  else value = resolved.value;
  if (typeof value === "boolean") return value ? "on" : "off";
  return value;
}

/** wordOf turns an author's true or false into the on or off a switch reads as. */
function wordOf(want: string): string {
  const lower = want.trim().toLowerCase();
  if (lower === "true") return "on";
  if (lower === "false") return "off";
  return lower;
}

/** compareState applies one state's comparison to a reading. */
export function compareState(actual: unknown, operator: StateOperator | undefined, expected: string | undefined): boolean {
  if (!operator) return true;
  if (actual === undefined || actual === null) return false;
  const text = (typeof actual === "object" ? JSON.stringify(actual) : String(actual)).trim().toLowerCase();
  const want = wordOf(expected ?? "");
  switch (operator) {
    case "equals":
      return text === want;
    case "not_equals":
      return text !== want;
    case "contains":
      return want !== "" && text.includes(want);
    case "above":
    case "below": {
      if (typeof actual === "object" || text === "" || want === "") return false;
      const a = Number(actual);
      const b = Number(want);
      if (!Number.isFinite(a) || !Number.isFinite(b)) return false;
      return operator === "above" ? a > b : a < b;
    }
  }
  return false;
}

/**
 * matchState returns the first of a binding's states that matches its
 * reading, or null. A source that cannot answer matches none, so a dead
 * sensor does not wear the look of a real reading.
 */
export function matchState(states: StateLook[] | undefined, resolved: Resolved, ready = true): StateLook | null {
  if (!ready || resolved.unavailable || !Array.isArray(states)) return null;
  for (const look of states.slice(0, MAX_STATES)) {
    if (!look || typeof look !== "object") continue;
    if (compareState(readingOf(look, resolved), look.operator, look.value)) return look;
  }
  return null;
}

/** stateLabel fills a state's label with the binding's formatted value and name. */
export function stateLabel(label: string, resolved: Resolved, name: string): string {
  const value = isComposite(resolved.kind)
    ? attributeValue(resolved.kind, resolved.value, primaryAttribute(resolved.kind))
    : resolved.value;
  return label
    .replaceAll("{value}", formatValue(value, resolved.options))
    .replaceAll("{name}", name);
}

/** safeColor drops a color that could end the style declaration it is written into. */
export function safeColor(value: string | undefined): string {
  const v = (value ?? "").trim();
  if (!v || v.length > 64 || /[;{}<>\\"']/.test(v) || /url\(/i.test(v)) return "";
  return v;
}
