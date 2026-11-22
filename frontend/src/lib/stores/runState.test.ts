import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

// The API client reads localStorage when it is built and the socket store
// opens a WebSocket, neither of which the test runtime has, so both are
// stand-ins: the last-run read resolves when the test says so, topic handlers
// are captured so the test can deliver notifications, and the connection
// listener is captured so the test can reconnect. vi.mock is hoisted above
// the imports.
const io = vi.hoisted(() => ({
  lastRun: [] as ((detail: unknown) => void)[],
  topics: new Map<string, (data: unknown, topic: string) => void>(),
  connection: null as null | ((state: { connected: boolean }) => void),
}));
vi.mock('../api/client', () => ({
  api: {
    flows: {
      lastRun: () => new Promise((resolve) => io.lastRun.push(resolve)),
      waits: async () => ({ waits: [] }),
    },
  },
}));
vi.mock('./websocket', () => ({
  websocket: {
    subscribeTopic: (topic: string, handler: (data: unknown, topic: string) => void) => {
      io.topics.set(topic, handler);
      return () => io.topics.delete(topic);
    },
    subscribe: (run: (state: { connected: boolean }) => void) => {
      io.connection = run;
      run({ connected: true });
      return () => {};
    },
  },
}));

import { runState } from './runState';

// The last-run read is a snapshot from when the server answered. A run that
// started while the answer was in flight is newer, and the answer replaced
// it: the toolbar showed the earlier run, the new run's node states were
// drawn over that run's, and its run_finished was dropped as another run's.
describe('runState.loadLastRun', () => {
  it('keeps a run that started while the last run was loading', async () => {
    runState.open('f1');
    const flowTopic = io.topics.get('flow:f1:runs')!;
    const runTopic = io.topics.get('run:*')!;

    flowTopic({ event: 'run_started', run: { id: 'new', status: 'running', started_at: '2026-01-01T00:00:01Z' } }, 'flow:f1:runs');
    io.lastRun[0]({
      run: { id: 'old', status: 'succeeded', started_at: '2026-01-01T00:00:00Z', ended_at: '2026-01-01T00:00:00Z' },
      events: [],
      node_states: { n1: { state: 'succeeded', attempt: 1 } },
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(get(runState).run?.id).toBe('new');
    expect(get(runState).nodeStates).toEqual({});

    runTopic({ event: 'node_state', node_id: 'n2', state: 'succeeded', attempt: 1 }, 'run:new');
    flowTopic({ event: 'run_finished', run: { id: 'new', status: 'succeeded', started_at: '2026-01-01T00:00:01Z', ended_at: '2026-01-01T00:00:02Z' } }, 'flow:f1:runs');
    expect(get(runState).run?.status).toBe('succeeded');
    expect(Object.keys(get(runState).nodeStates)).toEqual(['n2']);
    runState.close();
  });

  // A node event of the followed run that arrives while the answer is in
  // flight is newer than the answer too. The answer replaced the node states
  // wholesale: a node that had finished went back to running and stayed
  // there, since its finish is never sent again. The reconnect reload is the
  // case below; n2's event is in both the answer and the socket, and is
  // listed once.
  it('keeps node events that arrived while the same run was reloading', async () => {
    runState.open('f2');
    const first = io.lastRun.length - 1;
    const flowTopic = io.topics.get('flow:f2:runs')!;
    const runTopic = io.topics.get('run:*')!;
    const run = { id: 'r1', status: 'running', started_at: '2026-01-01T00:00:00Z' };
    io.lastRun[first]({ run, events: [], node_states: {} });
    await Promise.resolve();
    await Promise.resolve();
    flowTopic({ event: 'run_started', run }, 'flow:f2:runs');
    runTopic({ event: 'node_state', node_id: 'n1', state: 'running', attempt: 1, at: '2026-01-01T00:00:01Z' }, 'run:r1');

    io.connection!({ connected: false });
    io.connection!({ connected: true });
    runTopic({ event: 'node_state', node_id: 'n2', state: 'succeeded', attempt: 1, at: '2026-01-01T00:00:02Z' }, 'run:r1');
    runTopic({ event: 'node_state', node_id: 'n1', state: 'succeeded', attempt: 1, at: '2026-01-01T00:00:03Z' }, 'run:r1');
    runTopic({ event: 'node_state', node_id: 'n9', state: 'failed', attempt: 1, at: '2026-01-01T00:00:03Z' }, 'run:other');
    io.lastRun[first + 1]({
      run,
      events: [
        { node_id: 'n1', attempt: 1, event: 'node_state', state: 'running', at: '2026-01-01T00:00:01Z' },
        { node_id: 'n2', attempt: 1, event: 'node_state', state: 'succeeded', at: '2026-01-01T00:00:02.000Z' },
      ],
      node_states: { n1: { state: 'running', attempt: 1 }, n2: { state: 'succeeded', attempt: 1 } },
    });
    await Promise.resolve();
    await Promise.resolve();

    const state = get(runState);
    expect(state.nodeStates.n1.state, 'the finish that arrived during the read outlives the older answer').toBe('succeeded');
    expect(state.nodeStates.n9, 'another run stays out').toBeUndefined();
    expect(
      state.events.map((e) => `${e.node_id} ${e.state}`),
      'an event the answer already holds is listed once',
    ).toEqual(['n1 running', 'n2 succeeded', 'n1 succeeded']);
    runState.close();
  });
});
