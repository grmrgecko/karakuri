<script lang="ts">
  import type { Transformation } from "../data/types";
  import { TRANSFORMATIONS } from "../data/transformations";
  import { TRANSFORM_OPTIONS } from "./specs";
  import Fields from "./Fields.svelte";
  import StructuredTransform from "./StructuredTransform.svelte";

  // STRUCTURED are the transformations with their own form.
  const STRUCTURED = ["organize", "filterByValue", "groupBy"];

  // TransformationsEditor edits the ordered transformation list. Each has a
  // form for its common options, and its whole options object can be edited
  // as JSON for the rest.
  let {
    value = [],
    fieldNames = [],
    onchange,
  }: { value?: Transformation[]; fieldNames?: string[]; onchange: (next: Transformation[]) => void } = $props();

  let adding = $state("");

  function edit(i: number, t: Transformation): void {
    onchange(value.map((x, k) => (k === i ? t : x)));
  }

  function move(i: number, by: number): void {
    const j = i + by;
    if (j < 0 || j >= value.length) return;
    const next = [...value];
    [next[i], next[j]] = [next[j], next[i]];
    onchange(next);
  }

  function nameOf(id: string): string {
    return TRANSFORMATIONS.find((t) => t.id === id)?.name ?? id;
  }

  async function editJson(i: number): Promise<void> {
    const bridge = (window as any).__karakuri;
    if (!bridge?.editDocument) return;
    const text = await bridge.editDocument({
      title: `${nameOf(value[i].id)} options`,
      value: JSON.stringify(value[i].options ?? {}, null, 2),
      format: "json",
      requireObject: true,
    });
    if (text === null) return;
    try {
      edit(i, { ...value[i], options: text.trim() === "" ? {} : JSON.parse(text) });
    } catch {
      // The editor's own check refuses text that does not parse.
    }
  }
</script>

<div class="field">
  {#each value as t, i (i)}
    <div class="card" class:disabled={t.disabled}>
      <div class="row head">
        <span class="title">{i + 1}. {nameOf(t.id)}</span>
        <button type="button" class="icon" aria-label="Move up" disabled={i === 0} onclick={() => move(i, -1)}>↑</button>
        <button type="button" class="icon" aria-label="Move down" disabled={i === value.length - 1} onclick={() => move(i, 1)}>↓</button>
        <button type="button" class="icon" aria-label={t.disabled ? "Enable" : "Disable"} title={t.disabled ? "Enable" : "Disable"}
                onclick={() => edit(i, { ...t, disabled: !t.disabled || undefined })}>{t.disabled ? "○" : "●"}</button>
        <button type="button" class="remove" aria-label="Remove transformation" onclick={() => onchange(value.filter((_, k) => k !== i))}>×</button>
      </div>
      <Fields specs={TRANSFORM_OPTIONS[t.id] ?? []} value={t.options ?? {}} {fieldNames}
              onchange={(options) => edit(i, { ...t, options })} />
      {#if STRUCTURED.includes(t.id)}
        <StructuredTransform id={t.id} value={t.options ?? {}} {fieldNames} onchange={(options) => edit(i, { ...t, options })} />
      {/if}
      <button type="button" class="link" onclick={() => editJson(i)}>Edit options as JSON</button>
    </div>
  {/each}
  <div class="row">
    <select class="input" aria-label="Transformation to add" bind:value={adding}>
      <option value="">Add a transformation</option>
      {#each TRANSFORMATIONS as t}<option value={t.id} title={t.description}>{t.name}</option>{/each}
    </select>
    <button type="button" class="add" disabled={!adding}
            onclick={() => {
              onchange([...value, { id: adding, options: {} }]);
              adding = "";
            }}>Add</button>
  </div>
  <span class="hint">Transformations run in order on every query's results before the field options.</span>
</div>

<style>
  .head {
    align-items: center;
  }

  .title {
    flex: 1;
    font-size: 0.8rem;
    font-weight: 500;
  }

  .disabled {
    opacity: 0.6;
  }

  .icon,
  .link {
    background: none;
    border: none;
    color: var(--theme-text-muted, #a6adc8);
    cursor: pointer;
    font: inherit;
    padding: 0 0.2rem;
  }

  .link {
    align-self: flex-start;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.75rem;
    padding: 0;
  }
</style>
