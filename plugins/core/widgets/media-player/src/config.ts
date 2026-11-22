// The widget's config is itself one binding (`bindings: "."`), so placing it
// grants the dashboard's viewers this one player, and pressing it only when
// the author left it pressable. The binding is a Home Assistant media_player,
// a media control, or an MQTT or ESPHome device of kind media; each resolves
// to the media kind through the shared entity source.

import { parseAttr } from "../../shared/dashboardLive";

export type MediaSource = "ha" | "control" | "mqtt";

export interface MediaConfig {
  source: MediaSource;
  connectionId: string;
  entityId: string;
  controlId: string;
  deviceId: string;
  displayOnly: boolean;
  showArt: boolean;
  showControls: boolean;
  showVolume: boolean;
  layout: "card" | "compact";
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): MediaConfig {
  const fields = parseAttr(attr);
  // A placement saved before other sources existed has source "ha" or none,
  // and reads back exactly as it was.
  const source: MediaSource =
    fields.source === "control" || fields.source === "mqtt" ? fields.source : "ha";
  return {
    source,
    connectionId: String(fields.connectionId ?? ""),
    entityId: String(fields.entityId ?? ""),
    controlId: String(fields.controlId ?? ""),
    deviceId: String(fields.deviceId ?? ""),
    displayOnly: fields.displayOnly === true,
    showArt: fields.showArt !== false,
    showControls: fields.showControls !== false,
    showVolume: fields.showVolume === true,
    layout: fields.layout === "compact" ? "compact" : "card",
  };
}

/** isBound reports whether the config names a player for its source. */
export function isBound(cfg: MediaConfig): boolean {
  switch (cfg.source) {
    case "control":
      return !!cfg.controlId;
    case "mqtt":
      return !!cfg.deviceId;
    default:
      return !!cfg.connectionId && !!cfg.entityId;
  }
}

const STATE_LABELS: Record<string, string> = {
  playing: "Playing",
  paused: "Paused",
  idle: "Idle",
  off: "Off",
  on: "On",
  standby: "Standby",
  buffering: "Buffering",
  unavailable: "Unavailable",
  unknown: "Unknown",
};

/** stateLabel is the sentence-case word for a player's state. */
export function stateLabel(state: string): string {
  return STATE_LABELS[state] ?? (state ? state[0].toUpperCase() + state.slice(1) : "");
}

/** clock renders seconds as m:ss, or h:mm:ss past an hour. */
export function clock(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}
