<svelte:options customElement="flow-text-config" />

<script lang="ts">
  import { parseConfig, type FlowTextConfig } from "./config";
  import { placementIds } from "../../shared/dashboardLive";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<FlowTextConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const { widgetId } = placementIds(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function update(patch: Partial<FlowTextConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: $state.snapshot(cfg), bubbles: true, composed: true }),
    );
  }

  // Column rows edit columns and columnLabels together: a row's key picks a
  // column and its label renames it. Labels for keys no longer listed are
  // dropped so the saved config only names columns it shows.
  function setColumns(rows: { key: string; label: string }[]): void {
    const columnLabels: Record<string, string> = {};
    for (const row of rows) {
      if (row.key && row.label) columnLabels[row.key] = row.label;
    }
    update({ columns: rows.map((r) => r.key), columnLabels });
  }

  let columnRows = $derived(cfg.columns.map((key) => ({ key, label: cfg.columnLabels[key] ?? "" })));

  function editColumn(index: number, patch: Partial<{ key: string; label: string }>): void {
    setColumns(columnRows.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  }

  function maxRowsInput(e: Event): number {
    const input = e.target as HTMLInputElement;
    let n = Math.round(Number(input.value));
    if (input.value.trim() === "" || !Number.isFinite(n) || n < 0) n = 0;
    input.value = String(n);
    return n;
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <label class="label" for="ft-title">Title</label>
    <input id="ft-title" class="input" value={cfg.title} placeholder="None"
           oninput={(e: Event) => update({ title: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="ft-placeholder">Placeholder</label>
    <input id="ft-placeholder" class="input" value={cfg.placeholder}
           oninput={(e: Event) => update({ placeholder: (e.target as HTMLInputElement).value })} />
    <span class="hint">Shown until a flow sends content with a content command, and after it is cleared.</span>
  </div>

  <div class="field">
    <label class="label" for="ft-align">Alignment</label>
    <select id="ft-align" class="input" value={cfg.align}
            onchange={(e: Event) => update({ align: (e.target as HTMLSelectElement).value as FlowTextConfig["align"] })}>
      <option value="left">Left</option>
      <option value="center">Center</option>
      <option value="right">Right</option>
    </select>
  </div>

  <div class="field">
    <label class="label" for="ft-size">Text size</label>
    <select id="ft-size" class="input" value={cfg.size}
            onchange={(e: Event) => update({ size: (e.target as HTMLSelectElement).value as FlowTextConfig["size"] })}>
      <option value="small">Small</option>
      <option value="medium">Medium</option>
      <option value="large">Large</option>
      <option value="fit">Fit to widget</option>
    </select>
  </div>

  <div class="field">
    <span class="label">Table columns</span>
    {#each columnRows as row, index}
      <div class="column-row">
        <input class="input mono" placeholder="Key" value={row.key} aria-label="Column {index + 1} key"
               oninput={(e: Event) => editColumn(index, { key: (e.target as HTMLInputElement).value.trim() })} />
        <input class="input" placeholder="Header" value={row.label} aria-label="Column {index + 1} header"
               oninput={(e: Event) => editColumn(index, { label: (e.target as HTMLInputElement).value })} />
        <button type="button" class="remove" title="Remove column" aria-label="Remove column {index + 1}"
                onclick={() => setColumns(columnRows.filter((_, i) => i !== index))}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <line x1="18" y1="6" x2="6" y2="18" />
            <line x1="6" y1="6" x2="18" y2="18" />
          </svg>
        </button>
      </div>
    {/each}
    <button type="button" class="add" onclick={() => setColumns([...columnRows, { key: "", label: "" }])}>+ Add column</button>
    <span class="hint">
      A list of objects is drawn as a table. With no columns listed, every key is a column in the order it
      first appears. An empty header shows the key.
    </span>
  </div>

  <div class="field">
    <label class="label" for="ft-maxrows">Most table rows</label>
    <input id="ft-maxrows" class="input" type="number" min="0" value={cfg.maxRows}
           onchange={(e: Event) => update({ maxRows: maxRowsInput(e) })} />
    <span class="hint">0 shows every row.</span>
  </div>
</div>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-input-border, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-input-bg, var(--theme-surface0, #313244));
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .input.mono {
    font-family: var(--theme-font-mono, monospace);
    font-size: 0.8rem;
  }

  .column-row {
    display: flex;
    gap: 0.3rem;
  }

  .column-row .input {
    min-width: 0;
  }

  .remove {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    flex-shrink: 0;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface0, #313244);
    color: var(--theme-error, #f38ba8);
    cursor: pointer;
  }

  .remove:hover {
    border-color: var(--theme-error, #f38ba8);
  }

  .remove svg {
    width: 14px;
    height: 14px;
  }

  .add {
    align-self: flex-start;
    padding: 0.35rem 0.75rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
