// A Button is one binding drawn as a square a hand can hit: a large icon, a
// short label, and an optional reading. The binding, its states, and its
// format live in ../../shared with Entity Control's, so a light bound here
// behaves as one bound there.

import type { EntitySource, Resolved } from "../../shared/entitySource";
import { truthy } from "../../shared/entitySource";

export interface ButtonConfig extends EntitySource {
  /** showLabel draws the label under the icon. */
  showLabel: boolean;
  /** showValue draws the formatted reading under the label. */
  showValue: boolean;
}

export const DEFAULT_CONFIG: ButtonConfig = {
  source: "control",
  showLabel: true,
  showValue: false,
};

/** parseConfig reads the host's config attribute, whatever form it arrives in. */
export function parseConfig(attr: string | Record<string, unknown>): ButtonConfig {
  let parsed: Record<string, unknown> = {};
  if (typeof attr === "string") {
    try {
      parsed = JSON.parse(attr);
    } catch {
      parsed = {};
    }
  } else if (attr && typeof attr === "object") {
    parsed = attr;
  }
  // The host injects the placement's IDs on the way in; they are its
  // bookkeeping, not the binding's.
  const { dashboardId, dashboardWidgetId, dashboardReferences, ...fields } = parsed;
  void dashboardId;
  void dashboardWidgetId;
  void dashboardReferences;
  return {
    ...DEFAULT_CONFIG,
    ...(fields as Partial<ButtonConfig>),
    showLabel: fields.showLabel !== false,
    showValue: fields.showValue === true,
  };
}

/** Action is what one press of a Button sends, or null when it sends nothing. */
export type Action =
  | { kind: "command"; attribute: string; value: unknown }
  | { kind: "trigger"; pressId: string };

/**
 * singleAction is the one thing a press does for a reading: a switch, light,
 * or fan toggles; a lock flips; a buttons control with one action, a scene,
 * or a script runs it; a flow trigger fires its first press. Anything that
 * needs a value chosen, a number or a list, has no single action.
 */
export function singleAction(cfg: EntitySource, resolved: Resolved, primary: (kind: string) => string): Action | null {
  if (cfg.source === "flow-trigger") {
    const first = (resolved.options?.buttons as Array<{ value: string }> | undefined)?.[0];
    return first ? { kind: "trigger", pressId: first.value } : null;
  }
  switch (resolved.kind) {
    case "boolean":
    case "light":
    case "fan": {
      const attribute = primary(resolved.kind);
      const current =
        resolved.value && typeof resolved.value === "object"
          ? (resolved.value as Record<string, unknown>)[attribute]
          : resolved.value;
      return { kind: "command", attribute, value: !truthy(current) };
    }
    case "lock": {
      const state =
        resolved.value && typeof resolved.value === "object"
          ? String((resolved.value as Record<string, unknown>).state ?? "")
          : String(resolved.value ?? "");
      return { kind: "command", attribute: "state", value: state === "locked" ? "unlocked" : "locked" };
    }
    case "buttons": {
      const buttons = (resolved.options?.buttons as Array<{ value: string }> | undefined) ?? [];
      if (buttons.length > 1) return null;
      return { kind: "command", attribute: "value", value: buttons[0]?.value ?? true };
    }
  }
  return null;
}

/** isOn reports whether a reading tints the button as on. */
export function isOn(resolved: Resolved): boolean {
  const value = resolved.value;
  if (resolved.kind === "lock") {
    const state = value && typeof value === "object" ? (value as Record<string, unknown>).state : value;
    return state === "unlocked";
  }
  if (resolved.kind === "buttons" || resolved.kind === "text") return false;
  if (value && typeof value === "object") return truthy((value as Record<string, unknown>).on);
  return truthy(value);
}
