import { writable } from 'svelte/store';
import { api } from '../api/client';
import type { FlowAccessLevel } from './flows';

interface FolderRecord {
  id: number;
  name: string;
  parent_id?: number;
  owner_id: string;
  /** The caller's level on the folder, which reaches everything inside it. */
  access_level?: FlowAccessLevel;
  created_at: string;
  updated_at: string;
}

// FolderAccess is one share of a folder with a user or user group.
interface FolderAccess {
  id: number;
  folder_id: number;
  user?: { username: string };
  group?: { name: string };
  permission: FlowAccessLevel;
  created_at: string;
}

function createFoldersStore() {
  const folders = writable<FolderRecord[]>([]);

  return {
    folders,

    async list() {
      const data = (await api.folders.list<FolderRecord[]>()) || [];
      folders.set(data);
      return data;
    },

    async create(name: string, parentId: number | null) {
      const folder = await api.folders.create<FolderRecord>({
        name,
        ...(parentId === null ? {} : { parent_id: parentId }),
      });
      await this.list();
      return folder;
    },

    // update replaces both name and parent; a null parent moves it to the root.
    async update(id: number, name: string, parentId: number | null) {
      const folder = await api.folders.update<FolderRecord>(id, {
        name,
        ...(parentId === null ? {} : { parent_id: parentId }),
      });
      await this.list();
      return folder;
    },

    async remove(id: number) {
      await api.folders.remove(id);
      await this.list();
    },

    async listShares(id: number) {
      return api.get<FolderAccess[]>(`/folders/${id}/shares`);
    },

    async addShare(id: number, username?: string, groupName?: string, permission: FlowAccessLevel = 'view') {
      const body: Record<string, string> = { permission };
      if (username) body.username = username;
      if (groupName) body.group_name = groupName;
      return api.post<FolderAccess>(`/folders/${id}/shares`, body);
    },

    async removeShare(id: number, shareId: number) {
      return api.delete(`/folders/${id}/shares/${shareId}`);
    },
  };
}

export const foldersStore = createFoldersStore();
export type { FolderRecord, FolderAccess };
