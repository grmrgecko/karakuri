<script lang="ts">
  import { onMount } from 'svelte';
  import { usersStore } from '../../lib/stores/users';
  import type { UserRecord } from '../../lib/stores/users';
  import { groupsStore } from '../../lib/stores/groups';
  import type { Group } from '../../lib/stores/groups';
  import { confirm } from '../../lib/stores/confirmDialog';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let users: UserRecord[] = $state([]);
  let usersLoading = $state(true);

  // Create user modal.
  let showCreateUser = $state(false);
  let newUsername = $state('');
  let newEmail = $state('');
  let newUserPassword = $state('');
  let availableGroups: Group[] = $state([]);
  let selectedGroupId = $state<number | null>(null);
  let groupsLoading = $state(false);

  // Create group modal, reachable from the group select inside create user.
  let showCreateGroup = $state(false);
  let newGroupName = $state('');

  // Edit user modal.
  let showEditUser = $state(false);
  let editingUser = $state<UserRecord | null>(null);
  let editUsername = $state('');
  let editEmail = $state('');

  // Reset password modal.
  let showResetPassword = $state(false);
  let resetNewPassword = $state('');
  let resetConfirmPassword = $state('');
  let resetPasswordError = $state('');

  onMount(() => {
    loadUsers();
  });

  async function loadUsers() {
    usersLoading = true;
    try {
      users = (await usersStore.list()) || [];
    } catch { /* handled by empty state */ }
    usersLoading = false;
  }

  async function loadGroups() {
    groupsLoading = true;
    try {
      availableGroups = (await groupsStore.list()) || [];
      // Default-select the "User" group, matching the backend's own default.
      const userGroup = availableGroups.find((g) => g.name === 'User');
      selectedGroupId = userGroup ? userGroup.id : null;
    } catch { /* group list unavailable without api:groups:read */ }
    groupsLoading = false;
  }

  function openCreateUser() {
    newUsername = '';
    newEmail = '';
    newUserPassword = '';
    selectedGroupId = null;
    showCreateUser = true;
    loadGroups();
  }

  function handleGroupSelect(e: Event) {
    const select = e.target as HTMLSelectElement;
    if (select.value === '__new__') {
      newGroupName = '';
      showCreateGroup = true;
      // Reset the select back to the previous value.
      select.value = selectedGroupId ? String(selectedGroupId) : '';
    } else {
      selectedGroupId = select.value ? parseInt(select.value) : null;
    }
  }

  async function createGroupFromModal() {
    if (!newGroupName.trim()) return;
    try {
      const group = await groupsStore.create(newGroupName.trim());
      availableGroups = [...availableGroups, group];
      selectedGroupId = group.id;
      showCreateGroup = false;
    } catch (e: any) {
      alert(e.message || 'Failed to create group');
    }
  }

  async function createUser() {
    try {
      await usersStore.create(
        newUsername,
        newEmail,
        newUserPassword,
        selectedGroupId ? [selectedGroupId] : [],
      );
      showCreateUser = false;
      await loadUsers();
    } catch (e: any) {
      alert(e.message || 'Failed to create user');
    }
  }

  function openEditUser(user: UserRecord) {
    editingUser = user;
    editUsername = user.username;
    editEmail = user.email;
    showEditUser = true;
  }

  async function saveEditUser() {
    if (!editingUser) return;
    try {
      await usersStore.update(editingUser.id, {
        username: editUsername,
        email: editEmail,
      });
      showEditUser = false;
      editingUser = null;
      await loadUsers();
    } catch (e: any) {
      alert(e.message || 'Failed to update user');
    }
  }

  function openResetPassword() {
    resetNewPassword = '';
    resetConfirmPassword = '';
    resetPasswordError = '';
    showResetPassword = true;
  }

  async function saveResetPassword() {
    if (!editingUser) return;
    resetPasswordError = '';
    if (resetNewPassword !== resetConfirmPassword) {
      resetPasswordError = 'Passwords do not match.';
      return;
    }
    if (!resetNewPassword) {
      resetPasswordError = 'Password is required.';
      return;
    }
    try {
      await usersStore.updatePassword(editingUser.id, resetNewPassword);
      showResetPassword = false;
      resetNewPassword = '';
      resetConfirmPassword = '';
    } catch (e: any) {
      alert(e.message || 'Failed to reset password');
    }
  }

  async function deleteUser(id: string) {
    if (!await confirm('Delete this user?')) return;
    try {
      await usersStore.remove(id);
      await loadUsers();
    } catch (e: any) {
      alert(e.message || 'Failed to delete user');
    }
  }
</script>

<PageHeader title="Users">
  <button class="btn btn-primary text-sm" onclick={openCreateUser}>
    <i class="las la-plus mr-1"></i> Create User
  </button>
</PageHeader>

{#if usersLoading}
  <p class="opacity-60">Loading users...</p>
{:else}
  <div class="card overflow-hidden">
    <table class="w-full text-sm">
      <thead>
        <tr style="background-color: var(--theme-surface0);">
          <th class="text-left px-4 py-2">Username</th>
          <th class="text-left px-4 py-2">Email</th>
          <th class="text-left px-4 py-2">Created</th>
          <th class="text-right px-4 py-2">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each users as u}
          <tr class="border-t" style="border-color: var(--theme-surface1);">
            <td class="px-4 py-2">{u.username}</td>
            <td class="px-4 py-2">{u.email}</td>
            <td class="px-4 py-2 opacity-60">{new Date(u.created_at).toLocaleDateString()}</td>
            <td class="px-4 py-2 text-right">
              <button class="btn btn-secondary text-xs px-2 py-1 mr-1" onclick={() => openEditUser(u)}>
                Edit
              </button>
              <button class="btn btn-danger text-xs px-2 py-1" onclick={() => deleteUser(u.id)}>
                Delete
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<!-- Create User Modal -->
{#if showCreateUser}
  <Modal title="Create User" onclose={() => (showCreateUser = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createUser(); }} class="flex flex-col gap-4">
      <FormField label="Username" id="cu-user">
        <input id="cu-user" class="input w-full" bind:value={newUsername} required />
      </FormField>
      <FormField label="Email" id="cu-email">
        <input id="cu-email" type="email" class="input w-full" bind:value={newEmail} required />
      </FormField>
      <FormField label="Password" id="cu-pass">
        <input id="cu-pass" type="password" class="input w-full" bind:value={newUserPassword} required />
      </FormField>
      <FormField label="Group" id="cu-group">
        {#if groupsLoading}
          <p class="text-sm opacity-60">Loading groups...</p>
        {:else}
          <select
            id="cu-group"
            class="input w-full"
            value={selectedGroupId ? String(selectedGroupId) : ''}
            onchange={handleGroupSelect}
          >
            {#each availableGroups as group}
              <option value={String(group.id)}>{group.name}</option>
            {/each}
            <option value="__new__">+ Add new group</option>
          </select>
        {/if}
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">Create</button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Create Group Modal, opened from the create user group select -->
{#if showCreateGroup}
  <Modal title="Add Group" onclose={() => (showCreateGroup = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createGroupFromModal(); }} class="flex flex-col gap-4">
      <FormField label="Group Name" id="cg-name">
        <input id="cg-name" class="input w-full" bind:value={newGroupName} required />
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">Create</button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Edit User Modal -->
{#if showEditUser}
  <Modal title="Edit User" onclose={() => (showEditUser = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveEditUser(); }} class="flex flex-col gap-4">
      <FormField label="Username" id="eu-user">
        <input id="eu-user" class="input w-full" bind:value={editUsername} required />
      </FormField>
      <FormField label="Email" id="eu-email">
        <input id="eu-email" type="email" class="input w-full" bind:value={editEmail} required />
      </FormField>
      <div class="flex items-center justify-between">
        <button type="button" class="btn btn-secondary text-sm" onclick={openResetPassword}>
          <i class="las la-key mr-1"></i> Reset Password
        </button>
        <button type="submit" class="btn btn-primary">Save</button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Reset Password Modal -->
{#if showResetPassword}
  <Modal title="Reset Password" onclose={() => (showResetPassword = false)}>
    {#if editingUser}
      <p class="text-sm opacity-60 mb-4">Setting a new password for <strong>{editingUser.username}</strong>.</p>
    {/if}
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveResetPassword(); }} class="flex flex-col gap-4">
      <FormField label="New Password" id="rp-new">
        <input id="rp-new" type="password" class="input w-full" bind:value={resetNewPassword} required />
      </FormField>
      <FormField label="Confirm Password" id="rp-confirm" error={resetPasswordError}>
        <input id="rp-confirm" type="password" class="input w-full" bind:value={resetConfirmPassword} required />
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">Save</button>
      </div>
    </form>
  </Modal>
{/if}
