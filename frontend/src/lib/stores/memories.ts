import { writable } from 'svelte/store';
import { api } from '../api/client';

// A memory store is a named bank the AI nodes remember into. The settings
// panel defines them and the node editor's memory-store picker binds them,
// the same split controls have.

export interface MemoryStore {
  id: string;
  name: string;
  description: string;
  embed_connection: string;
  embed_model: string;
  half_life_days: number;
  recency_weight: number;
  max_session_turns: number;
  max_memories: number;
  facts: number;
  turns: number;
  sessions: number;
  pending: number;
  created_at: string;
  updated_at: string;
}

export interface Memory {
  id: string;
  store_id: string;
  kind: string;
  session?: string;
  role?: string;
  text: string;
  tags?: string[];
  importance: number;
  embedded: boolean;
  expires_at?: string;
  created_at: string;
  score?: number;
}

export interface MemorySession {
  session: string;
  turns: number;
  last_at: string;
}

interface MemoriesState {
  loaded: boolean;
  stores: MemoryStore[];
}

function createMemoryStoresStore() {
  const store = writable<MemoriesState>({ loaded: false, stores: [] });
  let loadPromise: Promise<void> | null = null;

  async function refresh(): Promise<MemoryStore[]> {
    const list = (await api.memories.listStores<MemoryStore[]>()) || [];
    store.update((s) => ({ ...s, loaded: true, stores: list }));
    return list;
  }

  return {
    subscribe: store.subscribe,

    // load fetches the stores once; concurrent calls share one request.
    async load() {
      if (loadPromise) return loadPromise;
      loadPromise = refresh()
        .then(() => {})
        .catch((err) => {
          loadPromise = null;
          throw err;
        });
      return loadPromise;
    },

    refresh,

    async create(body: Record<string, unknown>): Promise<MemoryStore> {
      const created = await api.memories.createStore<MemoryStore>(body);
      await refresh();
      return created;
    },

    async update(id: string, body: Record<string, unknown>): Promise<MemoryStore> {
      const updated = await api.memories.updateStore<MemoryStore>(id, body);
      await refresh();
      return updated;
    },

    async remove(id: string): Promise<void> {
      await api.memories.removeStore(id);
      await refresh();
    },
  };
}

export const memoryStoresStore = createMemoryStoresStore();
