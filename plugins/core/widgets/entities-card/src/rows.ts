// A card is a list of rows, and a row is one binding plus the identity the card
// needs to order and key it. The binding itself, and what each source turns out
// to be, lives in ../../shared: the single Entity Control widget answers those
// same questions and two tables would drift.

import type { EntitySource } from "../../shared/entitySource";

export type {
  CommandRequest,
  Resolved,
  TriggerPress,
  ControlKindDef,
} from "../../shared/entitySource";

export {
  BLANK_RESOLVED,
  attributesOf,
  commandRequest,
  displayOnly,
  formatValue,
  haDomain,
  haService,
  loadControlKinds,
  pressable,
  resolveControl,
  resolveDevice,
  applyDeviceMessage,
  resolveHA,
  triggerPresses,
  truthy,
} from "../../shared/entitySource";

export type RowSource = EntitySource["source"];

/** Row is a binding with the ID that keys it within its card. */
export interface Row extends EntitySource {
  id: string;
}

export interface CardConfig {
  title: string;
  rows: Row[];
}

export const DEFAULT_CONFIG: CardConfig = { title: "", rows: [] };

/** newRowId mints a stable key for a freshly added row. */
export function newRowId(): string {
  return Math.random().toString(36).slice(2, 10);
}

/** parseConfig reads the host's config attribute, whatever form it arrives in. */
export function parseConfig(attr: string | Record<string, unknown>): CardConfig {
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
  const rows = Array.isArray(parsed.rows) ? (parsed.rows as Row[]) : [];
  return {
    title: typeof parsed.title === "string" ? parsed.title : "",
    rows: rows.filter((r) => r && typeof r === "object"),
  };
}
