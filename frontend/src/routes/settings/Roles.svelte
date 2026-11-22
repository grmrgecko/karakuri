<script lang="ts">
  import { onMount } from 'svelte';
  import { rolesStore } from '../../lib/stores/roles';
  import type { Role, RolePermission, RoleAssignment, PermissionTree } from '../../lib/stores/roles';
  import { usersStore } from '../../lib/stores/users';
  import type { UserRecord } from '../../lib/stores/users';
  import { groupsStore } from '../../lib/stores/groups';
  import type { Group } from '../../lib/stores/groups';
  import { confirm } from '../../lib/stores/confirmDialog';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import PageHeader from '../../lib/components/PageHeader.svelte';
  import PermissionList from '../../lib/components/PermissionList.svelte';

  let roles: Role[] = $state([]);
  let rolesLoading = $state(true);

  let showRoleModal = $state(false);
  let editingRole = $state<Role | null>(null);
  let roleName = $state('');
  let roleDescription = $state('');
  let rolePermissions: RolePermission[] = $state([]);
  let permTree: PermissionTree = $state({});

  let assignments: RoleAssignment[] = $state([]);
  let showAssignmentModal = $state(false);
  let assignRoleId = $state<number | null>(null);
  let assignObjectType = $state<'user' | 'group' | null>(null);
  let assignObjectId = $state('');
  let users: UserRecord[] = $state([]);
  let groups: Group[] = $state([]);

  onMount(() => {
    loadRoles();
    loadAssignments();
    loadPermissionTree();
    loadUsers();
    loadGroups();
  });

  async function loadRoles() {
    rolesLoading = true;
    try {
      roles = (await rolesStore.listRoles()) || [];
    } catch { /* handled by empty state */ }
    rolesLoading = false;
  }

  async function loadAssignments() {
    try {
      assignments = (await rolesStore.listAssignments()) || [];
    } catch { /* ignore */ }
  }

  async function loadPermissionTree() {
    try {
      permTree = await rolesStore.getPermissionTree();
    } catch { /* autocomplete stays inert */ }
  }

  async function loadUsers() {
    try {
      users = (await usersStore.list()) || [];
    } catch { /* ignore */ }
  }

  async function loadGroups() {
    try {
      groups = (await groupsStore.list()) || [];
    } catch { /* ignore */ }
  }

  function openRoleModal(role: Role | null) {
    editingRole = role;
    roleName = role?.name || '';
    roleDescription = role?.description || '';
    rolePermissions = [];
    showRoleModal = true;

    if (role) {
      loadRolePermissions(role.id);
    }
  }

  async function loadRolePermissions(roleId: number) {
    try {
      rolePermissions = await rolesStore.listPermissions(roleId);
    } catch { /* ignore */ }
  }

  async function saveRole() {
    try {
      if (editingRole) {
        await rolesStore.updateRole(editingRole.id, roleName, roleDescription);
      } else {
        const role = await rolesStore.createRole(roleName, roleDescription);
        editingRole = role;
      }
      await loadRoles();
    } catch (e: any) {
      alert(e.message || 'Failed to save role');
    }
  }

  async function deleteRole(id: number) {
    if (!await confirm('Delete this role? This will also remove all its assignments.')) return;
    try {
      await rolesStore.deleteRole(id);
      await loadRoles();
      await loadAssignments();
    } catch (e: any) {
      alert(e.message || 'Failed to delete role');
    }
  }

  async function addPermissionToRole(permission: string) {
    if (!editingRole) return;
    try {
      await rolesStore.addPermission(editingRole.id, permission);
      await loadRolePermissions(editingRole.id);
    } catch (e: any) {
      alert(e.message || 'Failed to add permission');
    }
  }

  async function removePermissionFromRole(index: number) {
    const rp = rolePermissions[index];
    if (!editingRole || !rp) return;
    try {
      await rolesStore.removePermission(editingRole.id, rp.id);
      await loadRolePermissions(editingRole.id);
    } catch (e: any) {
      alert(e.message || 'Failed to remove permission');
    }
  }

  function openAssignmentModal() {
    assignRoleId = null;
    assignObjectType = null;
    assignObjectId = '';
    showAssignmentModal = true;
  }

  async function createAssignment() {
    if (!assignRoleId || !assignObjectType || !assignObjectId) return;
    try {
      if (assignObjectType === 'user') {
        await rolesStore.createAssignment(assignRoleId, assignObjectId);
      } else {
        await rolesStore.createAssignment(assignRoleId, undefined, parseInt(assignObjectId));
      }
      showAssignmentModal = false;
      assignRoleId = null;
      assignObjectType = null;
      assignObjectId = '';
      await loadAssignments();
    } catch (e: any) {
      alert(e.message || 'Failed to create assignment');
    }
  }

  async function deleteAssignment(id: number) {
    try {
      await rolesStore.deleteAssignment(id);
      await loadAssignments();
    } catch (e: any) {
      alert(e.message || 'Failed to delete assignment');
    }
  }
</script>

<PageHeader title="Roles">
  <button class="btn btn-primary text-sm" onclick={() => openRoleModal(null)}>
    <i class="las la-plus mr-1"></i> Add Role
  </button>
</PageHeader>

{#if rolesLoading}
  <p class="opacity-60">Loading roles...</p>
{:else}
  <div class="card overflow-hidden mb-8">
    <table class="w-full text-sm">
      <thead>
        <tr style="background-color: var(--theme-surface0);">
          <th class="text-left px-4 py-2">Name</th>
          <th class="text-left px-4 py-2">Description</th>
          <th class="text-right px-4 py-2">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each roles as role}
          <tr class="border-t" style="border-color: var(--theme-surface1);">
            <td class="px-4 py-2 font-medium">{role.name}</td>
            <td class="px-4 py-2 opacity-60">{role.description}</td>
            <td class="px-4 py-2 text-right">
              <button class="btn btn-secondary text-xs px-2 py-1 mr-1" onclick={() => openRoleModal(role)}>
                Edit
              </button>
              <button class="btn btn-danger text-xs px-2 py-1" onclick={() => deleteRole(role.id)}>
                Delete
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<!-- Role Assignments -->
<PageHeader title="Role Assignments" level="section">
  <button class="btn btn-primary text-sm" onclick={openAssignmentModal}>
    <i class="las la-plus mr-1"></i> Add Assignment
  </button>
</PageHeader>

<div class="card overflow-hidden">
  <table class="w-full text-sm">
    <thead>
      <tr style="background-color: var(--theme-surface0);">
        <th class="text-left px-4 py-2">Role</th>
        <th class="text-left px-4 py-2">Type</th>
        <th class="text-left px-4 py-2">Assigned To</th>
        <th class="text-right px-4 py-2">Actions</th>
      </tr>
    </thead>
    <tbody>
      {#each assignments as a}
        <tr class="border-t" style="border-color: var(--theme-surface1);">
          <td class="px-4 py-2">{a.role?.name || `Role ${a.role_id}`}</td>
          <td class="px-4 py-2 opacity-60">{a.user_id ? 'User' : 'Group'}</td>
          <td class="px-4 py-2">{a.user?.username || a.group?.name || '-'}</td>
          <td class="px-4 py-2 text-right">
            <button class="btn btn-danger text-xs px-2 py-1" onclick={() => deleteAssignment(a.id)}>
              Remove
            </button>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<!-- Assignment Modal -->
{#if showAssignmentModal}
  <Modal title="Add Assignment" onclose={() => (showAssignmentModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createAssignment(); }} class="flex flex-col gap-4">
      <FormField label="Role" id="assign-role">
        <select id="assign-role" class="input w-full" bind:value={assignRoleId}>
          <option value={null}>Select role...</option>
          {#each roles as r}
            <option value={r.id}>{r.name}</option>
          {/each}
        </select>
      </FormField>
      <FormField label="Assign To" id="assign-type">
        <select id="assign-type" class="input w-full" bind:value={assignObjectType} disabled={!assignRoleId}>
          <option value={null}>Select type...</option>
          <option value="user">User</option>
          <option value="group">Group</option>
        </select>
      </FormField>
      <FormField
        label={assignObjectType === 'user' ? 'User' : assignObjectType === 'group' ? 'Group' : 'Target'}
        id="assign-object"
      >
        <select id="assign-object" class="input w-full" bind:value={assignObjectId} disabled={!assignObjectType}>
          <option value="">Select...</option>
          {#if assignObjectType === 'user'}
            {#each users as u}
              <option value={u.id}>{u.username}</option>
            {/each}
          {:else if assignObjectType === 'group'}
            {#each groups as g}
              <option value={String(g.id)}>{g.name}</option>
            {/each}
          {/if}
        </select>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary"
                disabled={!assignRoleId || !assignObjectType || !assignObjectId}>Assign</button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Role Editor Modal -->
{#if showRoleModal}
  <Modal title={editingRole ? 'Edit Role' : 'Add Role'} onclose={() => (showRoleModal = false)} maxWidth="lg" maxHeight="90vh">
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveRole(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="role-name">
        <input id="role-name" class="input w-full" bind:value={roleName} required />
      </FormField>
      <FormField label="Description" id="role-desc">
        <textarea id="role-desc" class="input w-full" rows="2" bind:value={roleDescription}></textarea>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">{editingRole ? 'Save' : 'Create'}</button>
      </div>
    </form>

    {#if editingRole}
      <hr class="my-4" style="border-color: var(--theme-surface1);" />
      <h3 class="text-sm font-semibold mb-2">Permissions</h3>
      <PermissionList
        permissions={rolePermissions.map((rp) => rp.permission)}
        tree={permTree}
        onadd={addPermissionToRole}
        onremove={(_p, i) => removePermissionFromRole(i)}
      />
    {/if}
  </Modal>
{/if}
