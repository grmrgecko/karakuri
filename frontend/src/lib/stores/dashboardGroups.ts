import { api } from '../api/client';

interface DashboardGroup {
  id: number;
  name: string;
  description: string;
  /** Parent group; absent at the root. Groups nest the way flow folders do. */
  parent_id?: number;
  sort_order: number;
  owner_id: string;
  owner?: { id: string; username: string; email: string };
}

interface DashboardGroupAccess {
  id: number;
  dashboard_group_id: number;
  user_id?: string;
  user?: { id: string; username: string };
  group_id?: number;
  group?: { id: number; name: string };
  permission: string;
}

function createDashboardGroupsStore() {
  return {
    async list() {
      return api.get<DashboardGroup[]>('/dashboard_groups');
    },

    async create(name: string, description: string, parentId: number | null = null) {
      return api.post<DashboardGroup>('/dashboard_groups', {
        name,
        description,
        ...(parentId === null ? {} : { parent_id: parentId }),
      });
    },

    // update patches fields; parent_id of 0 is how the API spells "move to the
    // root", since an omitted field means "leave it alone".
    async update(
      id: number,
      data: { name?: string; description?: string; parent_id?: number | null },
    ) {
      const body: Record<string, unknown> = { ...data };
      if ('parent_id' in data) body.parent_id = data.parent_id ?? 0;
      return api.put<DashboardGroup>(`/dashboard_groups/${id}`, body);
    },

    // move re-parents a group; null puts it at the root.
    async move(id: number, parentId: number | null) {
      return api.put<DashboardGroup>(`/dashboard_groups/${id}`, { parent_id: parentId ?? 0 });
    },

    async delete(id: number) {
      return api.delete(`/dashboard_groups/${id}`);
    },

    async listShares(id: number) {
      return api.get<DashboardGroupAccess[]>(`/dashboard_groups/${id}/shares`);
    },

    async addShare(id: number, username?: string, groupName?: string, permission = 'view') {
      const body: any = { permission };
      if (username) body.username = username;
      if (groupName) body.group_name = groupName;
      return api.post<DashboardGroupAccess>(`/dashboard_groups/${id}/shares`, body);
    },

    async removeShare(id: number, shareId: number) {
      return api.delete(`/dashboard_groups/${id}/shares/${shareId}`);
    },

    async reorder(items: { id: number; sort_order: number }[]) {
      await api.put('/dashboard_groups/reorder', { items });
    },
  };
}

export const dashboardGroupsStore = createDashboardGroupsStore();
export type { DashboardGroup, DashboardGroupAccess };
