<script lang="ts">
  import { onMount } from 'svelte';
  import { Link } from 'svelte-routing';
  import { dashboards } from '../lib/stores/dashboards';
  import { shareSuggestionsStore } from '../lib/stores/shareSuggestions';
  import type { Dashboard, DashboardAccess } from '../lib/stores/dashboards';
  import type { SuggestedUser, SuggestedGroup } from '../lib/stores/shareSuggestions';
  import type { ApiError } from '../lib/api/client';
  import Autocomplete from '../lib/components/Autocomplete.svelte';

  let { id }: { id: string } = $props();

  let dashboard = $state<Dashboard | null>(null);
  let shares: DashboardAccess[] = $state([]);
  let loading = $state(true);
  let shareError = $state('');

  // Type selector and autocomplete state.
  let shareType = $state<'user' | 'group'>('user');
  let searchQuery = $state('');
  let suggestedUsers: SuggestedUser[] = $state([]);
  let suggestedGroups: SuggestedGroup[] = $state([]);
  let showDropdown = $state(false);
  let selectedUsername = $state('');
  let selectedGroupName = $state('');
  // shareLevels names each access level as the share list and picker show it.
  // A read share watches; a view share also operates what the dashboard shows.
  const shareLevels: Record<string, string> = {
    read: 'View only',
    view: 'View and operate',
    edit: 'Edit',
    admin: 'Admin',
  };
  let sharePermission = $state('view');
  let addingShare = $state(false);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  let filteredItems = $derived(
    shareType === 'user' ? suggestedUsers : suggestedGroups
  );

  onMount(async () => {
    const dash = await dashboards.get(id);
    if (dash) {
      dashboard = dash;
      try {
        shares = await dashboards.listShares(dash.id);
      } catch (e) {
        console.error('Failed to load shares', e);
      }
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
    if (!dashboard) return;
    if (shareType === 'user' && !selectedUsername) {
      shareError = 'Select a user from the suggestions.';
      return;
    }
    if (shareType === 'group' && !selectedGroupName) {
      shareError = 'Select a group from the suggestions.';
      return;
    }
    addingShare = true;
    shareError = '';
    try {
      await dashboards.addShare(
        dashboard.id,
        selectedUsername || undefined,
        selectedGroupName || undefined,
        sharePermission
      );
      shares = await dashboards.listShares(dashboard.id);
      searchQuery = '';
      selectedUsername = '';
      selectedGroupName = '';
    } catch (e) {
      const err = e as ApiError;
      shareError = err.message || 'Failed to add share.';
    } finally {
      addingShare = false;
    }
  }

  async function removeShare(shareId: number) {
    if (!dashboard) return;
    try {
      await dashboards.removeShare(dashboard.id, shareId);
      shares = shares.filter((s) => s.id !== shareId);
    } catch (e) {
      console.error('Failed to remove share', e);
    }
  }
</script>

<div class="max-w-2xl mx-auto p-6">
  {#if loading}
    <div class="text-center py-12 text-text-muted">Loading...</div>
  {:else if !dashboard}
    <div class="text-center py-12 text-text-muted">Dashboard not found.</div>
  {:else}
    <div class="flex items-center gap-3 mb-6">
      <Link to="/dashboards"><i class="la la-arrow-left text-lg"></i></Link>
      <h1 class="text-2xl font-bold">Share "{dashboard.name}"</h1>
    </div>

    <!-- Current shares -->
    {#if shares.length > 0}
      <div class="mb-6">
        <h2 class="text-sm font-medium mb-2 uppercase tracking-wide" style="color: var(--theme-subtext0, #a6adc8);">Current Shares</h2>
        <div class="flex flex-col gap-2">
          {#each shares as share (share.id)}
            <div class="flex items-center justify-between p-3 rounded-lg"
                 style="background-color: var(--theme-surface0, #313244);">
              <div class="flex items-center gap-2">
                {#if share.user}
                  <i class="la la-user"></i>
                  <span class="text-sm">{share.user.username}</span>
                {:else if share.group}
                  <i class="la la-users"></i>
                  <span class="text-sm">{share.group.name}</span>
                {/if}
                <span class="text-xs px-1.5 py-0.5 rounded"
                      style="background-color: var(--theme-surface1, #45475a);">
                  {shareLevels[share.permission] ?? share.permission}
                </span>
              </div>
              <button
                class="share-remove-btn"
                title="Remove share"
                onclick={() => removeShare(share.id)}
              >
                <i class="la la-times"></i>
              </button>
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Add share form -->
    <div class="card p-4">
      <h2 class="text-sm font-medium mb-3">Add Share</h2>
      <div class="flex flex-col gap-3">
        <div>
          <label for="share-type" class="block text-xs mb-1">Share with</label>
          <select
            id="share-type"
            class="input w-full text-sm"
            bind:value={shareType}
            onchange={onTypeChange}
          >
            <option value="user">User</option>
            <option value="group">Group</option>
          </select>
        </div>

        <Autocomplete show={showDropdown && filteredItems.length > 0}>
          {#snippet children()}
            <label for="share-search" class="block text-xs mb-1">
              {shareType === 'user' ? 'Username' : 'Group name'}
            </label>
            <input
              id="share-search"
              class="input w-full text-sm"
              placeholder="Search {shareType === 'user' ? 'users' : 'groups'}..."
              bind:value={searchQuery}
              oninput={onSearchInput}
              onfocus={() => { showDropdown = true; }}
              autocomplete="off"
            />
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
          <label for="share-perm" class="block text-xs mb-1">Permission</label>
          <select id="share-perm" class="input w-full text-sm" bind:value={sharePermission}>
            {#each Object.entries(shareLevels) as [level, label] (level)}
              <option value={level}>{label}</option>
            {/each}
          </select>
        </div>

        {#if shareError}
          <p class="text-xs" style="color: var(--theme-error, #f38ba8);">{shareError}</p>
        {/if}

        <div class="flex gap-2 justify-end">
          <button
            class="btn btn-primary text-sm"
            disabled={addingShare}
            onclick={addShare}
          >
            {addingShare ? 'Adding...' : 'Add Share'}
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
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
