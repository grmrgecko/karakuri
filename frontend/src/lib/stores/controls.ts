import { writable, get } from 'svelte/store';
import { api } from '../api/client';
import type { FieldSpec } from './nodeCatalog';

// A control is a value karakuri owns rather than observes: a toggle, a number,
// a dropdown, a button. The settings panel defines them, the node editor binds
// them, and dashboard rows operate them.

export interface Control {
  id: string;
  name: string;
  kind: string;
  icon: string;
  options: Record<string, unknown>;
  value: unknown;
  allow_public: boolean;
  updated_at: string;
}

// ControlAttribute is one addressable part of a kind's value: a light's
// brightness, a cover's position. A scalar kind has exactly one.
export interface ControlAttribute {
  id: string;
  label: string;
  type: string;
  primary?: boolean;
  read_only?: boolean;
  // write_only marks an action, such as a player's skip, that is announced
  // and never held in the value.
  write_only?: boolean;
  requires_option?: string;
}

// ControlKind is served by the backend, settings schema included, so a new kind
// renders here with no frontend change. MQTT devices pick from the same list,
// which is why the catalog is not gated on control permissions.
export interface ControlKind {
  name: string;
  label: string;
  description: string;
  icon: string;
  value_type: string;
  attributes: ControlAttribute[];
  options: FieldSpec[];
  press_only?: boolean;
}

interface ControlsState {
  loaded: boolean;
  controls: Control[];
  kinds: ControlKind[];
}

function createControlsStore() {
  const store = writable<ControlsState>({ loaded: false, controls: [], kinds: [] });
  let loadPromise: Promise<void> | null = null;

  async function refresh(): Promise<Control[]> {
    const list = (await api.controls.list<Control[]>()) || [];
    store.update((s) => ({ ...s, loaded: true, controls: list }));
    return list;
  }

  return {
    subscribe: store.subscribe,

    // load fetches the controls once; concurrent calls share one request. The
    // kind catalog is fetched alongside but is optional: a viewer without
    // control write access still needs the list to render rows.
    async load() {
      if (loadPromise) return loadPromise;
      loadPromise = (async () => {
        await refresh();
        try {
          const kinds = (await api.controls.kinds<ControlKind[]>()) || [];
          store.update((s) => ({ ...s, kinds }));
        } catch {
          // The catalog is optional here: without it composite kinds lose
          // their attribute pickers but the list still renders.
        }
      })().catch((err) => {
        loadPromise = null;
        throw err;
      });
      return loadPromise;
    },

    refresh,

    // byId returns a cached control, for a picker resolving a stored ID.
    byId(id: string): Control | undefined {
      return get(store).controls.find((c) => c.id === id);
    },

    // applyValue folds a live change from the broker into the cached list, so
    // one open page shows what another page just set.
    applyValue(id: string, value: unknown, updatedAt: string) {
      store.update((s) => ({
        ...s,
        controls: s.controls.map((c) =>
          c.id === id ? { ...c, value, updated_at: updatedAt } : c,
        ),
      }));
    },

    async create(body: {
      name: string;
      kind: string;
      icon?: string;
      options?: Record<string, unknown>;
      allow_public?: boolean;
    }): Promise<Control> {
      const created = await api.controls.create<Control>(body);
      await refresh();
      return created;
    },

    async update(
      id: string,
      body: {
        name: string;
        kind: string;
        icon?: string;
        options?: Record<string, unknown>;
        allow_public?: boolean;
      },
    ): Promise<Control> {
      const updated = await api.controls.update<Control>(id, body);
      await refresh();
      return updated;
    },

    async remove(id: string): Promise<void> {
      await api.controls.remove(id);
      await refresh();
    },

    // applyDeleted drops a control another session removed, announced on the
    // control's own topic, so the list does not offer a control that is gone.
    applyDeleted(id: string) {
      store.update((s) => ({ ...s, controls: s.controls.filter((c) => c.id !== id) }));
    },

    // set writes a value and folds the result back in, so the panel reflects
    // what the server actually stored rather than what was asked for. An
    // attribute names one part of a composite control, leaving the rest alone.
    async set(id: string, value: unknown, attribute?: string): Promise<Control> {
      const next = await api.controls.set<Control>(id, value, attribute);
      store.update((s) => ({
        ...s,
        controls: s.controls.map((c) => (c.id === id ? next : c)),
      }));
      return next;
    },
  };
}

export const controlsStore = createControlsStore();
