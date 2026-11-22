// The widget's rows are bindings to controls, devices, and Home Assistant
// entities, each display only. Placing the widget is what lets a dashboard's
// viewers read those subjects' event log entries, and nothing else.

import { parseAttr } from "../../shared/dashboardLive";

export type RowSource = "control" | "mqtt" | "ha";

export interface Row {
  id: string;
  source: RowSource;
  controlId?: string;
  deviceId?: string;
  connectionId?: string;
  entityId?: string;
  displayOnly: true;
}

export interface ActivityConfig {
  rows: Row[];
  /** categories and levels narrow the list, comma separated. */
  categories: string;
  levels: string;
  limit: number;
  showActor: boolean;
}

/** Entry is one event log entry as the server lists it. */
export interface Entry {
  id: number;
  at: string;
  last_at: string;
  count: number;
  category: string;
  level: "info" | "warning" | "error";
  subject_kind?: string;
  subject_id?: string;
  subject_name?: string;
  message: string;
  actor_kind: string;
  actor_name?: string;
  via?: string;
}

function readRow(raw: any): Row | null {
  if (!raw || typeof raw !== "object") return null;
  const source = raw.source as RowSource;
  if (source !== "control" && source !== "mqtt" && source !== "ha") return null;
  return {
    id: String(raw.id ?? Math.random().toString(36).slice(2)),
    source,
    controlId: raw.controlId ? String(raw.controlId) : undefined,
    deviceId: raw.deviceId ? String(raw.deviceId) : undefined,
    connectionId: raw.connectionId ? String(raw.connectionId) : undefined,
    entityId: raw.entityId ? String(raw.entityId) : undefined,
    displayOnly: true,
  };
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): ActivityConfig {
  const fields = parseAttr(attr);
  const limit = Number(fields.limit);
  const rows = Array.isArray(fields.rows) ? (fields.rows as unknown[]).map(readRow).filter((r): r is Row => r !== null) : [];
  return {
    rows,
    categories: String(fields.categories ?? ""),
    levels: String(fields.levels ?? ""),
    limit: Number.isFinite(limit) && limit >= 1 ? Math.min(Math.round(limit), 50) : 10,
    showActor: fields.showActor !== false,
  };
}

/** subjectOf names the event log subject a row is bound to, as kind:id. */
export function subjectOf(row: Row): string {
  switch (row.source) {
    case "control":
      return row.controlId ? `control:${row.controlId}` : "";
    case "mqtt":
      return row.deviceId ? `device:${row.deviceId}` : "";
    case "ha":
      return row.connectionId && row.entityId ? `ha_entity:${row.connectionId}/${row.entityId}` : "";
  }
}

function list(raw: string): string[] {
  return raw.split(",").map((s) => s.trim()).filter(Boolean);
}

/** eventsPath is the list request for the configured subjects. */
export function eventsPath(cfg: ActivityConfig, subjects: string[]): string {
  const p = new URLSearchParams();
  subjects.forEach((s) => p.append("subject", s));
  list(cfg.categories).forEach((c) => p.append("category", c));
  list(cfg.levels).forEach((l) => p.append("level", l));
  p.set("limit", String(cfg.limit));
  return `/events?${p.toString()}`;
}

/** accepts applies the category and level filters to a live entry. */
export function accepts(cfg: ActivityConfig, e: Entry): boolean {
  const cats = list(cfg.categories);
  const levels = list(cfg.levels);
  return (cats.length === 0 || cats.includes(e.category)) && (levels.length === 0 || levels.includes(e.level));
}

/** ago renders a time relative to now. */
export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** actorText says who did it, briefly. */
export function actorText(e: Entry): string {
  switch (e.actor_kind) {
    case "flow":
      return e.actor_name ?? "a flow";
    case "system":
      return "karakuri";
    case "agent":
      return e.actor_name ?? "an agent";
    case "anonymous":
      return "someone";
  }
  return e.actor_name ?? e.actor_kind;
}
