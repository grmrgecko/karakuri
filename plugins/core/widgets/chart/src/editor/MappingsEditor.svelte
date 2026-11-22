<script lang="ts">
  import type { MappingResult, ValueMapping } from "../data/types";

  // MappingsEditor edits value mappings. The stored form may hold several values in
  // one value mapping ({"0": ..., "1": ...}); the editor shows each value as
  // its own row, which maps the same, and saves the rows as it shows them so
  // no value is lost to an edit.
  let { value: stored = [], onchange: emit }: { value?: ValueMapping[]; onchange: (next: ValueMapping[]) => void } = $props();

  let value = $derived(expand(stored));

  function expand(list: ValueMapping[]): ValueMapping[] {
    const out: ValueMapping[] = [];
    for (const m of list) {
      const keys = m.type === "value" ? Object.keys(m.options ?? {}) : [];
      if (keys.length > 1) {
        for (const k of keys) out.push({ type: "value", options: { [k]: (m.options as Record<string, MappingResult>)[k] } });
      } else {
        out.push(m);
      }
    }
    return out;
  }

  function onchange(next: ValueMapping[]): void {
    emit(next);
  }

  const uid = Math.random().toString(36).slice(2, 8);
  const COLORS = ["green", "red", "orange", "yellow", "blue", "purple", "teal", "sky", "lavender", "pink", "text"];

  function result(m: ValueMapping): MappingResult {
    if (m.type === "value") return Object.values(m.options ?? {})[0] ?? {};
    return (m.options as any)?.result ?? {};
  }

  function withResult(m: ValueMapping, r: MappingResult): ValueMapping {
    if (m.type === "value") {
      const key = Object.keys(m.options ?? {})[0] ?? "";
      return { type: "value", options: { [key]: r } };
    }
    return { ...m, options: { ...(m.options as any), result: r } } as ValueMapping;
  }

  function replace(i: number, m: ValueMapping): void {
    onchange(value.map((x, k) => (k === i ? m : x)));
  }

  function setType(i: number, type: ValueMapping["type"]): void {
    const r = result(value[i]);
    const next: Record<string, ValueMapping> = {
      value: { type: "value", options: { "": r } },
      range: { type: "range", options: { from: null, to: null, result: r } },
      regex: { type: "regex", options: { pattern: "", result: r } },
      special: { type: "special", options: { match: "null", result: r } },
    };
    replace(i, next[type]);
  }

  function num(text: string): number | null {
    if (text.trim() === "") return null;
    const n = Number(text);
    return Number.isFinite(n) ? n : null;
  }
</script>

<div class="field">
  {#each value as m, i (i)}
    {@const r = result(m)}
    <div class="card">
      <div class="row">
        <select class="input" aria-label="Mapping type" value={m.type}
                onchange={(e) => setType(i, (e.target as HTMLSelectElement).value as ValueMapping["type"])}>
          <option value="value">Value</option>
          <option value="range">Range</option>
          <option value="regex">Regex</option>
          <option value="special">Special</option>
        </select>
        <button type="button" class="remove" aria-label="Remove mapping" onclick={() => onchange(value.filter((_, k) => k !== i))}>×</button>
      </div>
      {#if m.type === "value"}
        <input class="input" placeholder="Value" value={Object.keys(m.options ?? {})[0] ?? ""} aria-label="Value"
               onchange={(e) => replace(i, { type: "value", options: { [(e.target as HTMLInputElement).value]: r } })} />
      {:else if m.type === "range"}
        <div class="row">
          <input class="input" type="number" step="any" placeholder="From" value={m.options.from ?? ""} aria-label="From"
                 onchange={(e) => replace(i, { type: "range", options: { ...m.options, from: num((e.target as HTMLInputElement).value) } })} />
          <input class="input" type="number" step="any" placeholder="To" value={m.options.to ?? ""} aria-label="To"
                 onchange={(e) => replace(i, { type: "range", options: { ...m.options, to: num((e.target as HTMLInputElement).value) } })} />
        </div>
      {:else if m.type === "regex"}
        <input class="input mono" placeholder="Pattern" value={m.options.pattern ?? ""} aria-label="Pattern"
               onchange={(e) => replace(i, { type: "regex", options: { ...m.options, pattern: (e.target as HTMLInputElement).value } })} />
      {:else}
        <select class="input" aria-label="Special value" value={m.options.match}
                onchange={(e) => replace(i, { type: "special", options: { ...m.options, match: (e.target as HTMLSelectElement).value as any } })}>
          <option value="null">Null</option>
          <option value="nan">NaN</option>
          <option value="null+nan">Null and NaN</option>
          <option value="true">True</option>
          <option value="false">False</option>
          <option value="empty">Empty</option>
        </select>
      {/if}
      <div class="row">
        <input class="input" placeholder="Display text" value={r.text ?? ""} aria-label="Display text"
               onchange={(e) => replace(i, withResult(m, { ...r, text: (e.target as HTMLInputElement).value || undefined }))} />
        <input class="input color" list="map-colors-{uid}" placeholder="Colour" value={r.color ?? ""} aria-label="Colour"
               onchange={(e) => replace(i, withResult(m, { ...r, color: (e.target as HTMLInputElement).value.trim() || undefined }))} />
      </div>
    </div>
  {/each}
  <button type="button" class="add" onclick={() => onchange([...value, { type: "value", options: { "": {} } }])}>+ Add mapping</button>
</div>
<datalist id="map-colors-{uid}">
  {#each COLORS as color}<option value={color}></option>{/each}
</datalist>
