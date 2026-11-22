import { describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

// The API client reads localStorage when it is built, which the test runtime
// lacks, so the store runs against a stand-in for the flow endpoints. Each
// saveGraph call is held until the test resolves it, which is how a slow PUT
// finishes after the editor has moved to another flow. vi.mock is hoisted
// above the imports.
const calls = vi.hoisted(() => ({
  saves: [] as { id: string; resolve: (v: unknown) => void }[],
}));
vi.mock('../api/client', () => ({
  api: {
    flows: {
      get: async (id: string) => ({ id, draft_revision: 1, graph: { nodes: [], edges: [] } }),
      saveGraph: (id: string) =>
        new Promise((resolve) => {
          calls.saves.push({ id, resolve });
        }),
    },
  },
}));

import { editor } from './editor';

// The editor store is one singleton that reset and load re-point. A save of
// flow A that resolved after flow B was loaded wrote A back into the store,
// so the next autosave sent B's canvas to A's graph with A's revision.
describe('editor.saveGraph', () => {
  it('leaves a flow loaded during the request in place', async () => {
    const graph = { nodes: [], edges: [] } as any;
    await editor.load('A');
    const pending = editor.saveGraph(graph);
    editor.reset();
    await editor.load('B');
    calls.saves[0].resolve({ id: 'A', draft_revision: 2, graph });
    await pending;

    expect(get(editor).flow?.id).toBe('B');
    void editor.saveGraph(graph);
    expect(calls.saves[1].id).toBe('B');
  });
});
