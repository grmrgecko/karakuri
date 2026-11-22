<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { DashboardWidget } from '../stores/dashboards';
  import { alignOf, groupByRegion, regionLabel, type RegionId } from '../regionLayout';

  // RegionLayout draws the region layout: placements anchored to named
  // regions and stacked in order. The viewer and the
  // editor share it and differ only in how a cell is drawn and whether empty
  // regions are outlined.
  let {
    widgets,
    gridCols,
    rowHeight,
    gap,
    padding,
    editing = false,
    selectedRegion = '',
    cell,
    onregionclick,
  }: {
    widgets: DashboardWidget[];
    gridCols: number;
    rowHeight: number;
    gap: number;
    padding: number;
    editing?: boolean;
    selectedRegion?: string;
    cell: Snippet<[DashboardWidget, RegionId]>;
    onregionclick?: (region: RegionId) => void;
  } = $props();

  let groups = $derived(groupByRegion(widgets));

  function cellStyle(w: DashboardWidget, region: RegionId): string {
    if (region.startsWith('fullscreen_')) return 'position:absolute;inset:0;';
    const cols = Math.max(1, gridCols);
    const width = Math.min(100, (Math.max(1, w.width) / cols) * 100);
    const height = Math.max(1, w.height) * rowHeight + (Math.max(1, w.height) - 1) * gap;
    return `width:${width}%;height:${height}px;flex-shrink:0;`;
  }
</script>

{#snippet region(id: RegionId, extra: string = '')}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="region {extra}"
    class:outlined={editing}
    class:selected-region={editing && selectedRegion === id}
    class:empty={groups[id].length === 0}
    style="align-items:{alignOf(id)};gap:{gap}px;"
    data-region={id}
    role={editing ? 'button' : undefined}
    tabindex={editing ? 0 : undefined}
    onclick={(e: MouseEvent) => { if (editing && e.target === e.currentTarget) onregionclick?.(id); }}
    onkeydown={(e: KeyboardEvent) => { if (editing && (e.key === 'Enter' || e.key === ' ') && e.target === e.currentTarget) onregionclick?.(id); }}
  >
    {#if editing}<span class="region-label">{regionLabel(id)}</span>{/if}
    {#each groups[id] as w (w.id)}
      <div class="region-cell" style={cellStyle(w, id)}>{@render cell(w, id)}</div>
    {/each}
  </div>
{/snippet}

<div class="regions" style="padding:{padding}px;gap:{gap}px;">
  {#if !editing}
    <div class="layer below" style="padding:{padding}px;">{@render region('fullscreen_below', 'fullscreen')}</div>
  {/if}

  {@render region('top_bar', 'bar')}
  <div class="row3" style="gap:{gap}px;">
    {@render region('top_left')}
    {@render region('top_center')}
    {@render region('top_right')}
  </div>
  <div class="thirds" style="gap:{gap}px;">
    {@render region('upper_third', 'upper')}
    {@render region('middle_center', 'middle')}
    {@render region('lower_third', 'lower')}
  </div>
  <div class="row3 bottom" style="gap:{gap}px;">
    {@render region('bottom_left')}
    {@render region('bottom_center')}
    {@render region('bottom_right')}
  </div>
  {@render region('bottom_bar', 'bar')}

  {#if editing}
    <!-- The editor shows the full-screen layers as bands of their own: drawn
         in place, the one in front would cover every other region's cards. -->
    <div class="layer-bands" style="gap:{gap}px;">
      {@render region('fullscreen_below', 'band')}
      {@render region('fullscreen_above', 'band')}
    </div>
  {:else}
    <div class="layer above" style="padding:{padding}px;">{@render region('fullscreen_above', 'fullscreen')}</div>
  {/if}
</div>

<style>
  .regions {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 100%;
    min-height: 100%;
    box-sizing: border-box;
  }

  .row3 {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    align-items: start;
  }

  .row3.bottom {
    align-items: end;
  }

  /* The thirds share the height the rows above and below leave, but never
     shrink below what their placements need. */
  .thirds {
    flex: 1 0 auto;
    display: flex;
    flex-direction: column;
  }

  .thirds > .region {
    flex: 1 1 0;
    min-height: auto;
  }

  .region {
    position: relative;
    z-index: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
  }

  .region.upper { justify-content: flex-start; }
  .region.middle { justify-content: center; }
  .region.lower { justify-content: flex-end; }

  .region-cell {
    display: flex;
    max-width: 100%;
  }

  /* A collapsed placement takes its fixed-height wrapper with it, so the
     region's stack closes up behind it. */
  .region-cell:has(> :global([data-presence='collapsed'])) {
    display: none;
  }

  .layer {
    position: absolute;
    inset: 0;
    box-sizing: border-box;
    pointer-events: none;
  }

  .layer.below { z-index: 0; }
  .layer.above { z-index: 2; }

  .layer :global(.region-cell) {
    pointer-events: auto;
  }

  .region.fullscreen {
    position: absolute;
    inset: 0;
  }

  .outlined {
    border: 1px dashed var(--theme-surface1, #45475a);
    border-radius: var(--theme-radius-md, 8px);
    padding: 1.4rem 0.4rem 0.4rem;
    min-height: 3rem;
    cursor: pointer;
  }

  .outlined.selected-region {
    border-color: var(--theme-accent, #89b4fa);
  }

  .region-label {
    position: absolute;
    top: 0.2rem;
    left: 0.5rem;
    font-size: 0.65rem;
    opacity: 0.55;
    pointer-events: none;
  }

  .layer-bands {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .region.band {
    min-height: 8rem;
  }

  .region.band :global(.region-cell) {
    position: relative !important;
    inset: auto !important;
    width: 100% !important;
    height: 6rem !important;
  }
</style>
