<script lang="ts">
  import type { LegendItem } from "./viz/common";
  import { calcName } from "./viz/common";
  import { safeColor } from "./data/colors";

  // Legend is the chart legend as HTML: a list or a table with calculations,
  // below or beside the chart. A click shows only that series; a click with
  // Ctrl or Shift toggles it.
  let {
    items,
    mode = "list",
    placement = "bottom",
    calcs = [],
    ontoggle,
  }: {
    items: LegendItem[];
    mode?: string;
    placement?: string;
    calcs?: string[];
    ontoggle?: (name: string, only: boolean) => void;
  } = $props();

  let columns = $derived(calcs.filter((c) => items.some((i) => c in i.values)));

  function click(e: MouseEvent, name: string): void {
    ontoggle?.(name, !(e.ctrlKey || e.metaKey || e.shiftKey));
  }
</script>

{#if mode === "table"}
  <div class="legend table" class:right={placement === "right"}>
    <table>
      {#if columns.length}
        <thead>
          <tr>
            <th></th>
            {#each columns as c}<th>{calcName(c)}</th>{/each}
          </tr>
        </thead>
      {/if}
      <tbody>
        {#each items as item (item.name)}
          <tr class:off={item.hidden}>
            <td>
              <button type="button" class="entry" onclick={(e) => click(e, item.name)}>
                <span class="swatch" style:background={safeColor(item.color, "#888")}></span>
                <span class="name">{item.name}</span>
              </button>
            </td>
            {#each columns as c}<td class="value">{item.values[c] ?? ""}</td>{/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else}
  <div class="legend list" class:right={placement === "right"}>
    {#each items as item (item.name)}
      <button type="button" class="entry" class:off={item.hidden} onclick={(e) => click(e, item.name)}>
        <span class="swatch" style:background={safeColor(item.color, "#888")}></span>
        <span class="name">{item.name}</span>
        {#each columns as c}
          <span class="calc">{calcName(c)}: {item.values[c] ?? ""}</span>
        {/each}
      </button>
    {/each}
  </div>
{/if}

<style>
  .legend {
    flex: none;
    font-size: 0.75rem;
    color: var(--theme-text, #cdd6f4);
    overflow: auto;
    max-height: 35%;
  }

  .legend.right {
    max-height: none;
    max-width: 40%;
  }

  .list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.15rem 0.75rem;
    padding: 0.2rem 0.4rem;
  }

  .list.right {
    flex-direction: column;
    flex-wrap: nowrap;
  }

  .entry {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0;
    border: none;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
    text-align: left;
    min-width: 0;
  }

  .off {
    opacity: 0.4;
  }

  .swatch {
    flex: none;
    width: 12px;
    height: 4px;
    border-radius: 2px;
  }

  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .calc {
    color: var(--theme-text-muted, #a6adc8);
    white-space: nowrap;
  }

  table {
    border-collapse: collapse;
    width: 100%;
  }

  th {
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
    text-align: right;
    padding: 0.1rem 0.5rem;
    white-space: nowrap;
  }

  td {
    padding: 0.1rem 0.5rem;
  }

  td.value {
    text-align: right;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
</style>
