<script lang="ts" module>
  // BrowseList is the folder-browsing card list behind both the flow list and
  // the dashboard list. One folder is on screen at a time: subfolders and items
  // share a single grid, clicking a folder walks into it, and the breadcrumb
  // walks back out. Cards drag onto a folder card to move in, or onto a
  // breadcrumb crumb to move out — the crumbs are the only way back up, so they
  // are the natural drop target for "take this out of here".
  //
  // The two lists differ in their badges and row actions, not their layout, so
  // those arrive as snippets and everything structural lives here.

  /** BrowseFolder is a folder as this component needs it, whatever the backend calls it. */
  export interface BrowseFolder {
    id: number;
    name: string;
    parentId: number | null;
    /** Free-text note rendered as a chip, e.g. "shared by dana". */
    note?: string;
    /** False for a folder the viewer may not restructure (someone else's share). */
    canEdit?: boolean;
  }

  /** BrowseItem is one flow or dashboard. */
  export interface BrowseItem {
    id: string;
    name: string;
    description?: string;
    folderId: number | null;
  }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    folders,
    items,
    noun,
    nounPlural,
    loading = false,
    folderId = $bindable(null),
    onOpenItem,
    onMoveItem,
    onMoveFolder,
    onOpenFolder,
    toolbar,
    itemMark,
    itemBadges,
    itemMeta,
    itemActions,
    folderActions,
    emptyState,
  }: {
    folders: BrowseFolder[];
    items: BrowseItem[];
    noun: string;
    nounPlural: string;
    loading?: boolean;
    /** The folder being browsed; bindable so the page can restore or reset it. */
    folderId?: number | null;
    onOpenItem: (item: BrowseItem) => void;
    onMoveItem: (item: BrowseItem, folderId: number | null) => Promise<void> | void;
    onMoveFolder?: (folder: BrowseFolder, parentId: number | null) => Promise<void> | void;
    onOpenFolder?: (folder: BrowseFolder) => void;
    toolbar?: Snippet<[number | null]>;
    itemMark?: Snippet<[BrowseItem]>;
    itemBadges?: Snippet<[BrowseItem]>;
    itemMeta?: Snippet<[BrowseItem]>;
    itemActions?: Snippet<[BrowseItem]>;
    folderActions?: Snippet<[BrowseFolder]>;
    emptyState?: Snippet;
  } = $props();

  let query = $state('');
  let trimmed = $derived(query.trim().toLowerCase());

  // --- Tree lookups ---

  function folderById(id: number | null): BrowseFolder | undefined {
    return id === null ? undefined : folders.find((f) => f.id === id);
  }

  let childFolders = $derived(
    folders
      .filter((f) => f.parentId === folderId)
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  let childItems = $derived(
    items
      .filter((i) => i.folderId === folderId)
      .sort((a, b) => a.name.localeCompare(b.name)),
  );

  // path is the breadcrumb, root first. A folder whose parent has vanished
  // stops the walk rather than looping.
  let path = $derived.by(() => {
    const out: BrowseFolder[] = [];
    let cursor = folderById(folderId);
    const seen = new Set<number>();
    while (cursor && !seen.has(cursor.id)) {
      seen.add(cursor.id);
      out.unshift(cursor);
      cursor = folderById(cursor.parentId);
    }
    return out;
  });

  function pathLabel(id: number | null): string {
    const out: string[] = [];
    let cursor = folderById(id);
    const seen = new Set<number>();
    while (cursor && !seen.has(cursor.id)) {
      seen.add(cursor.id);
      out.unshift(cursor.name);
      cursor = folderById(cursor.parentId);
    }
    return out.length ? out.join(' / ') : nounPlural;
  }

  // countAll is what a folder card reports: everything beneath it, not just
  // what it holds directly, so a folder of folders does not read as empty.
  function countAll(id: number): number {
    let total = items.filter((i) => i.folderId === id).length;
    for (const child of folders.filter((f) => f.parentId === id)) {
      total += countAll(child.id);
    }
    return total;
  }

  function isDescendant(candidateId: number | null, folder: BrowseFolder): boolean {
    let cursor = folderById(candidateId);
    const seen = new Set<number>();
    while (cursor && !seen.has(cursor.id)) {
      if (cursor.id === folder.id) return true;
      seen.add(cursor.id);
      cursor = folderById(cursor.parentId);
    }
    return false;
  }

  // --- Search ---
  //
  // Browsing shows one folder, so search has to reach across the whole tree or
  // there is no way to find something without remembering where it was put.

  let matchedItems = $derived(
    trimmed
      ? items.filter((i) =>
          `${i.name} ${i.description || ''} ${pathLabel(i.folderId)}`
            .toLowerCase()
            .includes(trimmed),
        )
      : [],
  );

  let matchedFolders = $derived(
    trimmed ? folders.filter((f) => f.name.toLowerCase().includes(trimmed)) : [],
  );

  // --- Drag and drop ---

  type Drag =
    | { kind: 'item'; item: BrowseItem }
    | { kind: 'folder'; folder: BrowseFolder };

  let drag = $state<Drag | null>(null);
  // dropKey names the hovered target: "folder-3", "crumb-3", or "crumb-root".
  let dropKey = $state<string>('');
  let error = $state('');

  // canDrop refuses the moves that would be no-ops or would detach a subtree.
  function canDrop(target: number | null): boolean {
    if (!drag) return false;
    if (drag.kind === 'item') return drag.item.folderId !== target;
    if (!onMoveFolder) return false;
    if (drag.folder.canEdit === false) return false;
    if (drag.folder.parentId === target) return false;
    if (target !== null && (target === drag.folder.id || isDescendant(target, drag.folder))) {
      return false;
    }
    return true;
  }

  function startItemDrag(e: DragEvent, item: BrowseItem) {
    drag = { kind: 'item', item };
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', item.id);
    }
  }

  function startFolderDrag(e: DragEvent, folder: BrowseFolder) {
    if (!onMoveFolder || folder.canEdit === false) {
      e.preventDefault();
      return;
    }
    drag = { kind: 'folder', folder };
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', String(folder.id));
    }
  }

  function endDrag() {
    drag = null;
    dropKey = '';
  }

  function over(e: DragEvent, key: string, target: number | null) {
    if (!canDrop(target)) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    dropKey = key;
  }

  function leave(e: DragEvent, key: string) {
    const related = e.relatedTarget as Node | null;
    const current = e.currentTarget as HTMLElement;
    if (related && current.contains(related)) return;
    if (dropKey === key) dropKey = '';
  }

  async function drop(e: DragEvent, target: number | null) {
    e.preventDefault();
    // Capture the drag before clearing it: canDrop reads live state, so the
    // validity checks below re-run against what was actually picked up.
    const active = drag;
    dropKey = '';
    drag = null;
    if (!active) return;

    error = '';
    try {
      if (active.kind === 'item') {
        if (active.item.folderId === target) return;
        await onMoveItem(active.item, target);
      } else {
        if (!onMoveFolder) return;
        if (active.folder.parentId === target) return;
        if (target !== null && (target === active.folder.id || isDescendant(target, active.folder))) {
          return;
        }
        await onMoveFolder(active.folder, target);
      }
    } catch (err: any) {
      error = err?.message || 'Move failed';
    }
  }

  function openFolder(folder: BrowseFolder) {
    query = '';
    folderId = folder.id;
    onOpenFolder?.(folder);
  }

  function goTo(id: number | null) {
    query = '';
    folderId = id;
  }
</script>

<div class="browse">
  <div class="browse-head">
    <nav class="crumbs" aria-label="Folder path">
      <button
        class="crumb"
        class:drop-target={dropKey === 'crumb-root'}
        class:dropable={drag !== null && canDrop(null)}
        onclick={() => goTo(null)}
        ondragover={(e: DragEvent) => over(e, 'crumb-root', null)}
        ondragleave={(e: DragEvent) => leave(e, 'crumb-root')}
        ondrop={(e: DragEvent) => drop(e, null)}
      >
        <i class="las la-home"></i> {nounPlural}
      </button>
      {#each path as folder (folder.id)}
        <i class="las la-angle-right sep"></i>
        <button
          class="crumb"
          class:current={folder.id === folderId}
          class:drop-target={dropKey === `crumb-${folder.id}`}
          class:dropable={drag !== null && canDrop(folder.id)}
          onclick={() => goTo(folder.id)}
          ondragover={(e: DragEvent) => over(e, `crumb-${folder.id}`, folder.id)}
          ondragleave={(e: DragEvent) => leave(e, `crumb-${folder.id}`)}
          ondrop={(e: DragEvent) => drop(e, folder.id)}
        >
          {folder.name}
        </button>
      {/each}
    </nav>

    <div class="head-right">
      <input
        class="input search text-sm"
        placeholder="Search all {nounPlural.toLowerCase()}..."
        bind:value={query}
      />
      {#if toolbar}{@render toolbar(folderId)}{/if}
    </div>
  </div>

  {#if drag}
    <p class="drag-hint">
      Drop on a folder to move it in, or on a crumb above to move it out.
    </p>
  {/if}

  {#if error}
    <p class="move-error" role="alert"><i class="las la-exclamation-circle mr-1"></i>{error}</p>
  {/if}

  {#if loading}
    <p class="state">Loading {nounPlural.toLowerCase()}...</p>
  {:else if trimmed}
    <!-- Search reaches across the tree, so every hit names the folder it is in. -->
    <p class="state">
      {matchedFolders.length + matchedItems.length} matching {matchedFolders.length + matchedItems.length === 1 ? 'result' : 'results'}
    </p>
    <div class="grid">
      {#each matchedFolders as folder (folder.id)}
        {@render folderCard(folder)}
      {/each}
      {#each matchedItems as item (item.id)}
        {@render itemCard(item, true)}
      {/each}
    </div>
    {#if matchedFolders.length + matchedItems.length === 0}
      <p class="state">Nothing matches “{query}”.</p>
    {/if}
  {:else}
    <div class="grid">
      {#each childFolders as folder (folder.id)}
        {@render folderCard(folder)}
      {/each}
      {#each childItems as item (item.id)}
        {@render itemCard(item, false)}
      {/each}
    </div>

    {#if childFolders.length === 0 && childItems.length === 0}
      {#if folderId === null && emptyState}
        {@render emptyState()}
      {:else}
        <p class="state">This folder is empty.</p>
      {/if}
    {/if}
  {/if}
</div>

{#snippet folderCard(folder: BrowseFolder)}
  <div
    class="folder-card"
    class:drop-target={dropKey === `folder-${folder.id}`}
    class:dropable={drag !== null && canDrop(folder.id)}
    class:dragging={drag?.kind === 'folder' && drag.folder.id === folder.id}
    draggable={!!onMoveFolder && folder.canEdit !== false}
    role="button"
    tabindex="0"
    onclick={() => openFolder(folder)}
    onkeydown={(e: KeyboardEvent) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openFolder(folder);
      }
    }}
    ondragstart={(e: DragEvent) => startFolderDrag(e, folder)}
    ondragend={endDrag}
    ondragover={(e: DragEvent) => over(e, `folder-${folder.id}`, folder.id)}
    ondragleave={(e: DragEvent) => leave(e, `folder-${folder.id}`)}
    ondrop={(e: DragEvent) => drop(e, folder.id)}
  >
    <div class="card-top">
      <i class="las la-folder folder-icon"></i>
      <span class="card-title">{folder.name}</span>
    </div>
    <div class="card-foot">
      <span class="muted">{countAll(folder.id)} {countAll(folder.id) === 1 ? noun.toLowerCase() : nounPlural.toLowerCase()}</span>
      {#if folder.note}
        <span class="chip">{folder.note}</span>
      {/if}
      {#if folderActions}
        <span
          class="actions ml-auto"
          role="toolbar"
          tabindex="-1"
          onclick={(e: MouseEvent) => e.stopPropagation()}
          onkeydown={(e: KeyboardEvent) => e.stopPropagation()}
        >
          {@render folderActions(folder)}
        </span>
      {/if}
    </div>
  </div>
{/snippet}

{#snippet itemCard(item: BrowseItem, showPath: boolean)}
  <div
    class="item-card card-accent"
    class:dragging={drag?.kind === 'item' && drag.item.id === item.id}
    draggable="true"
    role="button"
    tabindex="0"
    onclick={() => onOpenItem(item)}
    onkeydown={(e: KeyboardEvent) => {
      if (e.key === 'Enter') onOpenItem(item);
    }}
    ondragstart={(e: DragEvent) => startItemDrag(e, item)}
    ondragend={endDrag}
  >
    <div class="card-top">
      {#if itemMark}{@render itemMark(item)}{/if}
      <span class="card-title">{item.name}</span>
    </div>
    {#if showPath}
      <p class="card-path"><i class="las la-folder folder-icon"></i> {pathLabel(item.folderId)}</p>
    {/if}
    <p class="card-desc">{item.description || 'No description'}</p>
    {#if itemBadges}
      <div class="card-badges">{@render itemBadges(item)}</div>
    {/if}
    <div class="card-foot">
      {#if itemMeta}{@render itemMeta(item)}{/if}
      {#if itemActions}
        <span
          class="actions ml-auto"
          role="toolbar"
          tabindex="-1"
          onclick={(e: MouseEvent) => e.stopPropagation()}
          onkeydown={(e: KeyboardEvent) => e.stopPropagation()}
        >
          {@render itemActions(item)}
        </span>
      {/if}
    </div>
  </div>
{/snippet}

<style>
  .browse {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .browse-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .head-right {
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .search {
    width: 14rem;
    padding-top: 0.3rem;
    padding-bottom: 0.3rem;
  }

  .crumbs {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.25rem;
    font-size: 0.85rem;
    min-height: 2rem;
  }

  .crumb {
    padding: 0.15rem 0.4rem;
    border: 1px solid transparent;
    border-radius: 5px;
    background: none;
    color: var(--theme-accent, #89b4fa);
    font-family: inherit;
    font-size: inherit;
    cursor: pointer;
  }

  .crumb:hover {
    background: var(--theme-surface0, #313244);
  }

  .crumb.current {
    color: var(--theme-text, #cdd6f4);
    font-weight: 600;
  }

  /* A crumb only advertises itself while something is being dragged. */
  .crumb.dropable {
    border-color: color-mix(in srgb, var(--theme-accent, #89b4fa) 45%, transparent);
    border-style: dashed;
  }

  .crumb.drop-target,
  .folder-card.drop-target {
    border-color: var(--theme-accent, #89b4fa);
    border-style: solid;
    background: color-mix(in srgb, var(--theme-accent, #89b4fa) 22%, transparent);
  }

  .sep {
    font-size: 0.6rem;
    opacity: 0.4;
  }

  .drag-hint {
    font-size: 0.75rem;
    opacity: 0.6;
  }

  .move-error {
    font-size: 0.78rem;
    color: var(--theme-error, #f38ba8);
  }

  .state {
    font-size: 0.8rem;
    opacity: 0.55;
    padding: 0.5rem 0.1rem;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(255px, 1fr));
    gap: 0.6rem;
  }

  .folder-card,
  .item-card {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 0.7rem 0.8rem;
    cursor: pointer;
    text-align: left;
    color: inherit;
    font-family: inherit;
  }

  .folder-card {
    background: color-mix(in srgb, var(--theme-surface0, #313244) 55%, transparent);
    border: 1px dashed var(--theme-surface1, #45475a);
    border-radius: var(--theme-radius-md, 8px);
  }

  .folder-card:hover {
    border-color: var(--theme-warning, #f9e2af);
    border-style: solid;
  }

  .item-card:hover {
    border-left-color: var(--theme-mauve, #cba6f7);
    box-shadow: var(--theme-shadow-md, 0 4px 6px rgba(0, 0, 0, 0.1));
  }

  .dragging {
    opacity: 0.4;
  }

  .card-top {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    min-width: 0;
  }

  .card-title {
    font-weight: 700;
    font-size: 0.92rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .card-path {
    font-size: 0.7rem;
    opacity: 0.5;
  }

  .card-desc {
    font-size: 0.76rem;
    opacity: 0.55;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .card-badges {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.15rem;
  }

  .card-foot {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.3rem;
    font-size: 0.7rem;
    margin-top: auto;
    padding-top: 0.45rem;
    white-space: nowrap;
  }

  .item-card .card-foot {
    border-top: 1px solid var(--theme-surface1, #45475a);
  }

  .muted {
    opacity: 0.55;
  }

  .chip {
    padding: 0.05rem 0.4rem;
    border: 1px solid var(--theme-surface1, #45475a);
    border-radius: 999px;
    font-size: 0.65rem;
    color: var(--theme-subtext0, #a6adc8);
  }

  .folder-icon {
    color: var(--theme-warning, #f9e2af);
  }

  .actions {
    display: inline-flex;
    gap: 0.15rem;
    opacity: 0;
    transition: opacity 0.12s ease;
  }

  .folder-card:hover .actions,
  .item-card:hover .actions,
  .actions:focus-within {
    opacity: 1;
  }

  .ml-auto {
    margin-left: auto;
  }
</style>
