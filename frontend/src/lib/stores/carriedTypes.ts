import { writable } from 'svelte/store';

// Carried output types for the graph currently open in the editor, keyed by
// node ID and then port ID.
//
// A node that only routes or delays a message cannot say what it emits on its
// own — the answer is upstream — so it is resolved once over the whole graph
// (see utils/typeFlow.ts) rather than by each node component, which sees only
// itself. EditorShell owns the graph and fills this in; FlowNode reads its own
// entry to label its sockets, and the connection checks read it so the canvas
// refuses exactly the edges the server would.
//
// Only carried types live here. A port that declares its own type is already
// answered by nodePorts and never appears.
export type CarriedTypes = Record<string, Record<string, string>>;

function createCarriedTypesStore() {
  const store = writable<CarriedTypes>({});
  return {
    subscribe: store.subscribe,
    set: store.set,
    // clear drops the map when the editor closes or drills into another graph,
    // so a stale answer from the previous graph is never shown.
    clear: () => store.set({}),
  };
}

export const carriedTypes = createCarriedTypesStore();
