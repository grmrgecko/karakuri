import { writable } from 'svelte/store';
import { api } from '../api/client';

interface Group {
  id: number;
  name: string;
  created_at: string;
}

interface GroupList {
  data: Group[];
  next?: number;
  [key: string]: unknown;
}

// pageLimit is the server's maximum page size, so list walks the fewest pages.
const pageLimit = 100;

interface GroupMember {
  group_id: number;
  user_id: string;
  user?: { id: string; username: string; email: string };
}

interface GroupOwner {
  id: number;
  group_id: number;
  user_id?: string;
  user?: { id: string; username: string; email: string };
  owner_group_id?: number;
  owner_group?: { id: number; name: string };
}

function createGroupsStore() {
  const groups = writable<Group[]>([]);

  return {
    groups,

    // list returns every group, following pagination to the end: the settings
    // pages draw one table with no pager.
    async list() {
      const all: Group[] = [];
      let page = 1;
      for (;;) {
        const result = await api.groups.list<GroupList>(pageLimit, page);
        all.push(...(result.data || []));
        if (!result.next) break;
        page = result.next;
      }
      groups.set(all);
      return all;
    },

    async create(name: string) {
      const group = await api.groups.create<Group>(name);
      await this.list();
      return group;
    },

    async update(id: number, name: string) {
      const group = await api.groups.update<Group>(id, name);
      await this.list();
      return group;
    },

    async remove(id: number) {
      await api.groups.remove(id);
      await this.list();
    },

    async listMembers(groupId: number): Promise<GroupMember[]> {
      return (await api.groups.listMembers<GroupMember[]>(groupId)) || [];
    },

    async addMember(groupId: number, userId: string) {
      return api.groups.addMember<GroupMember>(groupId, userId);
    },

    async removeMember(groupId: number, userId: string) {
      await api.groups.removeMember(groupId, userId);
    },

    async listOwners(groupId: number): Promise<GroupOwner[]> {
      return (await api.groups.listOwners<GroupOwner[]>(groupId)) || [];
    },

    async addOwner(groupId: number, userId?: string, ownerGroupId?: number) {
      const body: { user_id?: string; owner_group_id?: number } = {};
      if (userId) body.user_id = userId;
      if (ownerGroupId) body.owner_group_id = ownerGroupId;
      return api.groups.addOwner<GroupOwner>(groupId, body);
    },

    async removeOwner(groupId: number, ownerId: number) {
      await api.groups.removeOwner(groupId, ownerId);
    },
  };
}

export const groupsStore = createGroupsStore();
export type { Group, GroupMember, GroupOwner };
