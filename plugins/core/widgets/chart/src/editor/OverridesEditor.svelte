<script lang="ts">
  import type { Override, OverrideProperty } from "../data/types";
  import { MATCHERS } from "../data/fieldConfig";
  import { GRADIENTS } from "../data/colors";
  import type { Spec } from "./specs";
  import Fields from "./Fields.svelte";
  import UnitPicker from "./UnitPicker.svelte";
  import ThresholdsEditor from "./ThresholdsEditor.svelte";
  import MappingsEditor from "./MappingsEditor.svelte";

  // OverridesEditor edits field overrides: a matcher and the properties it
  // sets. Visualization field options appear as custom.<option>.
  let {
    value = [],
    customSpecs = [],
    fieldNames = [],
    refIds = [],
    onchange,
  }: {
    value?: Override[];
    customSpecs?: Spec[];
    fieldNames?: string[];
    refIds?: string[];
    onchange: (next: Override[]) => void;
  } = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  const STANDARD = [
    { id: "displayName", label: "Display name" },
    { id: "unit", label: "Unit" },
    { id: "decimals", label: "Decimals" },
    { id: "min", label: "Min" },
    { id: "max", label: "Max" },
    { id: "color", label: "Color scheme" },
    { id: "thresholds", label: "Thresholds" },
    { id: "mappings", label: "Value mappings" },
    { id: "noValue", label: "No value" },
  ];

  let properties = $derived([...STANDARD, ...customSpecs.map((s) => ({ id: `custom.${s.path}`, label: s.label }))]);

  function edit(i: number, o: Override): void {
    onchange(value.map((x, k) => (k === i ? o : x)));
  }

  function editProp(i: number, p: number, prop: OverrideProperty): void {
    const o = value[i];
    edit(i, { ...o, properties: o.properties.map((x, k) => (k === p ? prop : x)) });
  }

  function specFor(id: string): Spec | undefined {
    const s = customSpecs.find((x) => `custom.${x.path}` === id);
    return s ? { ...s, path: "v", when: undefined } : undefined;
  }

  function num(text: string): number | undefined {
    if (text.trim() === "") return undefined;
    const n = Number(text);
    return Number.isFinite(n) ? n : undefined;
  }
</script>

<div class="field">
  {#each value as o, i (i)}
    <div class="card">
      <div class="row">
        <select class="input" aria-label="Override matcher" value={o.matcher?.id ?? "byName"}
                onchange={(e) => edit(i, { ...o, matcher: { id: (e.target as HTMLSelectElement).value, options: "" } })}>
          {#each MATCHERS as m}<option value={m.id}>{m.name}</option>{/each}
        </select>
        <button type="button" class="remove" aria-label="Remove override" onclick={() => onchange(value.filter((_, k) => k !== i))}>×</button>
      </div>
      {#if o.matcher?.id === "byFrameRefID"}
        <select class="input" aria-label="Query" value={o.matcher.options ?? ""}
                onchange={(e) => edit(i, { ...o, matcher: { ...o.matcher, options: (e.target as HTMLSelectElement).value } })}>
          <option value="">Choose a query</option>
          {#each refIds as r}<option value={r}>{r}</option>{/each}
        </select>
      {:else if o.matcher?.id === "byType"}
        <select class="input" aria-label="Field type" value={o.matcher.options ?? "number"}
                onchange={(e) => edit(i, { ...o, matcher: { ...o.matcher, options: (e.target as HTMLSelectElement).value } })}>
          <option value="number">Number</option>
          <option value="string">String</option>
          <option value="time">Time</option>
          <option value="boolean">Boolean</option>
        </select>
      {:else}
        <input class="input mono" list="ov-fields-{uid}" aria-label="Field name or pattern" value={o.matcher?.options ?? ""}
               placeholder={o.matcher?.id === "byRegexp" ? "outdoor.*" : "Field name"}
               onchange={(e) => edit(i, { ...o, matcher: { ...(o.matcher ?? { id: "byName" }), options: (e.target as HTMLInputElement).value } })} />
      {/if}

      {#each o.properties ?? [] as prop, p (p)}
        <div class="prop">
          <div class="row">
            <select class="input" aria-label="Property" value={prop.id}
                    onchange={(e) => editProp(i, p, { id: (e.target as HTMLSelectElement).value, value: undefined })}>
              {#each properties as choice}<option value={choice.id}>{choice.label}</option>{/each}
            </select>
            <button type="button" class="remove" aria-label="Remove property"
                    onclick={() => edit(i, { ...o, properties: o.properties.filter((_, k) => k !== p) })}>×</button>
          </div>
          {#if prop.id === "unit"}
            <UnitPicker value={prop.value ?? ""} onchange={(u) => editProp(i, p, { ...prop, value: u || undefined })} />
          {:else if prop.id === "decimals" || prop.id === "min" || prop.id === "max"}
            <input class="input" type="number" step="any" value={prop.value ?? ""} aria-label="Value"
                   onchange={(e) => editProp(i, p, { ...prop, value: num((e.target as HTMLInputElement).value) })} />
          {:else if prop.id === "displayName" || prop.id === "noValue"}
            <input class="input" value={prop.value ?? ""} aria-label="Value"
                   onchange={(e) => editProp(i, p, { ...prop, value: (e.target as HTMLInputElement).value })} />
          {:else if prop.id === "color"}
            <div class="row">
              <select class="input" aria-label="Color mode" value={prop.value?.mode ?? "fixed"}
                      onchange={(e) => editProp(i, p, { ...prop, value: { ...(prop.value ?? {}), mode: (e.target as HTMLSelectElement).value } })}>
                <option value="fixed">Single color</option>
                <option value="palette-classic">Classic palette</option>
                <option value="thresholds">From thresholds</option>
                {#each Object.entries(GRADIENTS) as [id, g]}<option value={id}>{g.name}</option>{/each}
              </select>
              {#if (prop.value?.mode ?? "fixed") === "fixed"}
                <input class="input color" placeholder="blue" value={prop.value?.fixedColor ?? ""} aria-label="Color"
                       onchange={(e) => editProp(i, p, { ...prop, value: { mode: "fixed", fixedColor: (e.target as HTMLInputElement).value.trim() } })} />
              {/if}
            </div>
          {:else if prop.id === "thresholds"}
            <ThresholdsEditor value={prop.value} onchange={(t) => editProp(i, p, { ...prop, value: t })} />
          {:else if prop.id === "mappings"}
            <MappingsEditor value={prop.value ?? []} onchange={(m) => editProp(i, p, { ...prop, value: m })} />
          {:else if specFor(prop.id)}
            <Fields specs={[specFor(prop.id)!]} value={{ v: prop.value }} {fieldNames}
                    onchange={(next) => editProp(i, p, { ...prop, value: next.v })} />
          {/if}
        </div>
      {/each}
      <button type="button" class="add" onclick={() => edit(i, { ...o, properties: [...(o.properties ?? []), { id: "color", value: { mode: "fixed", fixedColor: "blue" } }] })}>
        + Add property
      </button>
    </div>
  {/each}
  <button type="button" class="add" onclick={() => onchange([...value, { matcher: { id: "byName", options: "" }, properties: [] }])}>
    + Add override
  </button>
</div>
<datalist id="ov-fields-{uid}">
  {#each fieldNames as name}<option value={name}></option>{/each}
</datalist>

<style>
  .prop {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding-left: 0.5rem;
    border-left: 2px solid var(--theme-border, #45475a);
  }
</style>
