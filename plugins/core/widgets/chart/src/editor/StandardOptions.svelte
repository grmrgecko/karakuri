<script lang="ts">
  import type { FieldConfig } from "../data/types";
  import { GRADIENTS } from "../data/colors";
  import UnitPicker from "./UnitPicker.svelte";

  // StandardOptions edits the standard field options of the defaults.
  let { value, onchange }: { value: FieldConfig; onchange: (next: FieldConfig) => void } = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  function set<K extends keyof FieldConfig>(key: K, v: FieldConfig[K] | undefined): void {
    const next = { ...value };
    if (v === undefined || v === null || (v as unknown) === "") delete next[key];
    else next[key] = v;
    onchange(next);
  }

  function num(e: Event, integer = false): number | undefined {
    const text = (e.target as HTMLInputElement).value.trim();
    if (text === "") return undefined;
    const n = Number(text);
    if (!Number.isFinite(n)) return undefined;
    return integer ? Math.max(0, Math.min(10, Math.round(n))) : n;
  }

  let mode = $derived(value.color?.mode ?? "palette-classic");
</script>

<div class="field">
  <label class="label" for="unit-{uid}">Unit</label>
  <UnitPicker id="unit-{uid}" value={value.unit ?? ""} onchange={(u) => set("unit", u || undefined)} />
</div>
<div class="row">
  <div class="field">
    <label class="label" for="min-{uid}">Min</label>
    <input id="min-{uid}" class="input" type="number" step="any" placeholder="Auto" value={value.min ?? ""}
           onchange={(e) => set("min", num(e))} />
  </div>
  <div class="field">
    <label class="label" for="max-{uid}">Max</label>
    <input id="max-{uid}" class="input" type="number" step="any" placeholder="Auto" value={value.max ?? ""}
           onchange={(e) => set("max", num(e))} />
  </div>
  <div class="field">
    <label class="label" for="dec-{uid}">Decimals</label>
    <input id="dec-{uid}" class="input" type="number" min="0" max="10" placeholder="Auto" value={value.decimals ?? ""}
           onchange={(e) => set("decimals", num(e, true))} />
  </div>
</div>
<div class="field">
  <label class="label" for="dn-{uid}">Display name</label>
  <input id="dn-{uid}" class="input" value={value.displayName ?? ""} placeholder={"${__field.labels.instance}"}
         onchange={(e) => set("displayName", (e.target as HTMLInputElement).value || undefined)} />
</div>
<div class="field">
  <label class="label" for="cm-{uid}">Color scheme</label>
  <select id="cm-{uid}" class="input" value={mode}
          onchange={(e) => set("color", { ...(value.color ?? {}), mode: (e.target as HTMLSelectElement).value })}>
    <option value="palette-classic">Classic palette</option>
    <option value="fixed">Single color</option>
    <option value="thresholds">From thresholds</option>
    <optgroup label="Continuous">
      {#each Object.entries(GRADIENTS) as [id, g]}<option value={id}>{g.name}</option>{/each}
    </optgroup>
  </select>
</div>
{#if mode === "fixed"}
  <div class="field">
    <label class="label" for="fc-{uid}">Color</label>
    <input id="fc-{uid}" class="input" value={value.color?.fixedColor ?? ""} placeholder="green, dark-red, or #89b4fa"
           onchange={(e) => set("color", { ...(value.color ?? { mode }), fixedColor: (e.target as HTMLInputElement).value.trim() || undefined })} />
  </div>
{:else if mode === "thresholds" || mode.startsWith("continuous-")}
  <div class="field">
    <label class="label" for="sb-{uid}">Series color by</label>
    <select id="sb-{uid}" class="input" value={value.color?.seriesBy ?? "last"}
            onchange={(e) => set("color", { ...(value.color ?? { mode }), seriesBy: (e.target as HTMLSelectElement).value as any })}>
      <option value="last">Last</option>
      <option value="min">Min</option>
      <option value="max">Max</option>
    </select>
  </div>
{/if}
<div class="field">
  <label class="label" for="nv-{uid}">No value</label>
  <input id="nv-{uid}" class="input" value={value.noValue ?? ""} placeholder="No data"
         onchange={(e) => set("noValue", (e.target as HTMLInputElement).value || undefined)} />
</div>
