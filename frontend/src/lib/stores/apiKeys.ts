import { api } from '../api/client';

export interface APIKey {
  id: number;
  name: string;
  prefix: string;
  expires_at: string | null;
  last_used: string | null;
  created_at: string;
  /** limited keys hold only `permissions` that their user's roles also grant. */
  limited: boolean;
  permissions?: string[];
  /** dashboard_id makes a render key, which opens only that dashboard's render URLs. */
  dashboard_id?: string;
  dashboard_name?: string;
}

export interface CreateAPIKeyResponse extends APIKey {
  key: string;
}

/** NewAPIKey is what a key is created with; permissions limit it when present. */
export interface NewAPIKey {
  name: string;
  expiresAt?: string;
  permissions?: string[];
  dashboardId?: string;
}

function createAPIKeyStore() {
  return {
    async list(dashboardId?: string): Promise<APIKey[]> {
      return api.apiKeys.list<APIKey[]>(dashboardId);
    },

    async create(key: NewAPIKey): Promise<CreateAPIKeyResponse> {
      const body: { name: string; expires_at?: string; permissions?: string[]; dashboard_id?: string } = {
        name: key.name,
      };
      if (key.expiresAt) body.expires_at = key.expiresAt;
      if (key.permissions) body.permissions = key.permissions;
      if (key.dashboardId) body.dashboard_id = key.dashboardId;
      return api.apiKeys.create<CreateAPIKeyResponse>(body);
    },

    /** value returns a render key's bearer value; other keys are shown only once. */
    async value(id: number): Promise<string> {
      const res = await api.apiKeys.value<{ key: string }>(id);
      return res.key;
    },

    /** permissions lists the registered permissions the caller may give a limited key. */
    async permissions(): Promise<string[]> {
      return api.apiKeys.permissions<string[]>();
    },

    async remove(id: number): Promise<void> {
      await api.apiKeys.remove(id);
    },
  };
}

export const apiKeyStore = createAPIKeyStore();
