import { writable, get } from 'svelte/store';
import { api } from '../api/client';

// PASSWORD_SENTINEL is what the backend returns in place of stored secrets.
// Sending it back on update preserves the stored value.
export const PASSWORD_SENTINEL = '••••••';

export interface ConnectionStatus {
  state: 'connecting' | 'connected' | 'error' | 'disabled';
  error?: string;
  since?: string;
}

export interface Connection {
  id: string;
  name: string;
  kind: string;
  enabled: boolean;
  config: Record<string, unknown>;
  status: ConnectionStatus;
  // access is the level the caller holds: use, edit, or admin.
  access?: 'use' | 'edit' | 'admin';
  owner_id?: string;
  created_at: string;
  updated_at: string;
}

export interface ConnectionTestResult {
  ok: boolean;
  error?: string;
  /** What a probing kind found, such as the calendars an account holds. */
  detail?: string;
}

export interface ConnectionFieldFilter {
  connection_kind?: string;
  connection_match?: Record<string, string>;
  connection_exclude?: Record<string, string>;
}

// connectionMatchesField applies a node field's kind and public-config rules.
export function connectionMatchesField(
  connection: Connection,
  field: ConnectionFieldFilter,
): boolean {
  if (field.connection_kind) {
    const kinds = field.connection_kind.split(',').map((kind) => kind.trim());
    if (!kinds.includes(connection.kind)) return false;
  }
  if (
    field.connection_match &&
    !Object.entries(field.connection_match).every(
      ([key, value]) => String(connection.config?.[key] ?? '') === value,
    )
  ) {
    return false;
  }
  if (
    field.connection_exclude &&
    Object.keys(field.connection_exclude).length > 0 &&
    Object.entries(field.connection_exclude).every(
      ([key, value]) => String(connection.config?.[key] ?? '') === value,
    )
  ) {
    return false;
  }
  return true;
}

interface ConnectionsState {
  loaded: boolean;
  connections: Connection[];
}

function createConnectionsStore() {
  const store = writable<ConnectionsState>({ loaded: false, connections: [] });
  let loadPromise: Promise<void> | null = null;

  async function refresh(): Promise<Connection[]> {
    const list = (await api.connections.list<Connection[]>()) || [];
    store.set({ loaded: true, connections: list });
    return list;
  }

  return {
    subscribe: store.subscribe,

    // load fetches the connection list once; concurrent calls share one request.
    async load() {
      if (loadPromise) return loadPromise;
      loadPromise = refresh()
        .then(() => undefined)
        .catch((err) => {
          loadPromise = null;
          throw err;
        });
      return loadPromise;
    },

    refresh,

    // byKind returns the cached connections of one driver kind.
    byKind(kind: string): Connection[] {
      const s = get(store);
      if (!kind) return s.connections;
      return s.connections.filter((c) => c.kind === kind);
    },

    // applyStatus folds a live WS status update into the cached list.
    applyStatus(connectionId: string, status: ConnectionStatus) {
      store.update((s) => ({
        ...s,
        connections: s.connections.map((c) =>
          c.id === connectionId ? { ...c, status } : c,
        ),
      }));
    },

    async create(body: {
      name: string;
      kind: string;
      config: Record<string, unknown>;
      enabled?: boolean;
    }): Promise<Connection> {
      const created = await api.connections.create<Connection>(body);
      await refresh();
      return created;
    },

    async update(
      id: string,
      body: { name?: string; config?: Record<string, unknown>; enabled?: boolean },
    ): Promise<Connection> {
      const updated = await api.connections.update<Connection>(id, body);
      await refresh();
      return updated;
    },

    async remove(id: string): Promise<void> {
      await api.connections.remove(id);
      await refresh();
    },

    async test(id: string): Promise<ConnectionTestResult> {
      return api.connections.test<ConnectionTestResult>(id);
    },
  };
}

export const connectionsStore = createConnectionsStore();
