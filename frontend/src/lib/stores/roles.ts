import { writable } from 'svelte/store';
import { api } from '../api/client';

interface Role {
  id: number;
  name: string;
  description: string;
}

interface RolePermission {
  id: number;
  role_id: number;
  permission: string;
}

interface RoleAssignment {
  id: number;
  role_id: number;
  role?: Role;
  user_id?: string;
  user?: { id: string; username: string; email: string };
  group_id?: number;
  group?: { id: number; name: string };
}

// PermissionTree mirrors the registry's nested permission map: each segment is a
// key, leaves are action lists, and a node that is both stores actions under
// "_actions".
type PermissionTree = Record<string, unknown>;

function createRolesStore() {
  const roles = writable<Role[]>([]);
  const assignments = writable<RoleAssignment[]>([]);

  return {
    roles,
    assignments,

    async listRoles() {
      const data = await api.roles.list<Role[]>();
      roles.set(data || []);
      return data;
    },

    async createRole(name: string, description: string) {
      const role = await api.roles.create<Role>(name, description);
      await this.listRoles();
      return role;
    },

    async updateRole(id: number, name: string, description: string) {
      const role = await api.roles.update<Role>(id, name, description);
      await this.listRoles();
      return role;
    },

    async deleteRole(id: number) {
      await api.roles.remove(id);
      await this.listRoles();
    },

    async listPermissions(roleId: number): Promise<RolePermission[]> {
      return (await api.roles.listPermissions<RolePermission[]>(roleId)) || [];
    },

    async addPermission(roleId: number, permission: string) {
      return api.roles.addPermission<RolePermission>(roleId, permission);
    },

    async removePermission(roleId: number, permId: number) {
      await api.roles.removePermission(roleId, permId);
    },

    async listAssignments() {
      const data = await api.roles.listAssignments<RoleAssignment[]>();
      assignments.set(data || []);
      return data;
    },

    async createAssignment(roleId: number, userId?: string, groupId?: number) {
      const body: { user_id?: string; group_id?: number } = {};
      if (userId) body.user_id = userId;
      if (groupId) body.group_id = groupId;
      const result = await api.roles.createAssignment<RoleAssignment>(roleId, body);
      await this.listAssignments();
      return result;
    },

    async deleteAssignment(id: number) {
      await api.roles.removeAssignment(id);
      await this.listAssignments();
    },

    async getPermissionTree(): Promise<PermissionTree> {
      return (await api.roles.permissionTree<PermissionTree>()) || {};
    },
  };
}

export const rolesStore = createRolesStore();
export type { Role, RolePermission, RoleAssignment, PermissionTree };
