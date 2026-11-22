<script lang="ts">
  import { onMount } from 'svelte';
  import { Link } from 'svelte-routing';
  import { api } from '../../lib/api/client';
  import { shareSuggestionsStore } from '../../lib/stores/shareSuggestions';
  import type { SuggestedUser, SuggestedGroup } from '../../lib/stores/shareSuggestions';
  import type { ApiError } from '../../lib/api/client';
  import Autocomplete from '../../lib/components/Autocomplete.svelte';
  import PageHeader from '../../lib/components/PageHeader.svelte';

  // ResourceShare lists and changes who a connection or an agent is shared
  // with. Only a user holding admin on it reaches the shares.
  let { kind, id }: { kind: 'connections' | 'agents'; id: string } = $props();

  type Level = 'use' | 'edit' | 'admin';
  interface Share {
    id: number;
    user?: { id: string; username: string };
    group?: { id: number; name: string };
    permission: Level;
  }
  interface Resource {
    id: string;
    name: string;
    access?: Level;
  }

  // levels names each access level as the list and picker show it.
  const levels: Record<Level, string> = {
    use: 'Use',
    edit: 'Edit',
    admin: 'Admin',
  };
  let levelHelp = $derived<Record<Level, string>>({
    use: kind === 'agents'
      ? 'See the agent and run its nodes in flows.'
      : 'See the connection and use it in flows, dashboards, and devices.',
    edit: kind === 'agents'
      ? 'Also rename, describe, enable, and disable the agent.'
      : 'Also change and test its settings.',
    admin: kind === 'agents'
      ? 'Also share, revoke, and delete the agent.'
      : 'Also share and delete the connection.',
  });
  let noun = $derived(kind === 'agents' ? 'agent' : 'connection');
  let back = $derived(kind === 'agents' ? '/settings/agents' : '/settings/connections');

  let resource = $state<Resource | null>(null);
  let shares = $state<Share[]>([]);
  let loading = $state(true);
  let shareError = $state('');

  let shareType = $state<'user' | 'group'>('user');
  let searchQuery = $state('');
  let suggestedUsers: SuggestedUser[] = $state([]);
  let suggestedGroups: SuggestedGroup[] = $state([]);
  let showDropdown = $state(false);
  let selectedUsername = $state('');
  let selectedGroupName = $state('');
  let permission = $state<Level>('use');
  let adding = $state(false);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  let filteredItems = $derived(shareType === 'user' ? suggestedUsers : suggestedGroups);

  async function loadResource(): Promise<Resource | null> {
    if (kind === 'connections') {
      return api.connections.get<Resource>(id);
    }
    const agents = await api.agents.list<Resource[]>();
    return agents.find((a) => a.id === id) ?? null;
  }

  onMount(async () => {
    try {
      resource = await loadResource();
      if (resource?.access === 'admin') {
        shares = await api.get<Share[]>(`/${kind}/${id}/shares`);
      }
    } catch (e) {
      console.error(`Failed to load the ${noun} or its shares`, e);
    }
    try {
      const result = await shareSuggestionsStore.getSuggestions();
      suggestedUsers = result.users;
      suggestedGroups = result.groups;
    } catch (e) {
      console.error('Failed to load suggestions', e);
    }
    loading = false;
  });

  function onTypeChange() {
    searchQuery = '';
    selectedUsername = '';
    selectedGroupName = '';
    showDropdown = false;
  }

  function onSearchInput() {
    selectedUsername = '';
    selectedGroupName = '';
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(async () => {
      try {
        const result = await shareSuggestionsStore.getSuggestions(searchQuery || undefined);
        suggestedUsers = result.users;
        suggestedGroups = result.groups;
        showDropdown = true;
      } catch (e) {
        console.error('Failed to search suggestions', e);
      }
    }, 250);
  }

  function selectUser(user: SuggestedUser) {
    selectedUsername = user.username;
    selectedGroupName = '';
    searchQuery = user.username;
    showDropdown = false;
  }

  function selectGroup(group: SuggestedGroup) {
    selectedGroupName = group.name;
    selectedUsername = '';
    searchQuery = group.name;
    showDropdown = false;
  }

  async function addShare() {
    if (shareType === 'user' && !selectedUsername) {
      shareError = 'Select a user from the suggestions.';
      return;
    }
    if (shareType === 'group' && !selectedGroupName) {
      shareError = 'Select a group from the suggestions.';
      return;
    }
    adding = true;
    shareError = '';
    try {
      const body: Record<string, string> = { permission };
      if (selectedUsername) body.username = selectedUsername;
      if (selectedGroupName) body.group_name = selectedGroupName;
      await api.post(`/${kind}/${id}/shares`, body);
      shares = await api.get<Share[]>(`/${kind}/${id}/shares`);
      searchQuery = '';
      selectedUsername = '';
      selectedGroupName = '';
    } catch (e) {
      shareError = (e as ApiError).message || 'Could not add the share.';
    } finally {
      adding = false;
    }
  }

  async function removeShare(shareId: number) {
    try {
      await api.delete(`/${kind}/${id}/shares/${shareId}`);
      shares = shares.filter((s) => s.id !== shareId);
    } catch (e) {
      shareError = (e as ApiError).message || 'Could not remove the share.';
    }
  }
</script>

<div class="max-w-2xl">
  {#if loading}
    <p class="opacity-60">Loading...</p>
  {:else if !resource || resource.access !== 'admin'}
    <p class="opacity-60">The {noun} was not found, or you may not share it.</p>
    <Link to={back} class="link text-sm">Back</Link>
  {:else}
    <div class="flex items-center gap-3 mb-2">
      <Link to={back} aria-label="Back"><i class="la la-arrow-left text-lg"></i></Link>
      <PageHeader title={`Share ${resource.name}`} />
    </div>
    <p class="text-sm opacity-60 mb-4">
      Users and groups listed here can see this {noun}. Whoever added it, and holders of the
      manage permission, can always reach it.
    </p>

    {#if shares.length > 0}
      <div class="mb-6">
        <h2 class="text-sm font-medium mb-2 opacity-70">Shared with</h2>
        <div class="flex flex-col gap-2">
          {#each shares as share (share.id)}
            <div class="flex items-center justify-between p-3 rounded-lg share-row">
              <div class="flex items-center gap-2">
                {#if share.user}
                  <i class="la la-user"></i>
                  <span class="text-sm">{share.user.username}</span>
                {:else if share.group}
                  <i class="la la-users"></i>
                  <span class="text-sm">{share.group.name}</span>
                {/if}
                <span class="text-xs px-1.5 py-0.5 rounded level-tag">
                  {levels[share.permission] ?? share.permission}
                </span>
              </div>
              <button class="share-remove-btn" title="Remove share" aria-label="Remove share"
                      onclick={() => removeShare(share.id)}>
                <i class="la la-times"></i>
              </button>
            </div>
          {/each}
        </div>
      </div>
    {:else}
      <p class="text-sm opacity-60 mb-6">Not shared with anyone yet.</p>
    {/if}

    <div class="card p-4">
      <h2 class="text-sm font-medium mb-3">Add a share</h2>
      <div class="flex flex-col gap-3">
        <div>
          <label for="share-type" class="block text-xs mb-1">Share with</label>
          <select id="share-type" class="input w-full text-sm" bind:value={shareType} onchange={onTypeChange}>
            <option value="user">User</option>
            <option value="group">Group</option>
          </select>
        </div>

        <Autocomplete show={showDropdown && filteredItems.length > 0}>
          {#snippet children()}
            <label for="share-search" class="block text-xs mb-1">
              {shareType === 'user' ? 'Username' : 'Group name'}
            </label>
            <input id="share-search" class="input w-full text-sm"
                   placeholder="Search {shareType === 'user' ? 'users' : 'groups'}..."
                   bind:value={searchQuery} oninput={onSearchInput}
                   onfocus={() => { showDropdown = true; }} autocomplete="off" />
          {/snippet}
          {#snippet items()}
            {#if shareType === 'user'}
              {#each suggestedUsers as user (user.id)}
                <button class="dropdown-item" onclick={() => selectUser(user)}>
                  <i class="la la-user"></i> {user.username}
                </button>
              {/each}
            {:else}
              {#each suggestedGroups as grp (grp.id)}
                <button class="dropdown-item" onclick={() => selectGroup(grp)}>
                  <i class="la la-users"></i> {grp.name}
                </button>
              {/each}
            {/if}
          {/snippet}
        </Autocomplete>

        <div>
          <label for="share-perm" class="block text-xs mb-1">Access</label>
          <select id="share-perm" class="input w-full text-sm" bind:value={permission}>
            {#each Object.entries(levels) as [level, label] (level)}
              <option value={level}>{label}</option>
            {/each}
          </select>
          <p class="text-xs opacity-60 mt-1">{levelHelp[permission]}</p>
        </div>

        {#if shareError}
          <p class="text-xs" style="color: var(--theme-error, #f38ba8);">{shareError}</p>
        {/if}

        <div class="flex justify-end">
          <button class="btn btn-primary text-sm" disabled={adding} onclick={addShare}>
            {adding ? 'Adding...' : 'Add share'}
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .share-row {
    background-color: var(--theme-surface0, #313244);
  }

  .level-tag {
    background-color: var(--theme-surface1, #45475a);
  }

  .dropdown-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    padding: 0.5rem 0.75rem;
    font-size: 0.85rem;
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
    text-align: left;
  }

  .dropdown-item:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .share-remove-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    border: none;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    transition: background-color 0.15s ease;
  }

  .share-remove-btn:hover {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }
</style>
