import { writable } from 'svelte/store';
import { api } from '../api/client';

interface UserRecord {
  id: string;
  username: string;
  email: string;
  created_at: string;
}

interface UserList {
  data: UserRecord[];
  next?: number;
  [key: string]: unknown;
}

// pageLimit is the server's maximum page size, so list walks the fewest pages.
const pageLimit = 100;

function createUsersStore() {
  const users = writable<UserRecord[]>([]);

  return {
    users,

    // list returns every user, following pagination to the end: the settings
    // pages draw one table with no pager.
    async list() {
      const all: UserRecord[] = [];
      let page = 1;
      for (;;) {
        const result = await api.users.list<UserList>(pageLimit, page);
        all.push(...(result.data || []));
        if (!result.next) break;
        page = result.next;
      }
      users.set(all);
      return all;
    },

    async create(username: string, email: string, password: string, groupIds?: number[]) {
      const user = await api.users.create<UserRecord>({
        username,
        email,
        password,
        ...(groupIds?.length ? { group_ids: groupIds } : {}),
      });
      await this.list();
      return user;
    },

    async update(id: string, body: { username?: string; email?: string }) {
      const user = await api.users.update<UserRecord>(id, body);
      await this.list();
      return user;
    },

    async updatePassword(id: string, newPassword: string, oldPassword?: string) {
      const body: { old_password?: string; new_password: string } = {
        new_password: newPassword,
      };
      if (oldPassword) body.old_password = oldPassword;
      await api.users.updatePassword(id, body);
    },

    async remove(id: string) {
      await api.users.remove(id);
      await this.list();
    },
  };
}

export const usersStore = createUsersStore();
export type { UserRecord };
