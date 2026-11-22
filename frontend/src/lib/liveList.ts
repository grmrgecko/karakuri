import { get } from 'svelte/store';
import { websocket } from './stores/websocket';

// Coalescing bounds for watchLists: a burst of notices waits for quiet, a
// steady stream still refreshes within MAX_WAIT_MS, and refreshes are at least
// MIN_GAP_MS apart, so a flow that runs every second does not reload a page
// every second.
const QUIET_MS = 300;
const MAX_WAIT_MS = 2000;
const MIN_GAP_MS = 2000;

// ListTopic is a topic to watch, with an optional test of which of its
// messages call for a refresh.
export type ListTopic = string | { topic: string; when: (data: unknown) => boolean };

// watchLists calls refresh when any of the topics announces a change, and
// again after the socket reconnects, since notices sent while it was down are
// lost. Notices that arrive together cause one refresh. It returns a function
// that stops watching.
export function watchLists(topics: ListTopic[], refresh: () => void): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let firstAt = 0;
  let lastAt = 0;

  const fire = () => {
    timer = null;
    firstAt = 0;
    lastAt = Date.now();
    refresh();
  };
  const schedule = () => {
    const now = Date.now();
    if (!firstAt) firstAt = now;
    if (timer) clearTimeout(timer);
    // A burst that never goes quiet still refreshes once MAX_WAIT_MS passes.
    const wait = Math.max(0, Math.min(QUIET_MS, firstAt + MAX_WAIT_MS - now), lastAt + MIN_GAP_MS - now);
    timer = setTimeout(fire, wait);
  };

  const offs = topics.map((t) => typeof t === 'string'
    ? websocket.subscribeTopic(t, schedule)
    : websocket.subscribeTopic(t.topic, (data) => { if (t.when(data)) schedule(); }));

  let wasConnected = get(websocket).connected;
  offs.push(websocket.subscribe((ws) => {
    if (ws.connected && !wasConnected) schedule();
    wasConnected = ws.connected;
  }));

  return () => {
    if (timer) clearTimeout(timer);
    for (const off of offs) off();
  };
}

// RunEvent is a run_started or run_finished message from the runs topic.
export interface RunEvent {
  event: 'run_started' | 'run_finished';
  run: {
    id: string;
    flow_id: string;
    status: string;
    started_at: string;
    ended_at?: string | null;
    had_failures?: boolean;
  };
}

// watchRuns follows every run the user may read as it starts and finishes.
export function watchRuns(handler: (ev: RunEvent) => void): () => void {
  return websocket.subscribeTopic('runs', (data) => {
    const ev = data as RunEvent;
    if (ev?.run?.flow_id) handler(ev);
  });
}
