import { api } from '../api/client';

interface Session {
  id: string;
  ip_address: string;
  user_agent: string;
  last_active_at: string;
  expires_at: string;
  created_at: string;
  current: boolean;
}

function createSessionsStore() {
  return {
    async list(): Promise<Session[]> {
      return (await api.sessions.list<Session[]>()) || [];
    },

    async revoke(id: string) {
      await api.sessions.revoke(id);
    },

    async revokeOthers() {
      return api.sessions.revokeOthers<{ message: string; revoked: number }>();
    },
  };
}

export const sessionsStore = createSessionsStore();
export type { Session };
