import { describe, expect, it, vi } from 'vitest';

// The API client reads localStorage when it is built and the socket store
// opens a WebSocket, neither of which the test runtime has, so both are
// stand-ins: each read resolves when the test says so, and topic handlers are
// captured so the test can push a change. vi.mock is hoisted above the
// imports.
const io = vi.hoisted(() => ({
  reads: new Map<string, (res: unknown) => void>(),
  topics: new Map<string, (data: unknown, topic: string) => void>(),
}));
vi.mock('./api/client', () => ({
  api: { get: (path: string) => new Promise((resolve) => io.reads.set(path, resolve)) },
}));
vi.mock('./stores/websocket', () => ({
  websocket: {
    subscribeTopic: (topic: string, handler: (data: unknown, topic: string) => void) => {
      io.topics.set(topic, handler);
      return () => io.topics.delete(topic);
    },
    subscribe: (run: (state: { connected: boolean }) => void) => {
      run({ connected: true });
      return () => {};
    },
  },
}));

import { watchConditionSources, watchLive } from './dashboardLive';
import { conditionsMet } from './displayState';
import type { Condition, LiveState } from './stores/dashboards';

// A read of the live state is a snapshot from when the server answered. A
// command published while the answer was in flight is newer, and the answer
// was applied over it regardless of its sequence number: the viewer went back
// to the page the command had just left and stayed there until the next one.
describe('watchLive', () => {
  it('keeps a pushed state over an older read that lands after it', async () => {
    const seen: LiveState[] = [];
    const stop = watchLive('d1', (state) => seen.push(state));

    io.topics.get('dashboard:d1:live')!({ state: { seq: 6, page: 'second' }, event: 'page' }, 'dashboard:d1:live');
    [...io.reads.values()][0]({ seq: 5, page: 'first' });
    await Promise.resolve();
    await Promise.resolve();

    expect(seen[seen.length - 1].page).toBe('second');
    stop();
  });
});

// The list reads that seed display conditions are snapshots as well. Home
// Assistant's state list is large and slow, and a door that opened while it
// was in flight went back to closed: a placement shown while the door is open
// stayed hidden until the door moved again.
describe('watchConditionSources', () => {
  it('keeps a pushed record over an older list that lands after it', async () => {
    const door: Condition = { kind: 'state', source: 'ha', connectionId: 'c1', entityId: 'binary_sensor.door', operator: 'equals', value: 'on' };
    const window: Condition = { kind: 'state', source: 'ha', connectionId: 'c1', entityId: 'binary_sensor.window', operator: 'equals', value: 'on' };
    let records: Record<string, unknown> = {};
    const stop = watchConditionSources([door, window], (next) => (records = next));

    io.topics.get('ha:c1:state:binary_sensor.door')!({ new_state: { entity_id: 'binary_sensor.door', state: 'on' } }, 'ha:c1:state:binary_sensor.door');
    io.reads.get('/connections/c1/ha/states')!([
      { entity_id: 'binary_sensor.door', state: 'off' },
      { entity_id: 'binary_sensor.window', state: 'on' },
    ]);
    await Promise.resolve();
    await Promise.resolve();

    expect(conditionsMet({ conditions: [door] }, records, new Date()), 'the pushed open door outlives the older list').toBe(true);
    expect(conditionsMet({ conditions: [window] }, records, new Date()), 'an entity nothing was pushed for is seeded from the list').toBe(true);
    stop();
  });
});
