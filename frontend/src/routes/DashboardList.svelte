<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { watchLists } from '../lib/liveList';
  import { navigate } from 'svelte-routing';
  import { dashboards } from '../lib/stores/dashboards';
  import { dashboardGroupsStore } from '../lib/stores/dashboardGroups';
  import { auth, isAuthenticated, currentUser, preferences } from '../lib/stores/auth';
  import { pinnedIds } from '../lib/homeStart';
  import { slugify } from '../lib/slug';
  import { confirm } from '../lib/stores/confirmDialog';
  import Modal from '../lib/components/Modal.svelte';
  import FormField from '../lib/components/FormField.svelte';
  import PageHeader from '../lib/components/PageHeader.svelte';
  import BrowseList from '../lib/components/BrowseList.svelte';
  import type { BrowseFolder, BrowseItem } from '../lib/components/BrowseList.svelte';
  import type { Dashboard } from '../lib/stores/dashboards';
  import type { DashboardGroup } from '../lib/stores/dashboardGroups';
  import type { ApiError } from '../lib/api/client';

  let groups: DashboardGroup[] = $state([]);
  let loading = $state(true);

  // The browsed folder survives a reload, matching the flow list. Read during
  // init, not in onMount: the effect below runs first and would persist the
  // default over the saved value.
  const folderKey = 'karakuri.dashboardList.folder';

  function savedFolder(): number | null {
    try {
      const saved = localStorage.getItem(folderKey);
      return saved ? Number(saved) : null;
    } catch {
      return null;
    }
  }

  let folderId = $state<number | null>(savedFolder());

  $effect(() => {
    try {
      localStorage.setItem(folderKey, folderId === null ? '' : String(folderId));
    } catch { /* storage unavailable; the folder is per-session then */ }
  });

  let toast = $state('');
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  function showToast(message: string) {
    toast = message;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 6000);
  }

  let dashboardState = $derived($dashboards);
  let userId = $derived($currentUser?.id);

  // Dashboards and groups added, moved, shared, or removed in other sessions
  // arrive as a list notice, which reads the list again.
  let stopLive: (() => void) | null = null;

  onMount(async () => {
    stopLive = watchLists(['dashboards'], () => loadAll(true));
    await loadAll();
  });

  onDestroy(() => stopLive?.());

  // loadAll reads the dashboards and groups. A quiet reload keeps the list on
  // screen instead of showing the loading state.
  async function loadAll(quiet = false) {
    if (!quiet) loading = true;
    const dashPromise = dashboards.list();
    let groupPromise: Promise<DashboardGroup[]> | null = null;
    if ($isAuthenticated) {
      groupPromise = dashboardGroupsStore.list();
    }
    await dashPromise;
    if (groupPromise) {
      groups = (await groupPromise) || [];
    }
    if (folderId !== null && !groups.some((g) => g.id === folderId)) folderId = null;
    loading = false;
  }

  // --- Shapes the browser understands ---
  //
  // A dashboard group is this list's folder, and reads as one everywhere the
  // user can see it. Sharing hangs off the group, so a folder the viewer does
  // not own is labelled and left un-draggable: re-parenting someone else's
  // share would move it out of their tree, not the viewer's.

  let browseFolders = $derived<BrowseFolder[]>(
    groups.map((g) => ({
      id: g.id,
      name: g.name,
      parentId: g.parent_id ?? null,
      note: g.owner_id !== userId && g.owner?.username ? `shared by ${g.owner.username}` : undefined,
      canEdit: g.owner_id === userId,
    })),
  );

  let browseItems = $derived<BrowseItem[]>(
    dashboardState.dashboards.map((d) => ({
      id: d.id,
      name: d.name,
      description: d.description,
      folderId: d.group_id ?? null,
    })),
  );

  function dashboardOf(item: BrowseItem): Dashboard | undefined {
    return dashboardState.dashboards.find((d) => d.id === item.id);
  }

  function groupRecord(folder: BrowseFolder): DashboardGroup | undefined {
    return groups.find((g) => g.id === folder.id);
  }

  // groupOptions flattens the tree into "Parent / Child" labels for pickers.
  function groupOptions(parentId: number | null = null, prefix = ''): { id: number; label: string }[] {
    const out: { id: number; label: string }[] = [];
    for (const group of groups
      .filter((g) => (g.parent_id ?? null) === parentId && g.owner_id === userId)
      .sort((a, b) => a.name.localeCompare(b.name))) {
      const label = prefix ? `${prefix} / ${group.name}` : group.name;
      out.push({ id: group.id, label });
      out.push(...groupOptions(group.id, label));
    }
    return out;
  }

  let allGroupOptions = $derived(groupOptions());

  function descendantIDs(id: number): Set<number> {
    const out = new Set<number>([id]);
    let grew = true;
    while (grew) {
      grew = false;
      for (const g of groups) {
        const parent = g.parent_id ?? null;
        if (parent !== null && out.has(parent) && !out.has(g.id)) {
          out.add(g.id);
          grew = true;
        }
      }
    }
    return out;
  }

  // --- Moves ---

  async function moveDashboard(item: BrowseItem, target: number | null) {
    await dashboards.update(item.id, { group_id: target ?? 0 } as Partial<Dashboard>);
    await dashboards.list();
  }

  async function moveGroup(folder: BrowseFolder, parentId: number | null) {
    await dashboardGroupsStore.move(folder.id, parentId);
    groups = groups.map((g) =>
      g.id === folder.id ? { ...g, parent_id: parentId ?? undefined } : g,
    );
  }

  // --- Dashboard actions ---

  let showCreateModal = $state(false);
  let newName = $state('');
  let newSlug = $state('');
  let slugManuallyEdited = $state(false);
  let newDescription = $state('');
  let newGroupId = $state<number | null>(null);
  let newLayoutMode = $state<'fit' | 'scroll'>('fit');
  let creating = $state(false);
  let slugError = $state('');

  $effect(() => {
    if (!slugManuallyEdited) {
      newSlug = slugify(newName);
    }
  });

  function openCreate(group: number | null) {
    newName = '';
    newSlug = '';
    slugManuallyEdited = false;
    newDescription = '';
    newGroupId = group;
    newLayoutMode = 'fit';
    slugError = '';
    showCreateModal = true;
  }

  async function createDashboard() {
    if (!newName.trim()) return;
    creating = true;
    slugError = '';
    try {
      const payload: Partial<Dashboard> & { group_id?: number } = {
        name: newName,
        slug: newSlug,
        description: newDescription,
        layout_mode: newLayoutMode,
        grid_rows: 12,
        grid_cols: 12,
        row_height: 80,
        is_public: false,
      };
      if (newGroupId) {
        payload.group_id = newGroupId;
      }
      const dashboard = await dashboards.create(payload);
      showCreateModal = false;
      navigate(`/dashboard/${dashboard.id}/edit`);
    } catch (e) {
      const err = e as ApiError;
      if (err.code === 'CONFLICT') {
        slugError = err.message;
      } else {
        showToast('Failed to create dashboard');
      }
    } finally {
      creating = false;
    }
  }

  async function deleteDashboard(dashboard: Dashboard) {
    if (!await confirm(`Delete dashboard "${dashboard.name}"? This cannot be undone.`)) return;
    try {
      await dashboards.remove(dashboard.id);
    } catch {
      showToast('Failed to delete dashboard');
    }
  }

  // --- Group actions ---

  let showGroupModal = $state(false);
  let editingGroup = $state<DashboardGroup | null>(null);
  let groupName = $state('');
  let groupDescription = $state('');
  let groupParentId = $state<number | null>(null);
  let savingGroup = $state(false);
  let groupError = $state('');

  function openGroupModal(group: DashboardGroup | null, parentId: number | null = null) {
    editingGroup = group;
    groupName = group?.name || '';
    groupDescription = group?.description || '';
    groupParentId = group ? (group.parent_id ?? null) : parentId;
    groupError = '';
    showGroupModal = true;
  }

  async function saveGroup() {
    if (!groupName.trim()) return;
    savingGroup = true;
    groupError = '';
    try {
      if (editingGroup) {
        await dashboardGroupsStore.update(editingGroup.id, {
          name: groupName,
          description: groupDescription,
          parent_id: groupParentId,
        });
      } else {
        await dashboardGroupsStore.create(groupName, groupDescription, groupParentId);
      }
      showGroupModal = false;
      await loadAll();
    } catch (e: any) {
      groupError = e?.message || 'Failed to save folder';
    } finally {
      savingGroup = false;
    }
  }

  async function deleteGroup(group: DashboardGroup) {
    if (!await confirm(`Delete folder "${group.name}"? Dashboards inside move to the root, not the trash.`)) return;
    try {
      await dashboardGroupsStore.delete(group.id);
      if (folderId === group.id) folderId = group.parent_id ?? null;
      await loadAll();
    } catch {
      showToast('Failed to delete folder');
    }
  }

  // --- Import ---

  let importInput = $state<HTMLInputElement | undefined>(undefined);

  // Pins are the overview's quick launch, kept in the user's preferences.
  let pins = $derived(pinnedIds($preferences.pinned_dashboards));

  function togglePin(id: string) {
    auth.setPreferences({ pinned_dashboards: pins.includes(id) ? pins.filter((p) => p !== id) : [...pins, id] });
  }
  let importing = $state(false);

  async function handleImportFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    importing = true;
    try {
      const doc = JSON.parse(await file.text());
      const dashboard = await dashboards.importDoc(doc);
      navigate(`/dashboard/${dashboard.id}/edit`);
    } catch (err: any) {
      showToast(err?.message || 'Failed to import dashboard: the file is not a valid export document');
    } finally {
      importing = false;
    }
  }

  let groupMoveOptions = $derived(
    editingGroup
      ? allGroupOptions.filter((o) => !descendantIDs(editingGroup!.id).has(o.id))
      : allGroupOptions,
  );
</script>

{#if !$isAuthenticated}
  <div class="welcome-screen">
    <div class="welcome-content">
      <i class="las la-th-large welcome-icon"></i>
      <h1 class="welcome-title">Welcome to Karakuri</h1>
      <p class="welcome-desc">Sign in to view and manage your dashboards.</p>
      <button class="btn btn-primary welcome-btn" onclick={() => navigate('/login')}>
        <i class="las la-sign-in-alt mr-2"></i>Log in
      </button>
    </div>
  </div>
{:else}
  <div class="p-6 max-w-6xl mx-auto w-full">
    <PageHeader title="Dashboards" />

    <!-- The file input lives outside the toolbar snippet: the snippet
         re-renders as the browsed folder changes, and the binding must
         survive that. -->
    <input
      type="file"
      accept=".json,application/json"
      style="display: none;"
      bind:this={importInput}
      onchange={handleImportFile}
    />

    <BrowseList
      folders={browseFolders}
      items={browseItems}
      noun="Dashboard"
      nounPlural="Dashboards"
      {loading}
      bind:folderId
      onOpenItem={(item) => {
        const d = dashboardOf(item);
        if (d) navigate(`/dashboard/${d.id}/edit`);
      }}
      onMoveItem={moveDashboard}
      onMoveFolder={moveGroup}
      {toolbar}
      {itemMark}
      {itemBadges}
      {itemActions}
      {folderActions}
      {emptyState}
    />
  </div>
{/if}

{#snippet toolbar(current: number | null)}
  <button
    class="btn btn-secondary btn-sm"
    disabled={importing}
    onclick={() => importInput?.click()}
    title="Import a dashboard from an exported JSON document"
  >
    <i class="las la-file-import mr-1"></i> {importing ? 'Importing...' : 'Import'}
  </button>
  <button class="btn btn-secondary btn-sm" onclick={() => openGroupModal(null, current)}>
    <i class="las la-folder-plus mr-1"></i> New folder
  </button>
  <button class="btn btn-primary btn-sm" onclick={() => openCreate(current)}>
    <i class="las la-plus mr-1"></i> New dashboard
  </button>
{/snippet}

{#snippet itemMark(_item: BrowseItem)}
  <i class="las la-th-large dash-icon"></i>
{/snippet}

{#snippet itemBadges(item: BrowseItem)}
  {@const dashboard = dashboardOf(item)}
  {#if dashboard?.is_public}
    <span class="badge badge-public"><i class="las la-globe mr-1"></i>Public</span>
  {/if}
{/snippet}

{#snippet itemActions(item: BrowseItem)}
  {@const dashboard = dashboardOf(item)}
  {#if dashboard}
    <button
      class="icon-btn"
      class:pinned={pins.includes(dashboard.id)}
      title={pins.includes(dashboard.id) ? 'Unpin from the overview' : 'Pin to the overview'}
      aria-pressed={pins.includes(dashboard.id)}
      onclick={() => togglePin(dashboard.id)}
    >
      <i class="las la-thumbtack"></i>
    </button>
    <button class="icon-btn" title="View" onclick={() => navigate(`/dashboard/${dashboard.id}`)}>
      <i class="las la-eye"></i>
    </button>
    <button class="icon-btn" title="Edit" onclick={() => navigate(`/dashboard/${dashboard.id}/edit`)}>
      <i class="las la-edit"></i>
    </button>
    <button class="icon-btn" title="Share" onclick={() => navigate(`/dashboard/${dashboard.id}/share`)}>
      <i class="las la-share-alt"></i>
    </button>
    <button class="icon-btn" title="Render" onclick={() => navigate(`/dashboard/${dashboard.id}/render`)}>
      <i class="las la-camera"></i>
    </button>
    <button class="icon-btn danger" title="Delete" onclick={() => deleteDashboard(dashboard)}>
      <i class="las la-trash"></i>
    </button>
  {/if}
{/snippet}

{#snippet folderActions(folder: BrowseFolder)}
  {@const group = groupRecord(folder)}
  {#if group && group.owner_id === userId}
    <button class="icon-btn" title="New dashboard here" onclick={() => openCreate(folder.id)}>
      <i class="las la-plus"></i>
    </button>
    <button class="icon-btn" title="Rename or move" onclick={() => openGroupModal(group)}>
      <i class="las la-pen"></i>
    </button>
    <button class="icon-btn" title="Share folder" onclick={() => navigate(`/dashboard-group/${group.id}/share`)}>
      <i class="las la-share-alt"></i>
    </button>
    <button class="icon-btn danger" title="Delete folder" onclick={() => deleteGroup(group)}>
      <i class="las la-trash"></i>
    </button>
  {/if}
{/snippet}

{#snippet emptyState()}
  <div class="card p-12 flex flex-col items-center justify-center text-center gap-3">
    <i class="las la-th-large" style="font-size: 3rem; color: var(--theme-overlay0);"></i>
    <h3 class="text-lg font-semibold">No dashboards yet</h3>
    <p class="text-sm opacity-60 max-w-md">
      A dashboard is a grid of widgets bound to controls, entities, and flows.
      Create your first one, or add a folder to organize them.
    </p>
    <button class="btn btn-primary text-sm" onclick={() => openCreate(null)}>
      <i class="las la-plus mr-1"></i> New Dashboard
    </button>
  </div>
{/snippet}

{#if toast}
  <div class="toast" role="alert">
    <i class="las la-exclamation-circle mr-1"></i>{toast}
  </div>
{/if}

<!-- Create Dashboard Modal -->
{#if showCreateModal}
  <Modal title="New Dashboard" onclose={() => (showCreateModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createDashboard(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="dash-name">
        <input id="dash-name" class="input w-full" bind:value={newName} required />
      </FormField>
      <FormField label="Slug" id="dash-slug" error={slugError}>
        <input
          id="dash-slug"
          class="input w-full"
          bind:value={newSlug}
          oninput={() => { slugManuallyEdited = true; slugError = ''; }}
          onblur={() => { if (!newSlug.trim()) { slugManuallyEdited = false; newSlug = slugify(newName); } }}
        />
      </FormField>
      <FormField label="Description" id="dash-desc">
        <textarea id="dash-desc" class="input w-full" rows="3" bind:value={newDescription}></textarea>
      </FormField>
      <FormField label="Layout" id="dash-layout">
        <select id="dash-layout" class="input w-full" bind:value={newLayoutMode}>
          <option value="fit">Fit to screen — the whole grid is always visible</option>
          <option value="scroll">Scrolling — fixed row height, sections, overflows below</option>
        </select>
      </FormField>
      <FormField label="Folder" id="dash-group">
        <select id="dash-group" class="input w-full" bind:value={newGroupId}>
          <option value={null}>No folder</option>
          {#each allGroupOptions as opt}
            <option value={opt.id}>{opt.label}</option>
          {/each}
        </select>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary" disabled={creating}>
          {creating ? 'Creating...' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Group Modal -->
{#if showGroupModal}
  <Modal title={editingGroup ? 'Edit Folder' : 'New Folder'} onclose={() => (showGroupModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveGroup(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="grp-name" error={groupError}>
        <input id="grp-name" class="input w-full" bind:value={groupName} required />
      </FormField>
      <FormField label="Description" id="grp-desc">
        <textarea id="grp-desc" class="input w-full" rows="3" bind:value={groupDescription}></textarea>
      </FormField>
      <FormField label="Parent Folder" id="grp-parent">
        <select id="grp-parent" class="input w-full" bind:value={groupParentId}>
          <option value={null}>No parent (root)</option>
          {#each groupMoveOptions as opt}
            <option value={opt.id}>{opt.label}</option>
          {/each}
        </select>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary" disabled={savingGroup}>
          {savingGroup ? 'Saving...' : editingGroup ? 'Save' : 'Create'}
        </button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  .icon-btn.pinned {
    color: var(--theme-accent);
  }
  .welcome-screen {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 60vh;
  }

  .welcome-content {
    text-align: center;
    max-width: 24rem;
  }

  .welcome-icon {
    font-size: 3rem;
    color: var(--theme-accent, #89b4fa);
    margin-bottom: 1rem;
    display: block;
  }

  .welcome-title {
    font-size: 1.75rem;
    font-weight: 800;
    letter-spacing: -0.02em;
    margin-bottom: 0.5rem;
  }

  .welcome-desc {
    color: var(--theme-subtext0, #a6adc8);
    margin-bottom: 1.5rem;
  }

  .welcome-btn {
    padding: 0.625rem 1.5rem;
    font-size: 1rem;
  }

  .dash-icon {
    color: var(--theme-accent, #89b4fa);
    flex-shrink: 0;
  }

  .badge {
    display: inline-flex;
    align-items: center;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid;
    font-size: 0.65rem;
    white-space: nowrap;
  }

  .badge-public {
    color: var(--theme-accent, #89b4fa);
    border-color: var(--theme-accent, #89b4fa);
  }

  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    border-radius: 4px;
    border: none;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
  }

  .icon-btn:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .icon-btn.danger:hover {
    background-color: var(--theme-error, #f38ba8);
    color: var(--theme-base, #1e1e2e);
  }

  .toast {
    position: fixed;
    bottom: 1.25rem;
    left: 50%;
    transform: translateX(-50%);
    z-index: 60;
    max-width: 28rem;
    padding: 0.6rem 1rem;
    border-radius: var(--theme-radius-md, 8px);
    background-color: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-warning, #f9e2af);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.8rem;
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  }
</style>
