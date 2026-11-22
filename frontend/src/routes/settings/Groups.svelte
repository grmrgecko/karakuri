<script lang="ts">
  import { onMount } from 'svelte';
  import { groupsStore } from '../../lib/stores/groups';
  import type { Group, GroupMember, GroupOwner } from '../../lib/stores/groups';
  import { usersStore } from '../../lib/stores/users';
  import type { UserRecord } from '../../lib/stores/users';
  import { confirm } from '../../lib/stores/confirmDialog';
  import Modal from '../../lib/components/Modal.svelte';
  import FormField from '../../lib/components/FormField.svelte';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  let groups: Group[] = $state([]);
  let groupsLoading = $state(true);
  let users: UserRecord[] = $state([]);

  // Group editor modal.
  let showGroupModal = $state(false);
  let editingGroup = $state<Group | null>(null);
  let groupName = $state('');

  // Members management inside the editor modal.
  let members: GroupMember[] = $state([]);
  let addMemberUserId = $state('');

  // Owners management inside the editor modal.
  let owners: GroupOwner[] = $state([]);
  let addOwnerType = $state<'user' | 'group' | null>(null);
  let addOwnerId = $state('');

  onMount(() => {
    loadGroups();
    loadUsers();
  });

  async function loadGroups() {
    groupsLoading = true;
    try {
      groups = (await groupsStore.list()) || [];
    } catch { /* handled by empty state */ }
    groupsLoading = false;
  }

  async function loadUsers() {
    try {
      users = (await usersStore.list()) || [];
    } catch { /* ignore */ }
  }

  function openGroupModal(group: Group | null) {
    editingGroup = group;
    groupName = group?.name || '';
    members = [];
    owners = [];
    addMemberUserId = '';
    addOwnerType = null;
    addOwnerId = '';
    showGroupModal = true;

    if (group) {
      loadMembers(group.id);
      loadOwners(group.id);
    }
  }

  async function saveGroup() {
    try {
      if (editingGroup) {
        await groupsStore.update(editingGroup.id, groupName);
      } else {
        const group = await groupsStore.create(groupName);
        editingGroup = group;
        if (group) {
          loadOwners(group.id);
        }
      }
      await loadGroups();
    } catch (e: any) {
      alert(e.message || 'Failed to save group');
    }
  }

  async function deleteGroup(id: number) {
    if (!await confirm('Delete this group? This will also remove all its members and owners.')) return;
    try {
      await groupsStore.remove(id);
      await loadGroups();
    } catch (e: any) {
      alert(e.message || 'Failed to delete group');
    }
  }

  // --- Members ---

  async function loadMembers(groupId: number) {
    try {
      members = await groupsStore.listMembers(groupId);
    } catch { /* ignore */ }
  }

  async function addMember() {
    if (!editingGroup || !addMemberUserId) return;
    try {
      await groupsStore.addMember(editingGroup.id, addMemberUserId);
      await loadMembers(editingGroup.id);
      addMemberUserId = '';
    } catch (e: any) {
      alert(e.message || 'Failed to add member');
    }
  }

  async function removeMember(userId: string) {
    if (!editingGroup) return;
    try {
      await groupsStore.removeMember(editingGroup.id, userId);
      await loadMembers(editingGroup.id);
    } catch (e: any) {
      alert(e.message || 'Failed to remove member');
    }
  }

  // --- Owners ---

  async function loadOwners(groupId: number) {
    try {
      owners = await groupsStore.listOwners(groupId);
    } catch { /* ignore */ }
  }

  async function addOwner() {
    if (!editingGroup || !addOwnerType || !addOwnerId) return;
    try {
      if (addOwnerType === 'user') {
        await groupsStore.addOwner(editingGroup.id, addOwnerId, undefined);
      } else {
        await groupsStore.addOwner(editingGroup.id, undefined, parseInt(addOwnerId));
      }
      await loadOwners(editingGroup.id);
      addOwnerType = null;
      addOwnerId = '';
    } catch (e: any) {
      alert(e.message || 'Failed to add owner');
    }
  }

  async function removeOwner(ownerId: number) {
    if (!editingGroup) return;
    try {
      await groupsStore.removeOwner(editingGroup.id, ownerId);
      await loadOwners(editingGroup.id);
    } catch (e: any) {
      alert(e.message || 'Failed to remove owner');
    }
  }

  let availableMembers = $derived(users.filter((u) => !members.some((m) => m.user_id === u.id)));
</script>

<PageHeader title="Groups">
  <button class="btn btn-primary text-sm" onclick={() => openGroupModal(null)}>
    <i class="las la-plus mr-1"></i> Add Group
  </button>
</PageHeader>

{#if groupsLoading}
  <p class="opacity-60">Loading groups...</p>
{:else}
  <div class="card overflow-hidden">
    <table class="w-full text-sm">
      <thead>
        <tr style="background-color: var(--theme-surface0);">
          <th class="text-left px-4 py-2">Name</th>
          <th class="text-left px-4 py-2">Created</th>
          <th class="text-right px-4 py-2">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each groups as group}
          <tr class="border-t" style="border-color: var(--theme-surface1);">
            <td class="px-4 py-2 font-medium">{group.name}</td>
            <td class="px-4 py-2 opacity-60">{new Date(group.created_at).toLocaleDateString()}</td>
            <td class="px-4 py-2 text-right">
              <button class="btn btn-secondary text-xs px-2 py-1 mr-1" onclick={() => openGroupModal(group)}>
                Edit
              </button>
              <button class="btn btn-danger text-xs px-2 py-1" onclick={() => deleteGroup(group.id)}>
                Delete
              </button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<!-- Group Editor Modal -->
{#if showGroupModal}
  <Modal title={editingGroup ? 'Edit Group' : 'Add Group'} onclose={() => (showGroupModal = false)} maxWidth="lg" maxHeight="90vh">
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveGroup(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="group-name">
        <input id="group-name" class="input w-full" bind:value={groupName} required />
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">{editingGroup ? 'Save' : 'Create'}</button>
      </div>
    </form>

    {#if editingGroup}
      <hr class="my-4" style="border-color: var(--theme-surface1);" />

      <!-- Members Section -->
      <h3 class="text-sm font-semibold mb-2">Members</h3>
      <div class="flex gap-2 mb-3">
        <select class="input flex-1 text-sm" bind:value={addMemberUserId}>
          <option value="">Select user...</option>
          {#each availableMembers as u}
            <option value={u.id}>{u.username}</option>
          {/each}
        </select>
        <button class="btn btn-primary text-sm" onclick={addMember} disabled={!addMemberUserId}>
          Add
        </button>
      </div>

      {#if members.length > 0}
        <div class="flex flex-col gap-1 mb-4">
          {#each members as m}
            <div class="flex items-center justify-between px-3 py-1.5 rounded text-sm"
                 style="background-color: var(--theme-surface0);">
              <span>{m.user?.username || m.user_id}</span>
              <button class="btn btn-danger text-xs px-2 py-0.5" onclick={() => removeMember(m.user_id)} aria-label="Remove member">
                <i class="las la-times"></i>
              </button>
            </div>
          {/each}
        </div>
      {:else}
        <p class="text-sm opacity-60 mb-4">No members.</p>
      {/if}

      <!-- Owners Section -->
      <h3 class="text-sm font-semibold mb-2">Owners</h3>
      <div class="flex gap-2 mb-3">
        <select class="input text-sm" style="width: 100px;" bind:value={addOwnerType}>
          <option value={null}>Type...</option>
          <option value="user">User</option>
          <option value="group">Group</option>
        </select>
        <select class="input flex-1 text-sm" bind:value={addOwnerId} disabled={!addOwnerType}>
          <option value="">Select...</option>
          {#if addOwnerType === 'user'}
            {#each users as u}
              <option value={u.id}>{u.username}</option>
            {/each}
          {:else if addOwnerType === 'group'}
            {#each groups as g}
              <option value={String(g.id)}>{g.name}</option>
            {/each}
          {/if}
        </select>
        <button class="btn btn-primary text-sm" onclick={addOwner} disabled={!addOwnerType || !addOwnerId}>
          Add
        </button>
      </div>

      {#if owners.length > 0}
        <div class="flex flex-col gap-1">
          {#each owners as o}
            <div class="flex items-center justify-between px-3 py-1.5 rounded text-sm"
                 style="background-color: var(--theme-surface0);">
              <span>
                {#if o.user}
                  <i class="las la-user mr-1"></i>{o.user.username}
                {:else if o.owner_group}
                  <i class="las la-layer-group mr-1"></i>{o.owner_group.name}
                {:else}
                  Unknown
                {/if}
              </span>
              <button class="btn btn-danger text-xs px-2 py-0.5" onclick={() => removeOwner(o.id)} aria-label="Remove owner">
                <i class="las la-times"></i>
              </button>
            </div>
          {/each}
        </div>
      {:else}
        <p class="text-sm opacity-60">No owners.</p>
      {/if}
    {/if}
  </Modal>
{/if}
