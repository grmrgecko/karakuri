// The widget's config is itself one Home Assistant binding (`bindings: "."`),
// so placing it grants the dashboard's viewers this one list, and ticking or
// adding items only when the author left it pressable.

import { parseAttr } from "../../shared/dashboardLive";

export interface TodoConfig {
  source: "ha";
  connectionId: string;
  entityId: string;
  displayOnly: boolean;
  /** title heads the list; empty uses the entity's friendly name. */
  title: string;
  showCompleted: boolean;
  allowAdd: boolean;
  /** maxItems caps the rows drawn; 0 draws them all. */
  maxItems: number;
}

/** TodoItem is one row of Home Assistant's todo/item/list. */
export interface TodoItem {
  uid: string;
  summary: string;
  status: "needs_action" | "completed";
  due?: string;
  description?: string;
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): TodoConfig {
  const fields = parseAttr(attr);
  const max = Math.floor(Number(fields.maxItems));
  return {
    source: "ha",
    connectionId: String(fields.connectionId ?? ""),
    entityId: String(fields.entityId ?? ""),
    displayOnly: fields.displayOnly === true,
    title: String(fields.title ?? ""),
    showCompleted: fields.showCompleted === true,
    allowAdd: fields.allowAdd !== false,
    maxItems: Number.isFinite(max) && max > 0 ? max : 0,
  };
}

/**
 * dueLabel renders an item's due date or date-time briefly. Home Assistant
 * sends a bare date for all-day items, which must not be shifted into the
 * previous day by reading it as UTC midnight.
 */
export function dueLabel(due: string | undefined): string {
  if (!due) return "";
  const dateOnly = /^\d{4}-\d{2}-\d{2}$/.test(due);
  const when = dateOnly ? new Date(`${due}T00:00:00`) : new Date(due);
  if (Number.isNaN(when.getTime())) return due;
  const options: Intl.DateTimeFormatOptions = { month: "short", day: "numeric" };
  if (!dateOnly) {
    options.hour = "numeric";
    options.minute = "2-digit";
  }
  return when.toLocaleString(undefined, options);
}
