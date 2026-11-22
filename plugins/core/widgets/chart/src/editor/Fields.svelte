<script lang="ts">
  import type { Spec } from "./specs";
  import { getPath, setPath } from "./paths";
  import UnitPicker from "./UnitPicker.svelte";

  // Fields renders a generated form for an options object. Every edit
  // returns a new object; a cleared field removes its key so the default
  // applies.
  let {
    specs,
    value,
    fieldNames = [],
    onchange,
  }: {
    specs: Spec[];
    value: Record<string, any>;
    fieldNames?: string[];
    onchange: (next: Record<string, any>) => void;
  } = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  const COLORS = ["green", "red", "orange", "yellow", "blue", "purple", "teal", "sky", "lavender", "pink", "text", "transparent"];

  function visible(spec: Spec): boolean {
    if (!spec.when) return true;
    return spec.when.values.includes(getPath(value, spec.when.path));
  }

  function set(spec: Spec, v: unknown): void {
    onchange(setPath(value ?? {}, spec.path, v));
  }

  function current(spec: Spec): any {
    const v = getPath(value, spec.path);
    if (spec.list && Array.isArray(v)) return v[0];
    return v;
  }

  function selectValue(spec: Spec): string {
    const v = current(spec);
    return v === undefined || v === null ? String(spec.default ?? "") : String(v);
  }

  function onSelect(spec: Spec, raw: string): void {
    if (raw === "") return set(spec, undefined);
    const v: unknown = spec.numeric ? Number(raw) : raw;
    set(spec, spec.list ? [v] : v);
  }

  function onNumber(spec: Spec, e: Event): void {
    const input = e.target as HTMLInputElement;
    if (input.value.trim() === "") return set(spec, undefined);
    let n = Number(input.value);
    if (!Number.isFinite(n)) return;
    if (spec.min !== undefined) n = Math.max(spec.min, n);
    if (spec.max !== undefined) n = Math.min(spec.max, n);
    input.value = String(n);
    set(spec, n);
  }

  function toggleMulti(spec: Spec, choice: string, on: boolean): void {
    const list: string[] = Array.isArray(current(spec)) ? current(spec) : [];
    const next = on ? [...list.filter((x) => x !== choice), choice] : list.filter((x) => x !== choice);
    set(spec, next.length ? next : undefined);
  }

  function spanMode(spec: Spec): string {
    const v = current(spec);
    return v === true ? "always" : typeof v === "number" ? "threshold" : "never";
  }

  async function editJson(spec: Spec): Promise<void> {
    const bridge = (window as any).__karakuri;
    if (!bridge?.editDocument) return;
    const text = await bridge.editDocument({
      title: spec.label,
      value: JSON.stringify(current(spec) ?? null, null, 2),
      format: "json",
      hint: spec.hint,
    });
    if (text === null) return;
    try {
      set(spec, text.trim() === "" ? undefined : JSON.parse(text));
    } catch {
      // The editor's own check refuses text that does not parse.
    }
  }
</script>

{#each specs as spec (spec.path)}
  {#if visible(spec)}
    {@const id = `f-${uid}-${spec.path}`}
    {#if spec.kind === "boolean"}
      <label class="checkbox-label">
        <input type="checkbox" checked={current(spec) ?? spec.default === true}
               onchange={(e) => set(spec, (e.target as HTMLInputElement).checked)} />
        <span>{spec.label}</span>
      </label>
    {:else}
      <div class="field">
        <label class="label" for={id}>{spec.label}</label>
        {#if spec.kind === "select"}
          <select {id} class="input" value={selectValue(spec)} onchange={(e) => onSelect(spec, (e.target as HTMLSelectElement).value)}>
            {#if spec.default === undefined}<option value="">Default</option>{/if}
            {#each spec.choices ?? [] as choice}
              <option value={choice.value}>{choice.label}</option>
            {/each}
          </select>
        {:else if spec.kind === "number"}
          <input {id} class="input" type="number" min={spec.min} max={spec.max} step={spec.step ?? "any"}
                 placeholder={spec.default !== undefined ? String(spec.default) : ""}
                 value={current(spec) ?? ""} onchange={(e) => onNumber(spec, e)} />
        {:else if spec.kind === "multi"}
          <div class="chips">
            {#each spec.choices ?? [] as choice}
              {@const on = Array.isArray(current(spec)) && current(spec).includes(choice.value)}
              <button type="button" class="chip" class:on onclick={() => toggleMulti(spec, choice.value, !on)}>{choice.label}</button>
            {/each}
          </div>
        {:else if spec.kind === "csv"}
          <input {id} class="input" value={Array.isArray(current(spec)) ? current(spec).join(", ") : ""}
                 placeholder="name, name"
                 onchange={(e) => {
                   const list = (e.target as HTMLInputElement).value.split(",").map((x) => x.trim()).filter(Boolean);
                   set(spec, list.length ? list : undefined);
                 }} />
        {:else if spec.kind === "color"}
          <input {id} class="input" list="colors-{uid}" value={current(spec) ?? ""} placeholder={String(spec.default ?? "")}
                 onchange={(e) => set(spec, (e.target as HTMLInputElement).value.trim() || undefined)} />
        {:else if spec.kind === "field"}
          <input {id} class="input" list="fields-{uid}" value={current(spec) ?? ""}
                 onchange={(e) => set(spec, (e.target as HTMLInputElement).value.trim() || undefined)} />
        {:else if spec.kind === "unit"}
          <UnitPicker {id} value={current(spec) ?? ""} onchange={(u) => set(spec, u || undefined)} />
        {:else if spec.kind === "spanNulls"}
          <div class="row">
            <select {id} class="input" value={spanMode(spec)}
                    onchange={(e) => {
                      const m = (e.target as HTMLSelectElement).value;
                      set(spec, m === "always" ? true : m === "threshold" ? 3600000 : false);
                    }}>
              <option value="never">Never</option>
              <option value="always">Always</option>
              <option value="threshold">Below a gap (ms)</option>
            </select>
            {#if spanMode(spec) === "threshold"}
              <input class="input" type="number" min="1" value={current(spec)} aria-label="Longest gap to connect, in milliseconds"
                     onchange={(e) => {
                       const n = Number((e.target as HTMLInputElement).value);
                       if (Number.isFinite(n) && n > 0) set(spec, n);
                     }} />
            {/if}
          </div>
        {:else if spec.kind === "json"}
          <button type="button" class="add" onclick={() => editJson(spec)}>Edit as JSON</button>
          {#if current(spec) !== undefined}
            <code class="json-preview">{JSON.stringify(current(spec))}</code>
          {/if}
        {:else}
          <input {id} class="input" value={current(spec) ?? ""}
                 onchange={(e) => set(spec, (e.target as HTMLInputElement).value || undefined)} />
        {/if}
        {#if spec.hint}<span class="hint">{spec.hint}</span>{/if}
      </div>
    {/if}
  {/if}
{/each}

<datalist id="colors-{uid}">
  {#each COLORS as color}<option value={color}></option>{/each}
</datalist>
<datalist id="fields-{uid}">
  {#each fieldNames as name}<option value={name}></option>{/each}
</datalist>
