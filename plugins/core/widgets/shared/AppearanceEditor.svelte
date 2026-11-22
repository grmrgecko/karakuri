<script lang="ts">
  import { MAX_STATES, STATE_OPERATORS, type StateLook, type StateOperator } from "./appearance";
  import { VALUE_FORMATS, type ValueFormat } from "./formats";
  import { loadIcons } from "./icons";

  // AppearanceEditor edits the part of a binding that decides how its reading
  // looks: the value format and the ordered states. Entity Control, each
  // Entities Card row, and the Button share it, so a state means the same
  // thing wherever it is written.
  let {
    states = [],
    format,
    precision,
    idPrefix,
    onchange,
  }: {
    states?: StateLook[];
    format?: ValueFormat;
    precision?: number;
    /** idPrefix keeps datalist IDs apart when several editors share a panel. */
    idPrefix: string;
    onchange: (patch: { states?: StateLook[]; format?: ValueFormat; precision?: number }) => void;
  } = $props();

  const iconNames = loadIcons().map((i) => i.name);

  function patchState(index: number, next: Partial<StateLook>) {
    const list = states.map((s, i) => {
      if (i !== index) return s;
      const merged: Record<string, unknown> = { ...s, ...next };
      for (const key of Object.keys(merged)) {
        if (merged[key] === undefined || merged[key] === "") delete merged[key];
      }
      return merged as StateLook;
    });
    onchange({ states: list });
  }

  function addState() {
    onchange({ states: [...states, { operator: "equals", value: "on" }] });
  }

  function removeState(index: number) {
    const list = states.filter((_, i) => i !== index);
    onchange({ states: list.length ? list : undefined });
  }

  function moveState(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= states.length) return;
    const list = [...states];
    [list[index], list[target]] = [list[target], list[index]];
    onchange({ states: list });
  }

  const text = (e: Event) => (e.currentTarget as HTMLInputElement | HTMLSelectElement).value;
  const hex = (v: string | undefined, fallback: string) => (/^#[0-9a-f]{6}$/i.test(v ?? "") ? v! : fallback);
</script>

{#snippet colorField(label: string, current: string | undefined, fallback: string, set: (v: string | undefined) => void)}
  <span class="color">
    <span class="mini-label">{label}</span>
    <span class="color-row">
      <input type="color" class="swatch" class:unset={!current} value={hex(current, fallback)} aria-label="{label} color"
             onchange={(e) => set(text(e))} />
      <input class="input small" placeholder="Default" value={current ?? ""} aria-label="{label} CSS color"
             onchange={(e) => set(text(e).trim() || undefined)} />
    </span>
  </span>
{/snippet}

<div class="appearance">
  <div class="field pair">
    <label class="half">
      <span class="label">Format</span>
      <select class="input" value={format ?? ""}
              onchange={(e) => onchange({ format: (text(e) || undefined) as ValueFormat | undefined })}>
        <option value="">As reported</option>
        {#each VALUE_FORMATS as f (f.value)}
          <option value={f.value}>{f.label}</option>
        {/each}
      </select>
    </label>
    <label class="half">
      <span class="label">Decimals</span>
      <input class="input" type="number" min="0" max="6" placeholder="Source"
             value={precision ?? ""}
             onchange={(e) => {
               const raw = text(e);
               const n = Number(raw);
               onchange({ precision: raw === "" || !Number.isFinite(n) ? undefined : Math.max(0, Math.min(6, Math.round(n))) });
             }} />
    </label>
  </div>

  <div class="field">
    <span class="label">States</span>
    {#each states as look, i (i)}
      <div class="state">
        <div class="when">
          <span class="mini-label">When</span>
          <input class="input small attr" placeholder="value" value={look.attribute ?? ""} aria-label="Attribute"
                 title="An attribute of a composite control, such as brightness; empty compares its main value"
                 onchange={(e) => patchState(i, { attribute: text(e).trim() || undefined })} />
          <select class="input small" value={look.operator ?? ""} aria-label="Comparison"
                  onchange={(e) => patchState(i, { operator: (text(e) || undefined) as StateOperator | undefined })}>
            <option value="">always</option>
            {#each STATE_OPERATORS as op (op.value)}
              <option value={op.value}>{op.label}</option>
            {/each}
          </select>
          {#if look.operator}
            <input class="input small" placeholder="on" value={look.value ?? ""} aria-label="Compared value"
                   onchange={(e) => patchState(i, { value: text(e) })} />
          {/if}
          <span class="tools">
            <button type="button" class="tool" title="Move up" aria-label="Move state up" disabled={i === 0}
                    onclick={() => moveState(i, -1)}>↑</button>
            <button type="button" class="tool" title="Move down" aria-label="Move state down"
                    disabled={i === states.length - 1} onclick={() => moveState(i, 1)}>↓</button>
            <button type="button" class="tool danger" title="Remove state" aria-label="Remove state"
                    onclick={() => removeState(i)}>×</button>
          </span>
        </div>
        <div class="looks">
          <label class="look-field">
            <span class="mini-label">Icon</span>
            <input class="input small" list="{idPrefix}-icons" placeholder="la-microphone-slash"
                   value={look.icon ?? ""} onchange={(e) => patchState(i, { icon: text(e).trim() || undefined })} />
          </label>
          <label class="look-field">
            <span class="mini-label">Label</span>
            <input class="input small" placeholder={"Muted, or {value}"} value={look.label ?? ""}
                   onchange={(e) => patchState(i, { label: text(e) || undefined })} />
          </label>
          {@render colorField("Background", look.background, "#c0392b", (v) => patchState(i, { background: v }))}
          {@render colorField("Text", look.color, "#ffffff", (v) => patchState(i, { color: v }))}
          {@render colorField("Icon", look.iconColor, "#ffffff", (v) => patchState(i, { iconColor: v }))}
        </div>
      </div>
    {/each}
    {#if states.length < MAX_STATES}
      <button type="button" class="add" onclick={addState}>Add a state</button>
    {/if}
    <span class="hint">
      The first state whose comparison matches the reading changes the icon, colors, and label.
      A switch reads as on or off. A label may use {"{value}"} and {"{name}"}.
    </span>
  </div>
  <datalist id="{idPrefix}-icons">
    {#each iconNames as name (name)}
      <option value={name}></option>
    {/each}
  </datalist>
</div>

<style>
  .appearance {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .field.pair {
    flex-direction: row;
    gap: 0.5rem;
  }

  .half {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    flex: 1;
    min-width: 0;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .mini-label {
    font-size: 0.68rem;
    color: var(--theme-subtext, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .input.small {
    padding: 0.25rem 0.4rem;
    font-size: 0.75rem;
    min-width: 0;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .state {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.45rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
  }

  .when {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.3rem;
  }

  .when .attr {
    width: 4.5rem;
    flex-shrink: 0;
  }

  .when select {
    width: auto;
    flex: 0 0 auto;
  }

  .when input:not(.attr) {
    flex: 1 1 3rem;
    width: auto;
  }

  .tools {
    display: flex;
    margin-left: auto;
    flex-shrink: 0;
  }

  .tool {
    background: transparent;
    border: none;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    padding: 0 0.3rem;
    font-size: 0.85rem;
  }

  .tool:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .tool.danger:hover {
    color: var(--theme-error, #f38ba8);
  }

  .looks {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.35rem;
  }

  .look-field,
  .color {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    min-width: 0;
  }

  .color-row {
    display: flex;
    gap: 0.25rem;
    align-items: center;
  }

  .swatch {
    width: 1.6rem;
    height: 1.6rem;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    flex-shrink: 0;
    cursor: pointer;
  }

  /* An unset color shows its fallback faintly, so it does not read as chosen. */
  .swatch.unset {
    opacity: 0.35;
  }

  .add {
    align-self: flex-start;
    padding: 0.3rem 0.6rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-size: 0.78rem;
    cursor: pointer;
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
