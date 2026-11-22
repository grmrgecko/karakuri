<script lang="ts">
  import { useSvelteFlow } from '@xyflow/svelte';
  import { nodeCatalog, isTrigger } from '../../stores/nodeCatalog';
  import { connectionsStore } from '../../stores/connections';
  import { subflowLibrary } from '../../stores/subflows';
  import type { NodeMeta } from '../../stores/nodeCatalog';
  import type { FlowRecord } from '../../stores/flows';

  let {
    mode = 'flow',
    onAdd,
    onAddSubflow,
    onNewLocalSubflow,
  }: {
    // mode "subflow" is active inside subflow graphs: pseudo-port nodes are
    // offered and triggers are hidden.
    mode?: 'flow' | 'subflow';
    onAdd: (type: string, position: { x: number; y: number }) => void;
    onAddSubflow: (subflowId: string, position: { x: number; y: number }) => void;
    onNewLocalSubflow: (position: { x: number; y: number }) => void;
  } = $props();

  const { screenToFlowPosition } = useSvelteFlow();

  let search = $state('');

  // Node types that need a driver connection are only useful once one exists,
  // so the palette needs the connection list to filter them out.
  $effect(() => {
    connectionsStore.load().catch(() => {});
  });

  // hasConnection reports whether a driver kind has a connection configured.
  // Before the list loads everything stays visible, so nodes never flash out
  // of the palette on open.
  function hasConnection(kind: string | undefined): boolean {
    if (!kind || !$connectionsStore.loaded) return true;
    return $connectionsStore.connections.some((c) => c.kind === kind);
  }

  let filtered = $derived.by(() => {
    const q = search.trim().toLowerCase();
    return $nodeCatalog.categories
      .map((cat) => ({
        ...cat,
        // The subflow instance node is placed via the Subflows group below.
        // Behavioral kind decides visibility, not the folder: pseudo-port
        // nodes only exist inside subflow graphs, and triggers are hidden
        // there because a subflow runs from its parent's trigger.
        types: cat.types.filter(
          (m) =>
            m.type !== 'subflow' &&
            (mode === 'subflow' ? !isTrigger(m) : m.category !== 'port') &&
            hasConnection(m.connection_kind) &&
            (!q ||
              m.label.toLowerCase().includes(q) ||
              m.type.toLowerCase().includes(q) ||
              m.description.toLowerCase().includes(q) ||
              // The group is a host's name, so searching it lists everything
              // that runs there.
              cat.group.toLowerCase().includes(q))
        ),
      }))
      .filter((cat) => cat.types.length > 0);
  });

  // grouped nests the surviving folders under their palette groups, in the
  // catalog's order: Server first, then each agent.
  let grouped = $derived.by(() => {
    const out: { group: string; cats: typeof filtered }[] = [];
    for (const cat of filtered) {
      const last = out[out.length - 1];
      if (last && last.group === cat.group) last.cats.push(cat);
      else out.push({ group: cat.group, cats: [cat] });
    }
    return out;
  });

  let deployedSubflows = $derived(
    $subflowLibrary.records.filter(
      (r) =>
        r.deployed &&
        (!search.trim() || r.name.toLowerCase().includes(search.trim().toLowerCase()))
    )
  );

  function handleDragStart(event: DragEvent, meta: NodeMeta) {
    if (!event.dataTransfer) return;
    event.dataTransfer.setData('application/karakuri-node', meta.type);
    event.dataTransfer.effectAllowed = 'move';
  }

  function handleSubflowDragStart(event: DragEvent, record: FlowRecord) {
    if (!event.dataTransfer) return;
    event.dataTransfer.setData('application/karakuri-subflow', record.id);
    event.dataTransfer.effectAllowed = 'move';
  }

  function handleNewSubflowDragStart(event: DragEvent) {
    if (!event.dataTransfer) return;
    event.dataTransfer.setData('application/karakuri-new-subflow', '1');
    event.dataTransfer.effectAllowed = 'move';
  }

  // centerPosition drops new nodes near the center of the visible canvas
  // with a small jitter so repeated clicks do not stack exactly.
  function centerPosition(): { x: number; y: number } {
    const rect = document.querySelector('.svelte-flow')?.getBoundingClientRect();
    const center = rect
      ? { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 }
      : { x: window.innerWidth / 2, y: window.innerHeight / 2 };
    const position = screenToFlowPosition(center);
    position.x += Math.round(Math.random() * 40 - 20);
    position.y += Math.round(Math.random() * 40 - 20);
    return position;
  }

  function clickAdd(meta: NodeMeta) {
    onAdd(meta.type, centerPosition());
  }
</script>

<div class="flex flex-col h-full">
  <div class="p-2 border-b flex items-center gap-1" style="border-color: var(--theme-surface1);">
    <input
      class="input flex-1 min-w-0 text-sm py-1"
      placeholder="Search nodes..."
      bind:value={search}
    />
  </div>
  <div class="flex-1 overflow-y-auto p-2 flex flex-col gap-3">
    {#each grouped as g (g.group)}
      <div>
        <h3 class="group-head mb-1">{g.group}</h3>
        <div class="flex flex-col gap-2 pl-2">
          {#each g.cats as cat (cat.id)}
            <div>
              <h4 class="text-xs uppercase tracking-wide opacity-50 mb-1 px-1">{cat.label}</h4>
              <div class="flex flex-col gap-1">
                {#each cat.types as meta (meta.type)}
                  <div
                    class="palette-item"
                    draggable="true"
                    role="button"
                    tabindex="0"
                    title={meta.description}
                    ondragstart={(e: DragEvent) => handleDragStart(e, meta)}
                    onclick={() => clickAdd(meta)}
                    onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') clickAdd(meta); }}
                  >
                    <i class={meta.icon}></i>
                    <span>{meta.label}</span>
                  </div>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      </div>
    {/each}

    <!-- Subflows: shared library entries plus a new local subflow. The header
         is a group's, so the section does not read as nested under whichever
         group happens to be listed last. -->
    <div>
      <h3 class="group-head mb-1">Subflows</h3>
      <div class="flex flex-col gap-1">
        <div
          class="palette-item"
          draggable="true"
          role="button"
          tabindex="0"
          title="Create an empty local subflow inside this flow"
          ondragstart={handleNewSubflowDragStart}
          onclick={() => onNewLocalSubflow(centerPosition())}
          onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') onNewLocalSubflow(centerPosition()); }}
        >
          <i class="las la-plus-circle"></i>
          <span>New local subflow</span>
        </div>
        {#each deployedSubflows as record (record.id)}
          <div
            class="palette-item"
            draggable="true"
            role="button"
            tabindex="0"
            title={record.description || `Shared subflow "${record.name}"`}
            ondragstart={(e: DragEvent) => handleSubflowDragStart(e, record)}
            onclick={() => onAddSubflow(record.id, centerPosition())}
            onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') onAddSubflow(record.id, centerPosition()); }}
          >
            <i class="las la-layer-group"></i>
            <span>{record.name}</span>
          </div>
        {/each}
      </div>
    </div>

    {#if filtered.length === 0 && deployedSubflows.length === 0}
      <p class="text-xs opacity-50 px-1">No matching node types.</p>
    {/if}
  </div>
</div>

<style>
  /* Group headers sit above folder headers: same treatment, stronger weight
     and no indent, so the two levels read apart without color. */
  .group-head {
    font-size: 0.7rem;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--theme-subtext0, #a6adc8);
    padding: 0 0.25rem;
  }

  /* Folders carry no color: every entry is neutral, the way a node lands on
     the canvas until its author colors it. */
  .palette-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0.5rem;
    border-radius: var(--theme-radius-sm, 4px);
    background-color: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-surface1, #45475a);
    border-left: 3px solid var(--theme-overlay0, #6c7086);
    font-size: 0.8rem;
    cursor: grab;
    user-select: none;
  }

  .palette-item:hover {
    background-color: var(--theme-surface1, #45475a);
  }

  .palette-item i {
    font-size: 1rem;
    color: var(--theme-overlay0, #6c7086);
  }
</style>
