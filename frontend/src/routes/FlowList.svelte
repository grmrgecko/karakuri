<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { navigate } from 'svelte-routing';
  import { auth, isAuthenticated } from '../lib/stores/auth';
  import { canFlow, flowsStore } from '../lib/stores/flows';
  import type { FlowRecord } from '../lib/stores/flows';
  import { foldersStore } from '../lib/stores/folders';
  import type { FolderRecord } from '../lib/stores/folders';
  import { confirm } from '../lib/stores/confirmDialog';
  import Modal from '../lib/components/Modal.svelte';
  import FormField from '../lib/components/FormField.svelte';
  import PageHeader from '../lib/components/PageHeader.svelte';
  import BrowseList from '../lib/components/BrowseList.svelte';
  import type { BrowseFolder, BrowseItem } from '../lib/components/BrowseList.svelte';
  import { api } from '../lib/api/client';
  import { relTime } from '../lib/utils/time';
  import { watchLists, watchRuns } from '../lib/liveList';
  import type { RunEvent } from '../lib/liveList';

  let flows: FlowRecord[] = $state([]);
  let folders: FolderRecord[] = $state([]);
  let flowsLoading = $state(true);

  // The browsed folder survives a reload, so returning to the list lands where
  // the last one left off rather than at the root. It is read during init, not
  // in onMount: the effect below runs first and would persist the default over
  // the saved value.
  const folderKey = 'karakuri.flowList.folder';

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

  // Transient toast for list-level errors (e.g. deleting a referenced subflow).
  let toast = $state('');
  let toastTimer: ReturnType<typeof setTimeout> | null = null;

  function showToast(message: string) {
    toast = message;
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 6000);
  }

  // lastRuns maps flow id to a summary of its last persisted run.
  interface LastRunSummary {
    status: string;
    at: string | null;
    had_failures: boolean;
  }
  let lastRuns: Record<string, LastRunSummary> = $state({});

  // activeRuns holds each flow's runs in progress. A flow can run more than
  // once at a time, and its dot stays running until the last one finishes.
  const activeRuns = new Map<string, Set<string>>();

  // lastRunRead holds flows whose last run has been read, including flows
  // with none, so a quiet reload asks only about flows that are new to it.
  const lastRunRead = new Set<string>();

  // loadLastRuns lazily fetches each flow's last run; flows without one 404.
  // Subflows never run on their own, so they are skipped. With onlyNew it
  // reads only flows the list has no run for yet, since the runs topic keeps
  // the others current.
  async function loadLastRuns(list: FlowRecord[], onlyNew = false) {
    const wanted = list.filter((f) => !f.is_subflow && !(onlyNew && lastRunRead.has(f.id)));
    for (const f of wanted) lastRunRead.add(f.id);
    const results = await Promise.all(
      wanted.map(async (f) => {
        try {
          const detail = await api.flows.lastRun<{ run: { id: string; status: string; started_at: string; ended_at?: string | null; had_failures?: boolean } }>(f.id);
          if (detail.run.status === 'running') trackRun(f.id, detail.run.id, true);
          return [f.id, {
            status: detail.run.status,
            at: detail.run.ended_at ?? detail.run.started_at ?? null,
            had_failures: !!detail.run.had_failures,
          }] as const;
        } catch {
          return null;
        }
      })
    );
    const next: Record<string, LastRunSummary> = onlyNew ? { ...lastRuns } : {};
    for (const r of results) {
      // A run event that arrived during the read is newer than the read.
      if (r && !(onlyNew && r[0] in lastRuns)) next[r[0]] = r[1];
    }
    lastRuns = next;
  }

  function trackRun(flowId: string, runId: string, running: boolean) {
    let runs = activeRuns.get(flowId);
    if (running) {
      if (!runs) activeRuns.set(flowId, (runs = new Set()));
      runs.add(runId);
    } else if (runs) {
      runs.delete(runId);
      if (runs.size === 0) activeRuns.delete(flowId);
    }
  }

  // onRun moves a flow's dot as its runs start and finish: running while any
  // run is in progress, then the status of the run that finished last.
  function onRun(ev: RunEvent) {
    const { run } = ev;
    const started = ev.event === 'run_started';
    trackRun(run.flow_id, run.id, started);
    if (!started && activeRuns.has(run.flow_id)) return;
    lastRuns = {
      ...lastRuns,
      [run.flow_id]: {
        status: started ? 'running' : run.status,
        at: started ? run.started_at : (run.ended_at ?? run.started_at),
        had_failures: !started && !!run.had_failures,
      },
    };
  }

  function runDotTitle(s: LastRunSummary): string {
    if (s.status === 'running') return s.at ? `Running, started ${relTime(s.at)}` : 'Running';
    let t = `Last run ${s.status}`;
    if (s.had_failures) t += ' (had failures)';
    if (s.at) t += `, ${relTime(s.at)}`;
    return t;
  }

  $effect(() => {
    if ($auth.initialized && !$isAuthenticated) {
      navigate('/login', { replace: true });
    }
  });

  // Runs and changes made in other sessions arrive over the socket: run
  // events move the dots, and a list notice reads the flows and folders again.
  let stopLive: (() => void)[] = [];

  onMount(() => {
    loadAll();
    stopLive = [watchRuns(onRun), watchLists(['flows'], () => loadAll(true))];
  });

  onDestroy(() => {
    for (const stop of stopLive) stop();
  });

  // loadAll reads the flows and folders. A quiet reload keeps the list on
  // screen instead of showing the loading state.
  async function loadAll(quiet = false) {
    if (!quiet) flowsLoading = true;
    try {
      const [flowList, folderList] = await Promise.all([
        flowsStore.listAll(),
        foldersStore.list(),
      ]);
      flows = flowList || [];
      folders = folderList || [];
      // A folder deleted elsewhere would leave the view pointing at nothing.
      if (folderId !== null && !folders.some((f) => f.id === folderId)) folderId = null;
      void loadLastRuns(flows, quiet);
    } catch { /* handled by empty state */ }
    flowsLoading = false;
  }

  // --- Shapes the browser understands ---

  let browseFolders = $derived<BrowseFolder[]>(
    folders.map((f) => ({ id: f.id, name: f.name, parentId: f.parent_id ?? null })),
  );

  let browseItems = $derived<BrowseItem[]>(
    flows.map((f) => ({
      id: f.id,
      name: f.name,
      description: f.description,
      folderId: f.folder_id ?? null,
    })),
  );

  function flowOf(item: BrowseItem): FlowRecord | undefined {
    return flows.find((f) => f.id === item.id);
  }

  // folderOptions flattens the tree into "Parent / Child" labels for pickers.
  function folderOptions(parentId: number | null = null, prefix = ''): { id: number; label: string }[] {
    const out: { id: number; label: string }[] = [];
    for (const folder of folders
      .filter((f) => (f.parent_id ?? null) === parentId)
      .sort((a, b) => a.name.localeCompare(b.name))) {
      const label = prefix ? `${prefix} / ${folder.name}` : folder.name;
      out.push({ id: folder.id, label });
      out.push(...folderOptions(folder.id, label));
    }
    return out;
  }

  let allFolderOptions = $derived(folderOptions());

  // descendantIDs returns a folder and everything beneath it, so a move picker
  // can exclude the destinations that would create a cycle.
  function descendantIDs(id: number): Set<number> {
    const out = new Set<number>([id]);
    let grew = true;
    while (grew) {
      grew = false;
      for (const f of folders) {
        const parent = f.parent_id ?? null;
        if (parent !== null && out.has(parent) && !out.has(f.id)) {
          out.add(f.id);
          grew = true;
        }
      }
    }
    return out;
  }

  // --- Moves ---

  async function moveFlow(item: BrowseItem, target: number | null) {
    await flowsStore.move(item.id, target);
    flows = flows.map((f) => (f.id === item.id ? { ...f, folder_id: target ?? undefined } : f));
  }

  async function moveFolder(folder: BrowseFolder, parentId: number | null) {
    await foldersStore.update(folder.id, folder.name, parentId);
    folders = folders.map((f) => (f.id === folder.id ? { ...f, parent_id: parentId ?? undefined } : f));
  }

  // --- Flow actions ---

  let showCreate = $state(false);
  let createSubflow = $state(false);
  let newName = $state('');
  let newDescription = $state('');
  let newFlowFolderId = $state<number | null>(null);
  let createError = $state('');

  function openCreate(isSubflow = false, folder: number | null = null) {
    createSubflow = isSubflow;
    newName = '';
    newDescription = '';
    newFlowFolderId = folder;
    createError = '';
    showCreate = true;
  }

  async function createFlow() {
    createError = '';
    try {
      const flow = await flowsStore.create(newName, newDescription, createSubflow, newFlowFolderId);
      showCreate = false;
      navigate(`/flows/${flow.id}`);
    } catch (e: any) {
      createError = e.message || 'Failed to create flow';
    }
  }

  async function toggleEnabled(flow: FlowRecord) {
    try {
      await flowsStore.setEnabled(flow.id, !flow.enabled);
      flows = (await flowsStore.listAll()) || flows;
    } catch (e: any) {
      showToast(e.message || 'Failed to update flow');
      await loadAll();
    }
  }

  async function deleteFlow(flow: FlowRecord) {
    const kind = flow.is_subflow ? 'subflow' : 'flow';
    if (!await confirm(`Delete ${kind} "${flow.name}"? This cannot be undone.`)) return;
    try {
      await flowsStore.remove(flow.id);
      await loadAll();
    } catch (e: any) {
      showToast(e.message || `Failed to delete ${kind}`);
    }
  }

  // --- Folder actions ---

  let showFolderModal = $state(false);
  let editingFolder = $state<FolderRecord | null>(null);
  let folderName = $state('');
  let folderParentId = $state<number | null>(null);
  let folderError = $state('');

  function openFolderModal(folder: FolderRecord | null, parentId: number | null = null) {
    editingFolder = folder;
    folderName = folder?.name || '';
    folderParentId = folder ? (folder.parent_id ?? null) : parentId;
    folderError = '';
    showFolderModal = true;
  }

  async function saveFolder() {
    folderError = '';
    try {
      if (editingFolder) {
        await foldersStore.update(editingFolder.id, folderName, folderParentId);
      } else {
        await foldersStore.create(folderName, folderParentId);
      }
      showFolderModal = false;
      await loadAll();
    } catch (e: any) {
      folderError = e.message || 'Failed to save folder';
    }
  }

  async function deleteFolder(folder: FolderRecord) {
    if (!await confirm(`Delete folder "${folder.name}"?`)) return;
    try {
      await foldersStore.remove(folder.id);
      if (folderId === folder.id) folderId = folder.parent_id ?? null;
      await loadAll();
    } catch (e: any) {
      showToast(e.message || 'Failed to delete folder');
    }
  }

  // Destinations offered when moving a folder, minus its own subtree.
  let folderMoveOptions = $derived(
    editingFolder
      ? allFolderOptions.filter((o) => !descendantIDs(editingFolder!.id).has(o.id))
      : allFolderOptions,
  );

  function folderRecord(folder: BrowseFolder): FolderRecord | undefined {
    return folders.find((f) => f.id === folder.id);
  }

  // --- Import ---

  let importInput = $state<HTMLInputElement | undefined>(undefined);
  let importing = $state(false);

  async function handleImportFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    input.value = '';
    if (!file) return;
    importing = true;
    try {
      const doc = JSON.parse(await file.text());
      const flow = await api.flows.importDoc<{ id: string }>(doc);
      navigate(`/flows/${flow.id}`);
    } catch (err: any) {
      showToast(err?.message || 'Failed to import flow: the file is not a valid export document');
    } finally {
      importing = false;
    }
  }
</script>

<div class="p-6 max-w-6xl mx-auto w-full">
  <PageHeader title="Flows" />

  <!-- The file input lives outside the toolbar snippet: the snippet re-renders
       as the browsed folder changes, and the binding must survive that. -->
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
    noun="Flow"
    nounPlural="Flows"
    loading={flowsLoading}
    bind:folderId
    onOpenItem={(item) => navigate(`/flows/${item.id}`)}
    onMoveItem={moveFlow}
    onMoveFolder={moveFolder}
    {toolbar}
    {itemMark}
    {itemBadges}
    {itemMeta}
    {itemActions}
    {folderActions}
    {emptyState}
  />
</div>

{#snippet toolbar(current: number | null)}
  <button
    class="btn btn-secondary btn-sm"
    disabled={importing}
    onclick={() => importInput?.click()}
    title="Import a flow from an exported JSON document"
  >
    <i class="las la-file-import mr-1"></i> {importing ? 'Importing...' : 'Import'}
  </button>
  <button class="btn btn-secondary btn-sm" onclick={() => openFolderModal(null, current)}>
    <i class="las la-folder-plus mr-1"></i> New folder
  </button>
  <!-- New here means new in the folder being browsed, subflows included: a
       subflow is a flow on this list and files the same way. -->
  <button
    class="btn btn-secondary btn-sm"
    onclick={() => openCreate(true, current)}
    title="Reusable library flow other flows can embed"
  >
    <i class="las la-layer-group mr-1"></i> New subflow
  </button>
  <button class="btn btn-primary btn-sm" onclick={() => openCreate(false, current)}>
    <i class="las la-plus mr-1"></i> New flow
  </button>
{/snippet}

{#snippet itemMark(item: BrowseItem)}
  {@const flow = flowOf(item)}
  {#if flow?.is_subflow}
    <i class="las la-layer-group subflow-icon" title="Subflow"></i>
  {:else if lastRuns[item.id]}
    <span
      class="run-dot run-dot-{lastRuns[item.id].status}"
      class:warn={lastRuns[item.id].had_failures}
      title={runDotTitle(lastRuns[item.id])}
    ></span>
  {:else}
    <span class="run-dot run-dot-none" title="No runs yet"></span>
  {/if}
{/snippet}

{#snippet itemBadges(item: BrowseItem)}
  {@const flow = flowOf(item)}
  {#if flow}
    {#if flow.is_subflow}
      <span class="badge badge-subflow" title="Reusable library flow other flows can embed">Subflow</span>
    {/if}
    {#if flow.deployed}
      <span class="badge badge-deployed"><i class="las la-check-circle mr-1"></i>Deployed</span>
      {#if flow.has_draft_changes}
        <span class="badge badge-draft" title="Draft changes not yet deployed">
          <i class="las la-pen"></i> changes
        </span>
      {/if}
    {:else}
      <span class="badge badge-undeployed">Not deployed</span>
    {/if}
  {/if}
{/snippet}

{#snippet itemMeta(item: BrowseItem)}
  {@const flow = flowOf(item)}
  {#if flow && !flow.is_subflow}
    <button
      class="toggle"
      class:on={flow.enabled}
      role="switch"
      aria-checked={flow.enabled}
      aria-label="Enabled"
      disabled={!flow.deployed || !canFlow(flow, 'edit')}
      title={!canFlow(flow, 'edit') ? 'Enabling needs edit access to this flow' : flow.deployed ? (flow.enabled ? 'Enabled' : 'Disabled') : 'Deploy the flow before enabling it'}
      onclick={(e: MouseEvent) => { e.stopPropagation(); toggleEnabled(flow); }}
    >
      <span class="knob"></span>
    </button>
  {/if}
{/snippet}

{#snippet itemActions(item: BrowseItem)}
  {@const flow = flowOf(item)}
  <button class="icon-btn" title="Open" onclick={() => navigate(`/flows/${item.id}`)}>
    <i class="las la-edit"></i>
  </button>
  {#if flow && canFlow(flow, 'admin')}
    <button class="icon-btn" title="Share" onclick={() => navigate(`/flows/${flow.id}/share`)}>
      <i class="las la-share-alt"></i>
    </button>
    <button class="icon-btn danger" title="Delete" onclick={() => deleteFlow(flow)}>
      <i class="las la-trash"></i>
    </button>
  {/if}
{/snippet}

{#snippet folderActions(folder: BrowseFolder)}
  {@const record = folderRecord(folder)}
  {#if record && canFlow(record, 'edit')}
    <button class="icon-btn" title="New flow here" onclick={() => openCreate(false, folder.id)}>
      <i class="las la-plus"></i>
    </button>
    <button class="icon-btn" title="Rename or move" onclick={() => openFolderModal(record)}>
      <i class="las la-pen"></i>
    </button>
  {/if}
  {#if record && canFlow(record, 'admin')}
    <button class="icon-btn" title="Share folder" onclick={() => navigate(`/folders/${record.id}/share`)}>
      <i class="las la-share-alt"></i>
    </button>
    <button class="icon-btn danger" title="Delete folder" onclick={() => deleteFolder(record)}>
      <i class="las la-trash"></i>
    </button>
  {/if}
{/snippet}

{#snippet emptyState()}
  <div class="card p-12 flex flex-col items-center justify-center text-center gap-3">
    <i class="las la-project-diagram" style="font-size: 3rem; color: var(--theme-overlay0);"></i>
    <h3 class="text-lg font-semibold">No flows yet</h3>
    <p class="text-sm opacity-60 max-w-md">
      Flows are automation pipelines that connect triggers, conditions, and actions.
      Create your first flow to open the editor, or add a folder to organize them.
    </p>
    <button class="btn btn-primary text-sm" onclick={() => openCreate(false, null)}>
      <i class="las la-plus mr-1"></i> New Flow
    </button>
  </div>
{/snippet}

{#if toast}
  <div class="toast" role="alert">
    <i class="las la-exclamation-circle mr-1"></i>{toast}
  </div>
{/if}

<!-- Create Flow Modal -->
{#if showCreate}
  <Modal title={createSubflow ? 'New Subflow' : 'New Flow'} onclose={() => (showCreate = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); createFlow(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="nf-name" error={createError}>
        <input id="nf-name" class="input w-full" bind:value={newName} required />
      </FormField>
      <FormField label="Description" id="nf-desc">
        <textarea id="nf-desc" class="input w-full" rows="2" bind:value={newDescription}></textarea>
      </FormField>
      <FormField label="Folder" id="nf-folder">
        <select id="nf-folder" class="input w-full" bind:value={newFlowFolderId}>
          <option value={null}>No folder</option>
          {#each allFolderOptions as opt}
            <option value={opt.id}>{opt.label}</option>
          {/each}
        </select>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">Create</button>
      </div>
    </form>
  </Modal>
{/if}

<!-- Folder Modal -->
{#if showFolderModal}
  <Modal title={editingFolder ? 'Edit Folder' : 'New Folder'} onclose={() => (showFolderModal = false)}>
    <form onsubmit={(e: SubmitEvent) => { e.preventDefault(); saveFolder(); }} class="flex flex-col gap-4">
      <FormField label="Name" id="fd-name" error={folderError}>
        <input id="fd-name" class="input w-full" bind:value={folderName} required />
      </FormField>
      <FormField label="Parent Folder" id="fd-parent">
        <select id="fd-parent" class="input w-full" bind:value={folderParentId}>
          <option value={null}>No parent (root)</option>
          {#each folderMoveOptions as opt}
            <option value={opt.id}>{opt.label}</option>
          {/each}
        </select>
      </FormField>
      <div class="flex justify-end">
        <button type="submit" class="btn btn-primary">{editingFolder ? 'Save' : 'Create'}</button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  .subflow-icon {
    color: var(--theme-mauve, #cba6f7);
    flex-shrink: 0;
  }

  .badge {
    display: inline-flex;
    align-items: center;
    gap: 0.15rem;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid;
    font-size: 0.65rem;
    white-space: nowrap;
  }

  .badge-deployed {
    color: var(--theme-success, #a6e3a1);
    border-color: var(--theme-success, #a6e3a1);
  }

  .badge-undeployed {
    color: var(--theme-overlay0, #6c7086);
    border-color: var(--theme-overlay0, #6c7086);
  }

  .badge-draft {
    color: var(--theme-warning, #f9e2af);
    border-color: var(--theme-warning, #f9e2af);
  }

  .badge-subflow {
    color: var(--theme-mauve, #cba6f7);
    border-color: var(--theme-mauve, #cba6f7);
  }

  .run-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .run-dot-running { background-color: var(--theme-peach, #fab387); }
  .run-dot-succeeded { background-color: var(--theme-success, #a6e3a1); }
  .run-dot-succeeded.warn { background-color: var(--theme-warning, #f9e2af); }
  .run-dot-failed { background-color: var(--theme-error, #f38ba8); }
  .run-dot-canceled { background-color: var(--theme-overlay0, #6c7086); }
  .run-dot-none { background-color: transparent; border: 1px solid var(--theme-overlay0, #6c7086); }

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

  .toggle {
    position: relative;
    width: 32px;
    height: 18px;
    border-radius: 999px;
    background-color: var(--theme-surface1, #45475a);
    border: none;
    transition: background-color 150ms;
    padding: 0;
    flex-shrink: 0;
  }

  .toggle.on {
    background-color: var(--theme-success, #a6e3a1);
  }

  .toggle:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .toggle .knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background-color: var(--theme-text, #cdd6f4);
    transition: transform 150ms;
  }

  .toggle.on .knob {
    transform: translateX(14px);
    background-color: var(--theme-base, #1e1e2e);
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
