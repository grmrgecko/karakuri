import { writable, get } from 'svelte/store';
import { api } from '../api/client';
import { websocket } from './websocket';

// RunInfo mirrors the run row / run payload published on flow:{id}:runs.
interface RunInfo {
  id: string;
  status: string; // running, succeeded, failed, or canceled.
  started_at: string | null;
  ended_at: string | null;
  error: string;
  had_failures: boolean;
  trigger_node_id?: string;
  // resumed_from_run_id is the run that armed the wait this run restored.
  resumed_from_run_id?: string;
}

// PendingWait is one timer, held message, or partial group that survives
// restarts and redeploys, as GET /flows/{id}/waits lists it.
export interface PendingWait {
  node_id: string;
  node_type: string;
  key: string;
  deadline: string;
  missed_deadline: string;
  run_id: string;
  durable: boolean;
}

// NodeRunState is the latest observed state of one node within a run.
interface NodeRunState {
  state: string; // running, retrying, succeeded, failed, or skipped.
  attempt: number;
  handled: boolean;
  error: string;
}

// DebugEntry is one debug emission from a node during a run.
interface DebugEntry {
  node_id: string;
  payload: unknown;
  note: string;
  at: string;
}

// RunEventEntry is one timeline entry: a node state transition or a debug.
interface RunEventEntry {
  node_id: string;
  event: string; // node_state or debug.
  state: string;
  attempt: number;
  handled: boolean;
  error: string;
  at: string;
}

interface RunStateData {
  run: RunInfo | null;
  nodeStates: Record<string, NodeRunState>;
  debugs: DebugEntry[];
  events: RunEventEntry[];
  starting: boolean;
  loaded: boolean;
  waits: PendingWait[];
}

const emptyState: RunStateData = {
  run: null,
  nodeStates: {},
  debugs: [],
  events: [],
  starting: false,
  loaded: false,
  waits: [],
};

// Shapes returned by GET /flows/{id}/run.
interface LastRunResponse {
  run: {
    id: string;
    status: string;
    started_at: string;
    ended_at?: string | null;
    error?: string;
    had_failures?: boolean;
    trigger_node_id?: string;
  };
  events: Array<{
    node_id: string;
    attempt: number;
    event: string;
    state?: string;
    payload?: string;
    at: string;
  }> | null;
  node_states: Record<string, { state: string; attempt: number; handled?: boolean; error?: string }> | null;
}

function createRunStateStore() {
  const store = writable<RunStateData>({ ...emptyState });
  const { subscribe, update } = store;

  let flowId: string | null = null;
  let unsubFlowTopic: (() => void) | null = null;
  let unsubRunTopic: (() => void) | null = null;
  let unsubConnection: (() => void) | null = null;
  // currentRunId gates the wildcard run topic to the run being followed.
  let currentRunId: string | null = null;
  // runStarts counts the new runs taken from notifications, so a last-run
  // read can tell whether one started while it was in flight.
  let runStarts = 0;
  // While a last-run read is in flight, run events are also kept here. The
  // read is a snapshot from when the server answered, so events that arrived
  // meanwhile are applied over it again.
  let loads = 0;
  let held: { data: unknown; topic: string }[] = [];

  // toRunInfo normalizes a run row / WS run payload into RunInfo.
  function toRunInfo(r: any): RunInfo {
    return {
      id: r.id,
      status: r.status,
      started_at: r.started_at ?? null,
      ended_at: r.ended_at ?? null,
      error: r.error ?? '',
      had_failures: !!r.had_failures,
      trigger_node_id: r.trigger_node_id,
      resumed_from_run_id: r.resumed_from_run_id,
    };
  }

  // loadWaits refreshes the flow's pending waits. Runs arm and finish waits,
  // so every run notification refreshes them; a failure keeps the last list.
  async function loadWaits(id: string) {
    try {
      const res = await api.flows.waits<{ waits: PendingWait[] }>(id);
      if (flowId === id) update((s) => ({ ...s, waits: res.waits ?? [] }));
    } catch {
      // Permission to read runs is needed; without it the list stays empty.
    }
  }

  // handleRunEvent applies one message from the wildcard run topic. Events
  // for runs other than the one being followed are ignored.
  function handleRunEvent(data: unknown, topic: string) {
    if (loads > 0) held.push({ data, topic });
    if (!currentRunId || topic !== `run:${currentRunId}`) return;
    applyRunEvent(data);
  }

  // applyRunEvent applies one event of the run being followed.
  function applyRunEvent(data: unknown) {
    const msg = data as any;
    if (!msg || typeof msg !== 'object') return;
    switch (msg.event) {
      case 'node_state': {
        const ns: NodeRunState = {
          state: msg.state,
          attempt: msg.attempt ?? 0,
          handled: !!msg.handled,
          error: msg.error ?? '',
        };
        update((s) => ({
          ...s,
          nodeStates: { ...s.nodeStates, [msg.node_id]: ns },
          events: [
            ...s.events,
            {
              node_id: msg.node_id,
              event: 'node_state',
              state: msg.state,
              attempt: msg.attempt ?? 0,
              handled: !!msg.handled,
              error: msg.error ?? '',
              at: msg.at ?? '',
            },
          ],
        }));
        break;
      }
      case 'debug': {
        const entry: DebugEntry = {
          node_id: msg.node_id,
          payload: msg.payload,
          note: msg.note ?? '',
          at: msg.at ?? '',
        };
        update((s) => ({
          ...s,
          debugs: [...s.debugs, entry],
          events: [
            ...s.events,
            { node_id: msg.node_id, event: 'debug', state: '', attempt: 0, handled: false, error: '', at: msg.at ?? '' },
          ],
        }));
        break;
      }
      case 'run_finished': {
        // Provisional finish from the run topic; the flow topic message
        // carries the authoritative run payload and overwrites it.
        update((s) =>
          s.run
            ? {
                ...s,
                run: {
                  ...s.run,
                  status: msg.status ?? s.run.status,
                  error: msg.error ?? s.run.error,
                  ended_at: s.run.ended_at ?? new Date().toISOString(),
                },
              }
            : s
        );
        break;
      }
    }
  }

  // applyRunStarted resets per-node state for a new run and follows its
  // events. A duplicate notification for the current run only refreshes the
  // run info.
  function applyRunStarted(run: RunInfo) {
    const current = get(store);
    if (current.run?.id === run.id) {
      update((s) => ({ ...s, run }));
    } else {
      update((s) => ({ ...s, run, nodeStates: {}, debugs: [], events: [] }));
      runStarts += 1;
    }
    currentRunId = run.id;
  }

  // handleFlowEvent applies one message from the flow:{id}:runs topic.
  function handleFlowEvent(data: unknown) {
    const msg = data as any;
    if (!msg || typeof msg !== 'object' || !msg.run) return;
    switch (msg.event) {
      case 'run_started':
        applyRunStarted(toRunInfo(msg.run));
        break;
      case 'run_finished':
        update((s) => (s.run?.id === msg.run.id || !s.run ? { ...s, run: toRunInfo(msg.run) } : s));
        break;
    }
    if (flowId) void loadWaits(flowId);
  }

  return {
    subscribe,

    // open wires the store to a flow: live run notifications plus the last
    // persisted run for the initial overlay.
    open(id: string) {
      this.close();
      flowId = id;
      unsubFlowTopic = websocket.subscribeTopic(`flow:${id}:runs`, handleFlowEvent);
      // The wildcard run subscription is held for the whole editor session so
      // no events are lost between run start and a per-run subscribe.
      unsubRunTopic = websocket.subscribeTopic('run:*', handleRunEvent);
      void this.loadLastRun(id);
      void loadWaits(id);
      // Notifications sent while the socket was down are never delivered, so
      // a reconnect reloads what they would have changed. Without it a
      // run_finished lost in the gap leaves the run showing as running.
      let connected = get(websocket).connected;
      unsubConnection = websocket.subscribe((ws) => {
        if (ws.connected && !connected) {
          void this.loadLastRun(id);
          void loadWaits(id);
        }
        connected = ws.connected;
      });
    },

    // cancelWait ends one pending wait without delivering it.
    async cancelWait(nodeId: string, key: string) {
      if (!flowId) return;
      const id = flowId;
      await api.flows.cancelWait(id, nodeId, key);
      await loadWaits(id);
    },

    // close unsubscribes all topics and resets state when leaving the editor.
    close() {
      unsubFlowTopic?.();
      unsubFlowTopic = null;
      unsubRunTopic?.();
      unsubRunTopic = null;
      unsubConnection?.();
      unsubConnection = null;
      currentRunId = null;
      flowId = null;
      held = [];
      store.set({ ...emptyState });
    },

    // loadLastRun fetches the flow's persisted run; a missing run is not an error.
    async loadLastRun(id: string) {
      const starts = runStarts;
      loads += 1;
      try {
        const detail = await api.flows.lastRun<LastRunResponse>(id);
        // The editor may have moved to another flow while this was loading.
        if (flowId !== id) return;
        // A run that started while this was loading is newer than the answer
        // and is already being followed from its first event.
        if (runStarts !== starts) {
          update((s) => ({ ...s, loaded: true }));
          return;
        }
        const run = toRunInfo(detail.run);

        // Rebuild per-node states, debug entries, and the timeline from events.
        const nodeStates: Record<string, NodeRunState> = {};
        for (const [nodeId, ns] of Object.entries(detail.node_states ?? {})) {
          nodeStates[nodeId] = {
            state: ns.state,
            attempt: ns.attempt,
            handled: !!ns.handled,
            error: ns.error ?? '',
          };
        }
        const debugs: DebugEntry[] = [];
        const events: RunEventEntry[] = [];
        for (const ev of detail.events ?? []) {
          if (ev.event === 'debug') {
            let payload: unknown = undefined;
            let note = '';
            if (ev.payload) {
              try {
                const parsed = JSON.parse(ev.payload);
                payload = parsed.payload;
                note = parsed.note ?? '';
              } catch {
                payload = ev.payload;
              }
            }
            debugs.push({ node_id: ev.node_id, payload, note, at: ev.at });
            events.push({ node_id: ev.node_id, event: 'debug', state: '', attempt: 0, handled: false, error: '', at: ev.at });
          } else if (ev.event === 'node_state') {
            let error = '';
            let handled = false;
            if (ev.payload) {
              try {
                const parsed = JSON.parse(ev.payload);
                error = parsed.error ?? '';
                handled = !!parsed.handled;
              } catch {
                // Ignore malformed payloads.
              }
            }
            events.push({
              node_id: ev.node_id,
              event: 'node_state',
              state: ev.state ?? '',
              attempt: ev.attempt,
              handled,
              error,
              at: ev.at,
            });
          }
        }

        update((s) => ({ ...s, run, nodeStates, debugs, events, loaded: true }));

        // Follow a run that is still executing after a page reload.
        if (run.status === 'running') currentRunId = run.id;

        // Apply what arrived for this run while the read was in flight,
        // except the events the snapshot already holds.
        const stamp = (ev: { event: string; node_id: string; attempt: number; state: string; at: string }) =>
          `${ev.event}|${ev.node_id}|${ev.attempt}|${ev.state}|${Date.parse(ev.at)}`;
        const known = new Set(events.map(stamp));
        for (const h of held) {
          if (h.topic !== `run:${run.id}`) continue;
          const msg = h.data as any;
          if (msg?.event === 'node_state' || msg?.event === 'debug') {
            const node = msg.event === 'node_state';
            const key = stamp({
              event: msg.event,
              node_id: msg.node_id,
              attempt: node ? (msg.attempt ?? 0) : 0,
              state: node ? msg.state : '',
              at: msg.at ?? '',
            });
            if (known.has(key)) continue;
          }
          applyRunEvent(msg);
        }
      } catch (e: any) {
        if (e?.code === 'NOT_FOUND' && flowId === id && runStarts === starts) {
          update((s) => ({ ...s, run: null, nodeStates: {}, debugs: [], events: [], loaded: true }));
        }
        // Other errors keep whatever state exists; the editor stays usable.
      } finally {
        loads -= 1;
        if (loads === 0) held = [];
      }
    },

    // startTestRun kicks off a test run of the draft graph — full, or partial
    // from startNodeId seeded by the last run. Errors are rethrown so the
    // toolbar can surface them.
    async startTestRun(
      id: string,
      opts?: { triggerNodeId?: string; startNodeId?: string; payload?: unknown; topic?: string },
    ) {
      update((s) => ({ ...s, starting: true }));
      try {
        const res = await api.flows.test<{ run_id: string }>(id, opts);
        // The WS run_started may have arrived first with authoritative data;
        // only seed a placeholder when it has not.
        if (get(store).run?.id !== res.run_id) {
          applyRunStarted({
            id: res.run_id,
            status: 'running',
            started_at: new Date().toISOString(),
            ended_at: null,
            error: '',
            had_failures: false,
            trigger_node_id: opts?.triggerNodeId,
          });
        }
        return res.run_id;
      } finally {
        update((s) => ({ ...s, starting: false }));
      }
    },

    // cancelRun cancels the current running run, if any.
    async cancelRun() {
      const run = get(store).run;
      if (!run || run.status !== 'running') return;
      await api.runs.cancel(run.id);
    },
  };
}

export const runState = createRunStateStore();
export type { RunInfo, NodeRunState, DebugEntry, RunEventEntry };
