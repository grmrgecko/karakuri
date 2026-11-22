import { writable, get } from 'svelte/store';
import { api } from '../api/client';
import type { FlowRecord, GraphDoc } from './flows';

// SubflowGraphEntry caches one shared subflow's name and graph for
// client-side port derivation and the read-only drill-in view.
interface SubflowGraphEntry {
  name: string;
  graph: GraphDoc;
}

interface SubflowLibraryState {
  records: FlowRecord[];
  graphs: Record<string, SubflowGraphEntry>;
  loaded: boolean;
}

// createSubflowLibrary holds the shared subflow library: the list of
// is_subflow flows for the palette, plus a graph cache keyed by flow ID used
// to derive subflow node ports on the canvas.
function createSubflowLibrary() {
  const store = writable<SubflowLibraryState>({ records: [], graphs: {}, loaded: false });
  const pending = new Map<string, Promise<SubflowGraphEntry | null>>();

  async function fetchGraph(id: string): Promise<SubflowGraphEntry | null> {
    try {
      const detail = await api.flows.get<{ name: string; graph: GraphDoc }>(id);
      const entry = { name: detail.name, graph: detail.graph };
      store.update((s) => ({ ...s, graphs: { ...s.graphs, [id]: entry } }));
      return entry;
    } catch {
      return null;
    } finally {
      pending.delete(id);
    }
  }

  return {
    subscribe: store.subscribe,

    // load refreshes the shared subflow list, following pagination to the
    // end: the palette lists every subflow with no pager.
    async load() {
      try {
        const records: FlowRecord[] = [];
        let page = 1;
        for (;;) {
          const result = await api.flows.list<{ data: FlowRecord[]; next?: number }>(true, 100, page);
          records.push(...(result.data || []));
          if (!result.next) break;
          page = result.next;
        }
        store.update((s) => ({ ...s, records, loaded: true }));
      } catch {
        // The palette simply shows no subflows on failure.
      }
    },

    // ensureGraph lazily fetches a shared subflow's graph into the cache.
    ensureGraph(id: string) {
      if (get(store).graphs[id] || pending.has(id)) return;
      pending.set(id, fetchGraph(id));
    },

    // getGraph returns the cached entry, fetching it when absent.
    async getGraph(id: string): Promise<SubflowGraphEntry | null> {
      const cached = get(store).graphs[id];
      if (cached) return cached;
      const inflight = pending.get(id);
      if (inflight) return inflight;
      const p = fetchGraph(id);
      pending.set(id, p);
      return p;
    },

    // invalidate drops a cached graph so the next use refetches it.
    invalidate(id: string) {
      store.update((s) => {
        if (!s.graphs[id]) return s;
        const graphs = { ...s.graphs };
        delete graphs[id];
        return { ...s, graphs };
      });
    },
  };
}

export const subflowLibrary = createSubflowLibrary();
export type { SubflowGraphEntry };
