import { get, writable } from 'svelte/store';
import { api } from '../api/client';
import type { NodeFunction } from './nodeCatalog';

export interface ScriptSdkCatalogState {
  loaded: boolean;
  loading: boolean;
  revision: string;
  functions: NodeFunction[];
  error: string;
}

const initialState: ScriptSdkCatalogState = {
  loaded: false,
  loading: false,
  revision: '',
  functions: [],
  error: '',
};

function createScriptSdkCatalogStore() {
  const store = writable<ScriptSdkCatalogState>(initialState);
  let inFlight: Promise<void> | null = null;
  let newestRequest = 0;

  // refresh replaces the snapshot only when its request is still the newest.
  // Plugin changes can arrive while an earlier catalog request is in flight.
  function refresh(): Promise<void> {
    const request = ++newestRequest;
    store.update((state) => ({ ...state, loading: true, error: '' }));
    const pending = (async () => {
      try {
        const catalog = await api.scripts.nodeFunctions();
        if (catalog.schema_version !== 1 || typeof catalog.revision !== 'string' || !Array.isArray(catalog.functions)) {
          throw new Error('the server returned an unsupported script function catalog');
        }
        if (request !== newestRequest) return;
        store.set({
          loaded: true,
          loading: false,
          revision: catalog.revision,
          functions: catalog.functions,
          error: '',
        });
      } catch (error: any) {
        if (request !== newestRequest) return;
        store.update((state) => ({
          ...state,
          loading: false,
          error: error?.message || 'could not load the script function catalog',
        }));
        throw error;
      }
    })();
    inFlight = pending;
    void pending.finally(() => {
      if (inFlight === pending) inFlight = null;
    }).catch(() => {});
    return pending;
  }

  return {
    subscribe: store.subscribe,

    // load is lazy. A loaded snapshot remains usable while a refresh runs.
    load(): Promise<void> {
      if (get(store).loaded) return Promise.resolve();
      return inFlight ?? refresh();
    },

    refresh,

    snapshot(): ScriptSdkCatalogState {
      return get(store);
    },
  };
}

export const scriptSdkCatalog = createScriptSdkCatalogStore();
