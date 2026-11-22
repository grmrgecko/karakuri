<script lang="ts">
  import { navigate } from 'svelte-routing';
  import { SvelteFlowProvider } from '@xyflow/svelte';
  import { get } from 'svelte/store';
  import { auth, isAuthenticated } from '../lib/stores/auth';
  import { nodeCatalog } from '../lib/stores/nodeCatalog';
  import { editor } from '../lib/stores/editor';
  import { runState } from '../lib/stores/runState';
  import { subflowLibrary } from '../lib/stores/subflows';
  import { scriptSdkCatalog } from '../lib/stores/scriptSdkCatalog';
  import { api } from '../lib/api/client';
  import EditorShell from '../lib/components/editor/EditorShell.svelte';
  import FaceplatePanel, { type Tab } from '../lib/components/faceplate/FaceplatePanel.svelte';

  let { id }: { id: string } = $props();

  interface FaceplateView {
    flow_id: string;
    name: string;
    access_level: 'faceplate' | 'view' | 'edit' | 'admin';
    source: string;
    tabs: Tab[];
  }

  let ready = $state(false);
  let loadError = $state('');
  let faceplates = $state<FaceplateView | null>(null);
  // active is "editor" or a faceplate key. A flow with faceplates opens on
  // its first one; the editor tab comes first in the bar.
  let active = $state('editor');
  let editorLoaded = $state(false);
  // editorShown records that the editor tab has been opened. The canvas is
  // mounted then, not before: laid out while hidden, it would measure itself
  // as zero-sized and place the graph off screen.
  let editorShown = $state(false);

  let level = $derived(faceplates?.access_level ?? '');
  let canView = $derived(level === 'view' || level === 'edit' || level === 'admin');
  let canEdit = $derived(level === 'edit' || level === 'admin');
  let tabs = $derived(faceplates?.tabs ?? []);

  $effect(() => {
    if ($auth.initialized && !$isAuthenticated) {
      navigate('/login', { replace: true });
    }
  });

  async function loadFaceplates(): Promise<FaceplateView | null> {
    try {
      return await api.get<FaceplateView>(`/flows/${id}/faceplates`);
    } catch (e: any) {
      loadError = e?.message || 'Flow not found';
      return null;
    }
  }

  async function loadEditor(flowId: string) {
    try {
      await nodeCatalog.load();
    } catch {
      // The load error surfaces through the editor state below.
    }
    // The palette's subflow library loads alongside; failures leave it empty.
    void subflowLibrary.load();
    void scriptSdkCatalog.refresh().catch(() => {});
    const flow = await editor.load(flowId);
    // Run overlay: live run notifications plus the last persisted run.
    if (flow) runState.open(flowId);
    editorLoaded = true;
  }

  // Load the faceplates, which every level may read, then the editor for
  // those who may see the graph.
  $effect(() => {
    const flowId = id;
    ready = false;
    editorLoaded = false;
    editorShown = false;
    seenRevision = '';
    loadError = '';
    (async () => {
      const view = await loadFaceplates();
      faceplates = view;
      active = view && view.tabs.length > 0 ? view.tabs[0].key : 'editor';
      // Set here rather than derived from a change to active: a flow without
      // faceplates starts on the editor tab, so active never changes.
      editorShown = active === 'editor';
      const viewer = view && view.access_level !== 'faceplate';
      if (viewer) await loadEditor(flowId);
      ready = true;
    })();
    return () => {
      editor.reset();
      runState.close();
    };
  });

  // Switching to a faceplate tab rereads the faceplates, so controls added in
  // the editor appear. It waits for a pending autosave to land first.
  async function select(key: string) {
    if (key === active) return;
    if (key !== 'editor' && canView) {
      for (let i = 0; i < 30 && (get(editor).dirty || get(editor).saving); i++) {
        await new Promise((r) => setTimeout(r, 100));
      }
      const view = await loadFaceplates();
      if (view) faceplates = view;
    }
    if (key === 'editor') editorShown = true;
    active = key;
  }

  // Each saved draft may add, rename, or remove a Faceplate node, so the tab
  // bar rereads the faceplates when the draft revision moves. A tab whose
  // faceplate was removed falls back to the editor.
  let seenRevision = '';
  $effect(() => {
    const revision = $editor.flow?.draft_revision ?? '';
    if (!ready || !canView || !revision || revision === seenRevision) return;
    const first = seenRevision === '';
    seenRevision = revision;
    if (first) return;
    loadFaceplates().then((view) => {
      if (!view) return;
      faceplates = view;
      if (active !== 'editor' && !view.tabs.some((t) => t.key === active)) active = 'editor';
    });
  });

  async function refreshAfterApply() {
    const view = await loadFaceplates();
    if (view) faceplates = view;
  }

  // Editors reorder tabs; the order lands in the draft.
  async function moveTab(index: number, direction: number) {
    const own = tabs.filter((t) => !t.key.includes('>'));
    const keys = own.map((t) => t.key);
    const at = keys.indexOf(tabs[index].key);
    const to = at + direction;
    if (at < 0 || to < 0 || to >= keys.length) return;
    [keys[at], keys[to]] = [keys[to], keys[at]];
    try {
      await api.post(`/flows/${id}/faceplates/order`, { keys });
      await refreshAfterApply();
    } catch (e: any) {
      loadError = e?.message || 'The tab order could not be saved';
    }
  }

  function handleBeforeUnload(event: BeforeUnloadEvent) {
    if ($editor.dirty) {
      event.preventDefault();
    }
  }
</script>

<svelte:window onbeforeunload={handleBeforeUnload} />

{#if !ready || (canView && !editorLoaded) || $editor.loading}
  <div class="flex items-center justify-center flex-1">
    <div class="animate-pulse text-text-muted">Loading flow...</div>
  </div>
{:else if !faceplates || (canView && !$editor.flow)}
  <div class="flex flex-col items-center justify-center flex-1 gap-3">
    <i class="las la-unlink" style="font-size: 3rem; color: var(--theme-overlay0);"></i>
    <p class="opacity-70">{$editor.error || loadError || 'Flow not found'}</p>
    <button class="btn btn-secondary btn-sm" onclick={() => navigate('/flows')}>Back to flows</button>
  </div>
{:else}
  <div class="flex flex-col flex-1 min-h-0">
    {#if tabs.length > 0}
      <div class="tabbar" role="tablist" aria-label="Flow pages">
        {#if !canView}<span class="flow-name">{faceplates.name}</span>{/if}
        {#if canView}
          <button role="tab" class="tab" class:on={active === 'editor'} aria-selected={active === 'editor'} onclick={() => select('editor')}>
            <i class="las la-project-diagram mr-1"></i>Flow editor
          </button>
        {/if}
        {#each tabs as tab, i (tab.key)}
          <span class="tab-wrap">
            <button role="tab" class="tab" class:on={active === tab.key} aria-selected={active === tab.key} onclick={() => select(tab.key)}>
              <i class="las la-sliders-h mr-1"></i>{tab.name || 'Untitled'}
              {#if tab.subflow}<span class="opacity-50 text-xs ml-1">({tab.subflow})</span>{/if}
            </button>
            {#if canEdit && active === tab.key && !tab.key.includes('>')}
              {@const own = tabs.filter((t) => !t.key.includes('>'))}
              {@const at = own.findIndex((t) => t.key === tab.key)}
              <button class="move" title="Move tab left" disabled={at <= 0} onclick={() => moveTab(i, -1)}><i class="las la-angle-left"></i></button>
              <button class="move" title="Move tab right" disabled={at >= own.length - 1} onclick={() => moveTab(i, 1)}><i class="las la-angle-right"></i></button>
            {/if}
          </span>
        {/each}
      </div>
    {/if}

    {#if canView && $editor.flow && editorShown}
      <!-- The editor stays mounted while a faceplate tab is open, so its
           canvas, selection, and undo history survive the switch. -->
      <div class="flex flex-col flex-1 min-h-0" class:hidden={active !== 'editor'}>
        <SvelteFlowProvider>
          <EditorShell flow={$editor.flow} />
        </SvelteFlowProvider>
      </div>
    {/if}
    {#if active !== 'editor'}
      <div class="flex-1 min-h-0 overflow-y-auto">
        <FaceplatePanel flowId={faceplates.flow_id} {tabs} activeKey={active} canApply={level !== 'view'} onApplied={refreshAfterApply} />
      </div>
    {/if}
  </div>
{/if}

<style>
  .tabbar {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.35rem 0.75rem 0;
    border-bottom: 1px solid var(--theme-surface0);
    background-color: var(--theme-crust, var(--theme-mantle));
    overflow-x: auto;
  }
  .flow-name {
    font-weight: 700;
    margin-right: 0.75rem;
    white-space: nowrap;
  }
  .tab-wrap {
    display: flex;
    align-items: center;
  }
  .tab {
    padding: 0.4rem 0.9rem;
    font-size: 0.85rem;
    border-radius: 0.4rem 0.4rem 0 0;
    border: 1px solid transparent;
    border-bottom: none;
    white-space: nowrap;
    opacity: 0.75;
  }
  .tab.on {
    background-color: var(--theme-mantle);
    border-color: var(--theme-surface0);
    opacity: 1;
    font-weight: 600;
  }
  .move {
    padding: 0 0.2rem;
    opacity: 0.6;
  }
  .move:disabled {
    opacity: 0.2;
  }
  .hidden {
    display: none;
  }
</style>
