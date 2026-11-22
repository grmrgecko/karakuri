import { writable } from 'svelte/store';

// Test-run start targets for the graph currently open in the editor, keyed by
// node ID.
//
// A run always begins at a trigger, so the button that starts one belongs on
// the trigger node rather than on the toolbar. Whether a given node may start a
// run is a question about the whole graph — which nodes are triggers, whether
// any exist at all, whether the flow is a subflow — and a node component sees
// only itself. EditorShell resolves it for every node at once and leaves the
// answer here; FlowNode reads its own entry.
//
// The value is the reason the node cannot start a run right now, empty when it
// can. A node absent from the map is not a start candidate and shows no button
// at all.
export type TestTargets = Record<string, string>;

function createTestTargetsStore() {
  const store = writable<TestTargets>({});
  return {
    subscribe: store.subscribe,
    set: store.set,
  };
}

export const testTargets = createTestTargetsStore();
