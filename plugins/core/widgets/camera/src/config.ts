// The widget's config is itself one binding, so the manifest declares it with
// `bindings: "."` and placing the widget is what lets a dashboard's viewers
// read that one camera: a Home Assistant camera entity, or an NVR camera. A
// camera is never pressed, so the binding is always display only.

import { parseAttr } from "../../shared/dashboardLive";

export interface CameraConfig {
  /** source is a Home Assistant camera entity or an NVR camera. */
  source: "ha" | "nvr";
  connectionId: string;
  /** entityId is the Home Assistant camera, for source ha. */
  entityId: string;
  /** camera is the NVR camera name, for source nvr. */
  camera: string;
  displayOnly: true;
  /** mode is a relayed MJPEG stream, or a still refreshed every interval. */
  mode: "live" | "snapshot";
  /** interval is the snapshot period in seconds, also used while a stream reconnects. */
  interval: number;
  fit: "cover" | "contain";
  /** showName overlays the camera's friendly name. */
  showName: boolean;
  /** bbox draws the NVR's boxes around tracked objects, for source nvr. */
  bbox: boolean;
}

/** parseConfig reads the host's config attribute with defaults filled in. */
export function parseConfig(attr: string | Record<string, unknown>): CameraConfig {
  const fields = parseAttr(attr);
  const interval = Number(fields.interval);
  return {
    source: fields.source === "nvr" ? "nvr" : "ha",
    connectionId: String(fields.connectionId ?? ""),
    entityId: String(fields.entityId ?? ""),
    camera: String(fields.camera ?? ""),
    displayOnly: true,
    mode: fields.mode === "snapshot" ? "snapshot" : "live",
    interval: Number.isFinite(interval) && interval >= 1 ? interval : 10,
    fit: fields.fit === "contain" ? "contain" : "cover",
    showName: fields.showName === true,
    bbox: fields.bbox === true,
  };
}

/** isBound reports whether the config names a camera. */
export function isBound(cfg: CameraConfig): boolean {
  if (!cfg.connectionId) return false;
  return cfg.source === "nvr" ? !!cfg.camera : !!cfg.entityId;
}

/** cameraPath is the relay route for one of the camera's resources. */
export function cameraPath(cfg: CameraConfig, resource: "snapshot" | "stream", height = 0): string {
  const conn = encodeURIComponent(cfg.connectionId);
  if (cfg.source === "nvr") {
    const params = new URLSearchParams();
    if (height > 0) params.set("height", String(height));
    if (cfg.bbox) params.set("bbox", "1");
    const query = params.toString();
    return `/connections/${conn}/nvr/cameras/${encodeURIComponent(cfg.camera)}/${resource}${query ? "?" + query : ""}`;
  }
  return `/connections/${conn}/ha/camera/${encodeURIComponent(cfg.entityId)}/${resource}`;
}

/**
 * streamHeight picks the height a live NVR stream is asked for: the cell's
 * height in device pixels, rounded up to a step so viewers of the same camera
 * at similar sizes share one upstream.
 */
export function streamHeight(cellHeight: number, ratio: number): number {
  if (cellHeight <= 0) return 0;
  const steps = [180, 360, 480, 720, 1080];
  const want = Math.ceil(cellHeight * (ratio || 1));
  for (const step of steps) if (want <= step) return step;
  return 0;
}
