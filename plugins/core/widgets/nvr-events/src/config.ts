// The widget's config is itself one NVR binding: a camera of a connection, or
// every camera with "*". Placing the widget is what lets a dashboard's viewers
// read those cameras' events and their media, and nothing else.

import { parseAttr } from "../../shared/dashboardLive";

export interface NVREventsConfig {
  source: "nvr";
  connectionId: string;
  /** camera is one camera name, or "*" for every camera. */
  camera: string;
  displayOnly: true;
  /** labels and zones narrow the list, comma separated. */
  labels: string;
  zones: string;
  limit: number;
  showThumbnails: boolean;
}

/** NVREvent is one event as the server lists it. */
export interface NVREvent {
  id: string;
  camera: string;
  label: string;
  sub_label: string | null;
  zones: string[];
  score: number;
  start_time: string;
  end_time: string | null;
  has_clip: boolean;
  has_snapshot: boolean;
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): NVREventsConfig {
  const fields = parseAttr(attr);
  const limit = Number(fields.limit);
  return {
    source: "nvr",
    connectionId: String(fields.connectionId ?? ""),
    camera: String(fields.camera ?? ""),
    displayOnly: true,
    labels: String(fields.labels ?? ""),
    zones: String(fields.zones ?? ""),
    limit: Number.isFinite(limit) && limit >= 1 ? Math.min(Math.round(limit), 100) : 10,
    showThumbnails: fields.showThumbnails !== false,
  };
}

/** eventsPath is the listing route for the config. */
export function eventsPath(cfg: NVREventsConfig): string {
  const params = new URLSearchParams();
  if (cfg.camera && cfg.camera !== "*") params.set("cameras", cfg.camera);
  if (cfg.labels.trim()) params.set("labels", cfg.labels.trim());
  if (cfg.zones.trim()) params.set("zones", cfg.zones.trim());
  params.set("limit", String(cfg.limit));
  return `/connections/${encodeURIComponent(cfg.connectionId)}/nvr/events?${params}`;
}

/** mediaPath is the relay route for one event's media. */
export function mediaPath(cfg: NVREventsConfig, id: string, media: "thumbnail" | "snapshot" | "clip"): string {
  return `/connections/${encodeURIComponent(cfg.connectionId)}/nvr/events/${encodeURIComponent(id)}/${media}`;
}

/** when renders an event's start for the list: a time today, a date before. */
export function when(iso: string, now = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const sameDay = d.toDateString() === now.toDateString();
  return sameDay
    ? d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })
    : d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " +
        d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

/** title names an event: its label, and its sub label when it has one. */
export function title(e: NVREvent): string {
  const label = e.label.charAt(0).toUpperCase() + e.label.slice(1);
  return e.sub_label ? `${label}: ${e.sub_label}` : label;
}
