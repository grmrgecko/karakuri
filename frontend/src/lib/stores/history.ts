import { api } from '../api/client';
import type { Connection } from './connections';

// A history forward writes control, MQTT device, or Home Assistant values to
// time-series connections. Forwards are live settings: saving one takes
// effect at once, with no draft.

export type ForwardSourceType = 'control' | 'mqtt_device' | 'ha';

// WriterStatus is one writable connection's delivery state.
export interface WriterStatus {
  state: 'idle' | 'ok' | 'failing' | 'disabled';
  last_error?: string;
  last_error_at?: string;
  last_success?: string;
  buffered_points: number;
  buffered_bytes: number;
  written: number;
  discarded: number;
  rejected: number;
  skipped: number;
}

// DestinationStatus is one destination of a forward. Writer is absent until
// the writer has handled points.
export interface DestinationStatus {
  connection_id: string;
  name?: string;
  missing?: boolean;
  writer?: WriterStatus;
}

export interface HistoryForward {
  id: string;
  name: string;
  enabled: boolean;
  source_type: ForwardSourceType;
  source_ids: string[] | null;
  owner_id?: string;
  connection_id?: string;
  include: string[] | null;
  exclude: string[] | null;
  attributes: string[] | null;
  all_attributes: boolean;
  destinations: string[] | null;
  metric?: string;
  labels: Record<string, string> | null;
  min_interval_ms: number;
  created_at?: string;
  updated_at?: string;
  status?: DestinationStatus[];
}

// ConnectionWriterStatus is one entry of GET /history/status.
export interface ConnectionWriterStatus {
  connection_id: string;
  name: string;
  kind: string;
  writer?: WriterStatus;
}

// WRITABLE_KINDS are the connection kinds a forward can write to.
export const WRITABLE_KINDS = ['prometheus', 'influxdb', 'postgres'];

// GATED_KINDS only write when their config turns writing on.
const GATED_KINDS = ['prometheus', 'influxdb', 'postgres'];

export function isWritable(connection: Connection): boolean {
  return WRITABLE_KINDS.includes(connection.kind);
}

// writeHint says why a destination will not record anything, or '' when it
// will.
export function writeHint(connection: Connection): string {
  if (!connection.enabled) return 'disabled';
  if (GATED_KINDS.includes(connection.kind) && connection.config?.write_enabled !== true) {
    return 'writing off';
  }
  return '';
}

// ownerSource is the list filter for the forward a control or device owns.
export function ownerSource(type: 'control' | 'mqtt_device', id: string): string {
  return `${type}:${id}`;
}

// loadOwnedForward returns the forward a control or device editor owns, or
// null when it has none.
export async function loadOwnedForward(
  type: 'control' | 'mqtt_device',
  id: string,
): Promise<HistoryForward | null> {
  const list = (await api.history.listForwards<HistoryForward[]>(ownerSource(type, id))) || [];
  return list[0] ?? null;
}

// saveOwnedForward stores or removes the forward an editor owns once the
// control or device itself is saved. An existing forward keeps the options
// set in Settings → History forwarding; only its destinations change and it
// is switched back on.
export async function saveOwnedForward(opts: {
  type: 'control' | 'mqtt_device';
  id: string;
  name: string;
  record: boolean;
  destinations: string[];
  existing: HistoryForward | null;
}): Promise<void> {
  const { type, id, name, record, destinations, existing } = opts;
  if (!record) {
    if (existing) await api.history.deleteForward(existing.id);
    return;
  }
  if (existing) {
    const same =
      existing.enabled &&
      (existing.destinations ?? []).length === destinations.length &&
      destinations.every((d) => (existing.destinations ?? []).includes(d));
    if (same) return;
    await api.history.updateForward(existing.id, {
      ...forwardBody(existing),
      enabled: true,
      destinations,
    });
    return;
  }
  await api.history.createForward({
    name: `${name} history`,
    enabled: true,
    source_type: type,
    owner_id: id,
    source_ids: [id],
    destinations,
  });
}

// forwardBody is a forward as the API takes it back: lists that came back
// null are sent empty, and the status and timestamps the server owns are left
// out.
export function forwardBody(f: HistoryForward): Record<string, unknown> {
  return {
    name: f.name,
    enabled: f.enabled,
    source_type: f.source_type,
    source_ids: f.source_ids ?? [],
    ...(f.owner_id ? { owner_id: f.owner_id } : {}),
    ...(f.connection_id ? { connection_id: f.connection_id } : {}),
    include: f.include ?? [],
    exclude: f.exclude ?? [],
    attributes: f.attributes ?? [],
    all_attributes: f.all_attributes,
    destinations: f.destinations ?? [],
    metric: f.metric ?? '',
    labels: f.labels ?? {},
    min_interval_ms: f.min_interval_ms,
  };
}

// formatBytes renders a buffer or archive size.
export function formatBytes(n: number): string {
  if (!n) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  // 1023.96 KiB would print as "1024.0 KiB"; it is 1.0 MiB.
  if (i > 0 && i < units.length - 1 && Number(v.toFixed(1)) >= 1024) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}
