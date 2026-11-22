import { writable } from 'svelte/store';
import { api } from '../api/client';
import { scriptSdkCatalog } from './scriptSdkCatalog';

// PluginState mirrors the API's PluginItem: what a plugin is, whether its
// contributions are applied, and what those contributions are.
interface PluginState {
  name: string;
  version?: string;
  description?: string;
  author?: string;
  builtin: boolean;
  enabled: boolean;
  status: 'loaded' | 'unloaded' | 'error';
  error_message?: string;
  has_process: boolean;
  has_settings: boolean;
  widgets?: string[];
  nodes?: string[];
  kinds?: string[];
}

// PluginSettings pairs the plugin's JSON Schema with its stored values, so the
// form renders and populates from one request.
interface PluginSettings {
  schema: string;
  values: Record<string, string>;
}

function refreshScriptFunctions() {
  void scriptSdkCatalog.refresh().catch(() => {});
}

function createPluginStore() {
  const { subscribe, update } = writable<{
    plugins: PluginState[];
    loading: boolean;
  }>({
    plugins: [],
    loading: false,
  });

  return {
    subscribe,

    async list() {
      update((s) => ({ ...s, loading: true }));
      try {
        const data = await api.get<PluginState[]>('/plugins');
        update((s) => ({ ...s, plugins: data || [], loading: false }));
      } catch {
        update((s) => ({ ...s, loading: false }));
      }
    },

    async load(name: string) {
      await api.post(`/plugins/${name}/load`);
      refreshScriptFunctions();
    },

    async unload(name: string) {
      await api.post(`/plugins/${name}/unload`);
      refreshScriptFunctions();
    },

    async reload(name: string) {
      await api.post(`/plugins/${name}/reload`);
      refreshScriptFunctions();
    },

    async enable(name: string) {
      await api.post(`/plugins/${name}/enable`);
      refreshScriptFunctions();
    },

    async disable(name: string) {
      await api.post(`/plugins/${name}/disable`);
      refreshScriptFunctions();
    },

    async getSettings(name: string): Promise<PluginSettings> {
      return api.get<PluginSettings>(`/plugins/${name}/settings`);
    },

    async updateSettings(name: string, values: Record<string, string>) {
      await api.put(`/plugins/${name}/settings`, { values });
    },
  };
}

export const pluginStore = createPluginStore();
export type { PluginState, PluginSettings };
