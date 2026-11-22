<script lang="ts">
  import type { Field, Frame, Palette } from "./data/types";
  import { display, displayText, formatNumber } from "./data/display";
  import { displayName, frameLength } from "./data/frames";
  import { hiddenFromViz } from "./data/fieldConfig";
  import { fieldMinMax, safeColor, withAlpha } from "./data/colors";
  import { reduce, reducerName, toNumber } from "./data/reducers";

  // Table is the table panel. Sorting, filtering, and paging belong to
  // this viewer and are never saved.
  let {
    frames,
    options,
    palette,
    height,
  }: { frames: Frame[]; options: Record<string, any>; palette: Palette; height: number } = $props();

  let frameIndex = $state(0);
  $effect(() => {
    if (frameIndex >= frames.length) frameIndex = 0;
  });
  let frame = $derived(frames[Math.min(frameIndex, Math.max(0, frames.length - 1))]);
  let columns = $derived(frame ? frame.fields.filter((f) => !hiddenFromViz(f)) : []);
  let rowCount = $derived(frame ? frameLength(frame) : 0);

  let sortName = $state<string | null>(null);
  let sortDesc = $state(false);
  let seededSort = "";
  $effect(() => {
    const initial = Array.isArray(options.sortBy) ? options.sortBy[0] : undefined;
    const key = JSON.stringify(initial ?? null);
    if (key === seededSort) return;
    seededSort = key;
    sortName = initial?.displayName ?? null;
    sortDesc = initial?.desc === true;
  });

  let filters = $state<Record<string, string[]>>({});
  let openFilter = $state<string | null>(null);
  let page = $state(0);

  function nameOf(f: Field): string {
    return frame ? displayName(f, frame) : f.name;
  }

  function compare(a: unknown, b: unknown): number {
    if (a === b) return 0;
    if (a === null || a === undefined) return 1;
    if (b === null || b === undefined) return -1;
    const x = toNumber(a);
    const y = toNumber(b);
    if (x !== null && y !== null) return x - y;
    return String(a).localeCompare(String(b));
  }

  let rows = $derived.by(() => {
    let r = Array.from({ length: rowCount }, (_, i) => i);
    for (const [name, allowed] of Object.entries(filters)) {
      if (!allowed.length) continue;
      const f = columns.find((c) => nameOf(c) === name);
      if (!f) continue;
      r = r.filter((i) => allowed.includes(cellText(f, f.values[i])));
    }
    const sf = sortName ? columns.find((c) => nameOf(c) === sortName) : undefined;
    if (sf) r = [...r].sort((a, b) => compare(sf.values[a], sf.values[b]) * (sortDesc ? -1 : 1));
    return r;
  });

  let rowHeight = $derived(options.cellHeight === "lg" ? 48 : options.cellHeight === "md" ? 40 : 32);
  let paginate = $derived(options.footer?.enablePagination === true || options.enablePagination === true);
  let footer = $derived(options.footer?.show === true);
  let pageSize = $derived(Math.max(1, Math.floor((height - (options.showHeader === false ? 0 : 34) - (footer ? 34 : 0) - 36) / rowHeight)));
  let pages = $derived(paginate ? Math.max(1, Math.ceil(rows.length / pageSize)) : 1);
  let shown = $derived(paginate ? rows.slice(Math.min(page, pages - 1) * pageSize, (Math.min(page, pages - 1) + 1) * pageSize) : rows);

  function sortBy(f: Field): void {
    if (options.sortable === false) return;
    const name = nameOf(f);
    if (sortName !== name) {
      sortName = name;
      sortDesc = false;
    } else if (!sortDesc) {
      sortDesc = true;
    } else {
      sortName = null;
    }
  }

  function cellType(f: Field): string {
    return f.config.custom?.cellOptions?.type ?? f.config.custom?.displayMode ?? "auto";
  }

  function cellText(f: Field, v: unknown): string {
    if (cellType(f) === "json-view" && v && typeof v === "object") return JSON.stringify(v);
    return displayText(display(f, v, palette));
  }

  function align(f: Field): string {
    const a = f.config.custom?.align;
    if (a === "left" || a === "center" || a === "right") return a;
    return f.type === "number" ? "right" : "left";
  }

  function uniqueValues(f: Field): string[] {
    const set = new Set<string>();
    for (const v of f.values) {
      set.add(cellText(f, v));
      if (set.size >= 200) break;
    }
    return [...set].sort();
  }

  function toggleFilter(name: string, value: string): void {
    const current = filters[name] ?? [];
    filters = { ...filters, [name]: current.includes(value) ? current.filter((x) => x !== value) : [...current, value] };
    page = 0;
  }

  function footerText(f: Field): string {
    const calcs: string[] = Array.isArray(options.footer?.reducer) && options.footer.reducer.length ? options.footer.reducer : ["sum"];
    const only: string[] = Array.isArray(options.footer?.fields) ? options.footer.fields : [];
    if (only.length && !only.includes(nameOf(f))) return "";
    if (f.type !== "number") return "";
    const values = rows.map((i) => f.values[i]);
    return calcs.map((c) => formatNumber(f, reduce(values, c))).join(" / ");
  }

  function sparkPath(v: unknown, w: number, h: number): string {
    const arr = Array.isArray(v) ? v.map(toNumber).filter((x): x is number => x !== null) : [];
    if (arr.length < 2) return "";
    const lo = Math.min(...arr);
    const hi = Math.max(...arr);
    const span = hi - lo || 1;
    return arr.map((y, i) => `${i ? "L" : "M"}${((i / (arr.length - 1)) * w).toFixed(1)},${(h - ((y - lo) / span) * h).toFixed(1)}`).join("");
  }

  function gaugeFraction(f: Field, v: unknown): number {
    const n = toNumber(v);
    if (n === null) return 0;
    const { min, max } = fieldMinMax(f, true);
    return Math.max(0, Math.min(1, (n - min) / (max - min)));
  }
</script>

<div class="table-wrap">
  {#if !frame}
    <div class="empty">No data</div>
  {:else}
    <div class="scroll">
      <table style:--row="{rowHeight}px">
        {#if options.showHeader !== false}
          <thead>
            <tr>
              {#each columns as f (f)}
                {@const name = nameOf(f)}
                <th style:text-align={align(f)} style:width={f.config.custom?.width ? `${f.config.custom.width}px` : undefined}
                    style:min-width={f.config.custom?.minWidth ? `${f.config.custom.minWidth}px` : undefined}>
                  <button type="button" class="head" onclick={() => sortBy(f)}>
                    {name}{#if sortName === name}<span class="sort">{sortDesc ? " ▼" : " ▲"}</span>{/if}
                  </button>
                  {#if f.config.custom?.filterable}
                    <button type="button" class="filter" class:active={(filters[name] ?? []).length > 0}
                            aria-label="Filter {name}" onclick={() => (openFilter = openFilter === name ? null : name)}>⏷</button>
                    {#if openFilter === name}
                      <div class="filter-menu">
                        {#each uniqueValues(f) as value}
                          <label><input type="checkbox" checked={(filters[name] ?? []).includes(value)}
                                        onchange={() => toggleFilter(name, value)} /> {value}</label>
                        {/each}
                        {#if (filters[name] ?? []).length}
                          <button type="button" class="clear" onclick={() => { filters = { ...filters, [name]: [] }; }}>Clear</button>
                        {/if}
                      </div>
                    {/if}
                  {/if}
                </th>
              {/each}
            </tr>
          </thead>
        {/if}
        <tbody>
          {#each shown as r (r)}
            <tr>
              {#each columns as f (f)}
                {@const v = f.values[r]}
                {@const type = cellType(f)}
                {@const d = display(f, v, palette)}
                {@const c = safeColor(d.color, palette.text)}
                <td style:text-align={align(f)}
                    style:color={type === "color-text" ? c : type === "color-background" || type === "color-background-solid" ? "#fff" : undefined}
                    style:background={type === "color-background-solid" || (type === "color-background" && f.config.custom?.cellOptions?.mode === "basic")
                      ? c
                      : type === "color-background"
                        ? `linear-gradient(120deg, ${withAlpha(c, 0.6)}, ${c})`
                        : undefined}>
                  {#if type === "gauge" || type === "gradient-gauge" || type === "lcd-gauge" || type === "basic"}
                    <div class="gauge">
                      <div class="gauge-bar" style:width="{gaugeFraction(f, v) * 100}%" style:background={c}></div>
                      <span>{displayText(d)}</span>
                    </div>
                  {:else if type === "sparkline" && Array.isArray(v)}
                    <svg class="cell-spark" viewBox="0 0 100 20" preserveAspectRatio="none" aria-hidden="true">
                      <path d={sparkPath(v, 100, 20)} fill="none" stroke={c} stroke-width="1.5" vector-effect="non-scaling-stroke" />
                    </svg>
                  {:else}
                    {cellText(f, v)}
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
        {#if footer}
          <tfoot>
            <tr>
              {#each columns as f, i (f)}
                <td style:text-align={align(f)}>{i === 0 && f.type !== "number" ? (options.footer?.countRows ? `Count: ${rows.length}` : reducerName((options.footer?.reducer ?? ["sum"])[0])) : footerText(f)}</td>
              {/each}
            </tr>
          </tfoot>
        {/if}
      </table>
    </div>
    {#if frames.length > 1 || (paginate && pages > 1)}
      <div class="bar">
        {#if frames.length > 1}
          <select class="frame-select" aria-label="Table" value={String(frameIndex)}
                  onchange={(e) => { frameIndex = Number((e.target as HTMLSelectElement).value); page = 0; }}>
            {#each frames as fr, i}
              <option value={String(i)}>{fr.name || fr.refId || `Table ${i + 1}`}</option>
            {/each}
          </select>
        {/if}
        {#if paginate && pages > 1}
          <span class="pager">
            <button type="button" disabled={page <= 0} onclick={() => (page = Math.max(0, page - 1))}>‹</button>
            {Math.min(page, pages - 1) + 1} / {pages}
            <button type="button" disabled={page >= pages - 1} onclick={() => (page = Math.min(pages - 1, page + 1))}>›</button>
          </span>
        {/if}
      </div>
    {/if}
  {/if}
</div>

<style>
  .table-wrap {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    font-size: 0.8rem;
    color: var(--theme-text, #cdd6f4);
  }

  .empty {
    margin: auto;
    color: var(--theme-text-muted, #a6adc8);
  }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }

  table {
    width: 100%;
    border-collapse: collapse;
    font-variant-numeric: tabular-nums;
  }

  th {
    position: sticky;
    top: 0;
    background: var(--theme-table-header-bg, var(--theme-surface0, #313244));
    font-weight: 500;
    padding: 0 0.5rem;
    height: 32px;
    white-space: nowrap;
    z-index: 1;
  }

  td {
    height: var(--row);
    padding: 0 0.5rem;
    border-bottom: 1px solid var(--theme-border-translucent, rgba(69, 71, 90, 0.5));
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 30rem;
  }

  tfoot td {
    font-weight: 500;
    border-bottom: none;
    border-top: 1px solid var(--theme-border, #45475a);
  }

  .head,
  .filter,
  .pager button,
  .clear {
    background: none;
    border: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
    padding: 0;
  }

  .filter {
    margin-left: 0.25rem;
    opacity: 0.5;
  }

  .filter.active {
    opacity: 1;
    color: var(--theme-accent, #89b4fa);
  }

  .filter-menu {
    position: absolute;
    margin-top: 0.25rem;
    max-height: 14rem;
    overflow: auto;
    background: var(--theme-dropdown-bg, var(--theme-surface0, #313244));
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    padding: 0.35rem;
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    text-align: left;
    font-weight: 400;
    z-index: 2;
  }

  .sort {
    font-size: 0.7em;
  }

  .gauge {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.4rem;
  }

  .gauge-bar {
    height: 10px;
    border-radius: 2px;
    flex: none;
    max-width: 70%;
  }

  .cell-spark {
    width: 100%;
    height: calc(var(--row) - 10px);
  }

  .bar {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    padding: 0.25rem 0;
  }

  .frame-select {
    background: var(--theme-input-bg, var(--theme-surface0, #313244));
    color: inherit;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    font: inherit;
    padding: 0.1rem 0.3rem;
  }

  .pager {
    margin-left: auto;
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }
</style>
