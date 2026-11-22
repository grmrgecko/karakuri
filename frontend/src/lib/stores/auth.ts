import { writable, derived, get } from 'svelte/store';
import { api } from '../api/client';
import { transientStatus } from '../api/status';

interface User {
  id: string;
  username: string;
  email: string;
  permissions: string[];
  // Client UI settings, persisted with the profile. The server stores the
  // blob without interpreting it, so keys live wherever they are read.
  preferences?: Record<string, unknown>;
}

interface AuthState {
  user: User | null;
  loading: boolean;
  initialized: boolean;
}

function matchPermission(held: string, required: string): boolean {
  const heldParts = held.split(':');
  const reqParts = required.split(':');

  for (let i = 0; i < reqParts.length; i++) {
    if (i >= heldParts.length) return false;
    if (heldParts[i] === '*') {
      if (i === heldParts.length - 1) return true;
      continue;
    }
    if (heldParts[i] !== reqParts[i]) return false;
  }

  return heldParts.length === reqParts.length;
}

function createAuthStore() {
  const { subscribe, set, update } = writable<AuthState>({
    user: null,
    loading: false,
    initialized: false,
  });

  return {
    subscribe,

    async init() {
      if (!api.getToken()) {
        set({ user: null, loading: false, initialized: true });
        return;
      }

      update((s) => ({ ...s, loading: true }));
      try {
        const user = await api.auth.me<User>();
        set({ user, loading: false, initialized: true });
      } catch (err: any) {
        // Only an answer from the server ends the session. A request that
        // never connected rejects without an API error code, or with the
        // client's NETWORK code from the refresh, and a server error or a
        // proxy's 502 during a restart carries a transient status. The
        // tokens are still good once the server is back.
        const answered =
          typeof err?.code === 'string' && err.code !== 'NETWORK' && !transientStatus(err.status);
        if (answered) {
          api.setToken(null);
          api.setRefreshToken(null);
        }
        set({ user: null, loading: false, initialized: true });
      }
    },

    async login(username: string, password: string) {
      update((s) => ({ ...s, loading: true }));
      try {
        const data = await api.auth.login<{
          access_token: string;
          refresh_token: string;
          user: User;
        }>(username, password);

        api.setToken(data.access_token);
        api.setRefreshToken(data.refresh_token);
        set({ user: data.user, loading: false, initialized: true });
        return data.user;
      } catch (err) {
        update((s) => ({ ...s, loading: false }));
        throw err;
      }
    },

    async logout() {
      try {
        await api.auth.logout();
      } catch {
        // Proceed even if the server-side logout fails.
      }
      api.setToken(null);
      api.setRefreshToken(null);
      set({ user: null, loading: false, initialized: true });
    },

    // setPreferences merges keys into the stored profile preferences. The
    // local copy updates first so the UI reacts immediately; a failed write
    // only costs the setting on the next login, so it is not surfaced.
    async setPreferences(patch: Record<string, unknown>) {
      update((s) =>
        s.user
          ? { ...s, user: { ...s.user, preferences: { ...(s.user.preferences ?? {}), ...patch } } }
          : s
      );
      try {
        await api.auth.setPreferences(patch);
      } catch {
        // Preference persistence is best-effort.
      }
    },
  };
}

export const auth = createAuthStore();
export const isAuthenticated = derived(auth, ($a) => $a.user !== null);
export const currentUser = derived(auth, ($a) => $a.user);
export const preferences = derived(auth, ($a) => $a.user?.preferences ?? {});

export function hasPermission(required: string): boolean {
  const state = get(auth);
  if (!state.user) return false;
  return state.user.permissions.some((held) => matchPermission(held, required));
}

export type { User };
