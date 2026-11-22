import { get } from 'svelte/store';
import { api } from './api/client';
import { websocket } from './stores/websocket';
import type { Condition, LiveState } from './stores/dashboards';
import { sourceKey } from './displayState';

// liveTopic matches dashscope.LiveTopic on the server.
export function liveTopic(dashboardId: string): string {
  return `dashboard:${dashboardId}:live`;
}

/**
 * watchLive loads a dashboard's runtime display state and follows it. Each
 * message carries the whole state, so a stale one is dropped by sequence
 * number; after the socket reconnects the state is fetched again, since
 * whatever changed while it was down was never delivered.
 */
export function watchLive(
  dashboardId: string,
  onState: (state: LiveState, event: string | undefined) => void
): () => void {
  let stopped = false;
  let seq = 0;
  // pushed counts the states taken from the topic, so a read can tell
  // whether one arrived while it was in flight.
  let pushed = 0;

  const apply = (state: LiveState | null | undefined, event?: string, fresh = false): boolean => {
    if (stopped || !state) return false;
    if (!fresh && state.seq < seq) return false;
    seq = state.seq;
    onState(normalize(state), event);
    return true;
  };

  const load = async () => {
    const before = pushed;
    try {
      const state = await api.get<LiveState>(`/dashboards/${dashboardId}/live`);
      // A read replaces whatever the viewer holds, since a restarted server
      // answers with a lower number. A state pushed while the read was in
      // flight is the exception: the read may predate it, so the numbers
      // decide.
      apply(state, undefined, pushed === before);
    } catch {
      // A viewer that cannot read the state keeps the authored start state.
    }
  };

  const off = websocket.subscribeTopic(liveTopic(dashboardId), (data: any) => {
    if (apply(data?.state, data?.event)) pushed++;
  });

  let wasConnected = get(websocket).connected;
  const offSocket = websocket.subscribe((s) => {
    if (s.connected && !wasConnected) void load();
    wasConnected = s.connected;
  });

  void load();
  return () => {
    stopped = true;
    off();
    offSocket();
  };
}

/** normalize fills in the maps an empty state may omit. */
function normalize(state: LiveState): LiveState {
  return {
    ...state,
    locks: state.locks ?? {},
    alerts: state.alerts ?? [],
    content: state.content ?? {},
  };
}

/**
 * watchConditionSources loads and follows every source the given
 * conditions read, reporting a map of raw records keyed by sourceKey. It
 * reads through the same endpoints and topics widgets use, so a dashboard
 * viewer's grant covers them.
 */
export function watchConditionSources(
  conditions: Condition[],
  onRecords: (records: Record<string, unknown>) => void
): () => void {
  const records: Record<string, unknown> = {};
  const offs: (() => void)[] = [];
  let stopped = false;
  const set = (key: string, record: unknown) => {
    if (stopped) return;
    records[key] = record;
    onRecords({ ...records });
  };
  // A list read is a snapshot from when the server answered. A change pushed
  // while it was in flight is newer, so the list leaves that key alone.
  const pushed = new Set<string>();
  const push = (key: string, record: unknown) => {
    pushed.add(key);
    set(key, record);
  };
  const seed = (key: string, record: unknown) => {
    if (!pushed.has(key)) set(key, record);
  };

  const state = conditions.filter((c) => c.kind === 'state' && sourceKey(c));
  const controls = new Set(state.filter((c) => c.source === 'control' && c.controlId).map((c) => c.controlId!));
  const devices = new Set(state.filter((c) => c.source === 'mqtt' && c.deviceId).map((c) => c.deviceId!));
  const entities = new Map<string, Set<string>>();
  for (const c of state) {
    if (c.source !== 'ha' || !c.connectionId || !c.entityId) continue;
    if (!entities.has(c.connectionId)) entities.set(c.connectionId, new Set());
    entities.get(c.connectionId)!.add(c.entityId);
  }

  const list = (res: any): any[] => (Array.isArray(res) ? res : (res?.data ?? []));

  if (controls.size > 0) {
    api.get('/controls').then((res) => {
      for (const control of list(res)) {
        if (controls.has(control.id)) seed(`control:${control.id}`, control);
      }
    }).catch(() => {});
    for (const id of controls) {
      offs.push(websocket.subscribeTopic(`control:${id}`, (data: any) => {
        if (data?.deleted) push(`control:${id}`, undefined);
        else if (data?.control) push(`control:${id}`, data.control);
      }));
    }
  }

  for (const [connection, ids] of entities) {
    api.get(`/connections/${connection}/ha/states`).then((res) => {
      for (const entity of list(res)) {
        if (ids.has(entity.entity_id)) seed(`ha:${connection}:${entity.entity_id}`, entity);
      }
    }).catch(() => {});
    for (const entity of ids) {
      offs.push(websocket.subscribeTopic(`ha:${connection}:state:${entity}`, (data: any) => {
        if (data?.new_state) push(`ha:${connection}:${entity}`, data.new_state);
      }));
    }
  }

  if (devices.size > 0) {
    api.get('/mqtt_devices').then((res) => {
      for (const device of list(res)) {
        if (devices.has(device.id)) {
          seed(`mqtt:${device.id}`, { state: device.state, stale: !!device.stale, reported_at: device.reported_at });
        }
      }
    }).catch(() => {});
    for (const id of devices) {
      offs.push(websocket.subscribeTopic(`mqttdevice:${id}`, (data: any) => {
        if (data?.state) push(`mqtt:${id}`, { state: data.state, stale: false, reported_at: data.reported_at });
      }));
    }
  }

  return () => {
    stopped = true;
    for (const off of offs) off();
  };
}
