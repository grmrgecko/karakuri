<script lang="ts">
  import { REDUCERS } from "../data/reducers";

  // StructuredTransform edits the transformations whose options are keyed by
  // field name or hold a list of conditions, which a generated form cannot
  // describe: organize fields, filter data by values, and group by.
  let {
    id,
    value = {},
    fieldNames = [],
    onchange,
  }: { id: string; value?: Record<string, any>; fieldNames?: string[]; onchange: (next: Record<string, any>) => void } = $props();

  const uid = Math.random().toString(36).slice(2, 8);

  const CONDITIONS = [
    { id: "greater", label: "Greater than" },
    { id: "greaterOrEqual", label: "Greater or equal" },
    { id: "lower", label: "Lower than" },
    { id: "lowerOrEqual", label: "Lower or equal" },
    { id: "equal", label: "Equal" },
    { id: "notEqual", label: "Not equal" },
    { id: "range", label: "In range" },
    { id: "regex", label: "Matches regex" },
    { id: "isNull", label: "Is null" },
    { id: "isNotNull", label: "Is not null" },
  ];

  // names lists the fields the data has, then any the options name that the
  // data lacks now, so a setting for a missing field is still visible.
  function names(keyed: Record<string, unknown> | undefined): string[] {
    const out = [...fieldNames];
    for (const k of Object.keys(keyed ?? {})) if (!out.includes(k)) out.push(k);
    return out;
  }

  function setKeyed(key: string, name: string, v: unknown): void {
    const map = { ...(value[key] ?? {}) };
    if (v === undefined || v === "" || v === false) delete map[name];
    else map[name] = v;
    onchange({ ...value, [key]: map });
  }

  // ---- Organize fields ----

  function orderOf(): string[] {
    const index: Record<string, number> = value.indexByName ?? {};
    return names(value.excludeByName).sort((a, b) => (index[a] ?? 1e9) - (index[b] ?? 1e9));
  }

  function moveField(name: string, by: number): void {
    const order = orderOf();
    const i = order.indexOf(name);
    const j = i + by;
    if (j < 0 || j >= order.length) return;
    [order[i], order[j]] = [order[j], order[i]];
    const indexByName: Record<string, number> = {};
    order.forEach((n, k) => (indexByName[n] = k));
    onchange({ ...value, indexByName });
  }

  // ---- Filter by value ----

  function filters(): any[] {
    return Array.isArray(value.filters) ? value.filters : [];
  }

  function setFilter(i: number, next: any): void {
    onchange({ ...value, filters: filters().map((f, k) => (k === i ? next : f)) });
  }

  function setCondition(i: number, patch: Record<string, unknown>): void {
    const f = filters()[i] ?? {};
    const config = { ...(f.config ?? {}), ...patch };
    setFilter(i, { ...f, config });
  }

  function setConditionOption(i: number, key: string, raw: string): void {
    const f = filters()[i] ?? {};
    const options = { ...(f.config?.options ?? {}) };
    if (raw.trim() === "") delete options[key];
    else options[key] = key === "value" && f.config?.id === "regex" ? raw : Number.isFinite(Number(raw)) ? Number(raw) : raw;
    setFilter(i, { ...f, config: { ...(f.config ?? {}), options } });
  }

  // ---- Group by ----

  function groupSpec(name: string): { operation?: string; aggregations?: string[] } {
    return value.fields?.[name] ?? {};
  }

  function setGroup(name: string, patch: Record<string, unknown>): void {
    const next = { ...groupSpec(name), ...patch };
    if (!next.operation) setKeyed("fields", name, undefined);
    else setKeyed("fields", name, next);
  }

  function toggleAggregation(name: string, reducer: string, on: boolean): void {
    const list = new Set(groupSpec(name).aggregations ?? []);
    if (on) list.add(reducer);
    else list.delete(reducer);
    setGroup(name, { aggregations: [...list] });
  }
</script>

{#if id === "organize"}
  <div class="field">
    {#if orderOf().length === 0}
      <span class="hint">Run a preview to list the fields to organize.</span>
    {/if}
    {#each orderOf() as name, i (name)}
      <div class="row organize">
        <label class="checkbox-label" title="Show this field">
          <input type="checkbox" checked={!value.excludeByName?.[name]}
                 onchange={(e) => setKeyed("excludeByName", name, !(e.target as HTMLInputElement).checked)} />
        </label>
        <span class="name" title={name}>{name}</span>
        <input class="input" aria-label="Rename {name}" placeholder="Rename" value={value.renameByName?.[name] ?? ""}
               onchange={(e) => setKeyed("renameByName", name, (e.target as HTMLInputElement).value.trim())} />
        <button type="button" class="icon" aria-label="Move {name} up" disabled={i === 0} onclick={() => moveField(name, -1)}>↑</button>
        <button type="button" class="icon" aria-label="Move {name} down" disabled={i === orderOf().length - 1} onclick={() => moveField(name, 1)}>↓</button>
      </div>
    {/each}
    <span class="hint">Unticked fields are hidden. Order and names apply after the queries run.</span>
  </div>
{:else if id === "filterByValue"}
  <div class="field">
    <div class="row">
      <select class="input" aria-label="Filter type" value={value.type ?? "include"} onchange={(e) => onchange({ ...value, type: (e.target as HTMLSelectElement).value })}>
        <option value="include">Include rows</option>
        <option value="exclude">Exclude rows</option>
      </select>
      <select class="input" aria-label="Conditions" value={value.match ?? "any"} onchange={(e) => onchange({ ...value, match: (e.target as HTMLSelectElement).value })}>
        <option value="any">matching any</option>
        <option value="all">matching all</option>
      </select>
    </div>
    {#each filters() as f, i (i)}
      <div class="card condition">
        <div class="row">
          <input class="input" list="fv-fields-{uid}" aria-label="Field" placeholder="Field" value={f.fieldName ?? ""}
                 onchange={(e) => setFilter(i, { ...f, fieldName: (e.target as HTMLInputElement).value.trim() })} />
          <select class="input" aria-label="Condition" value={f.config?.id ?? "greater"} onchange={(e) => setCondition(i, { id: (e.target as HTMLSelectElement).value })}>
            {#each CONDITIONS as c}<option value={c.id}>{c.label}</option>{/each}
          </select>
          <button type="button" class="remove" aria-label="Remove condition" onclick={() => onchange({ ...value, filters: filters().filter((_, k) => k !== i) })}>×</button>
        </div>
        {#if (f.config?.id ?? "greater") === "range"}
          <div class="row">
            <input class="input" aria-label="From" placeholder="From" value={f.config?.options?.from ?? ""} onchange={(e) => setConditionOption(i, "from", (e.target as HTMLInputElement).value)} />
            <input class="input" aria-label="To" placeholder="To" value={f.config?.options?.to ?? ""} onchange={(e) => setConditionOption(i, "to", (e.target as HTMLInputElement).value)} />
          </div>
        {:else if !["isNull", "isNotNull"].includes(f.config?.id ?? "greater")}
          <input class="input" aria-label="Value" placeholder={f.config?.id === "regex" ? "/pattern/" : "Value"} value={f.config?.options?.value ?? ""}
                 onchange={(e) => setConditionOption(i, "value", (e.target as HTMLInputElement).value)} />
        {/if}
      </div>
    {/each}
    <button type="button" class="link" onclick={() => onchange({ ...value, filters: [...filters(), { fieldName: fieldNames[0] ?? "", config: { id: "greater", options: { value: 0 } } }] })}>+ Add condition</button>
    <datalist id="fv-fields-{uid}">{#each fieldNames as n}<option value={n}></option>{/each}</datalist>
  </div>
{:else if id === "groupBy"}
  <div class="field">
    {#if names(value.fields).length === 0}
      <span class="hint">Run a preview to list the fields to group.</span>
    {/if}
    {#each names(value.fields) as name (name)}
      <div class="card">
        <div class="row">
          <span class="name" title={name}>{name}</span>
          <select class="input" aria-label="How {name} is used" value={groupSpec(name).operation ?? ""}
                  onchange={(e) => setGroup(name, { operation: (e.target as HTMLSelectElement).value || undefined })}>
            <option value="">Ignore</option>
            <option value="groupby">Group by</option>
            <option value="aggregate">Calculate</option>
          </select>
        </div>
        {#if groupSpec(name).operation === "aggregate"}
          <div class="chips">
            {#each REDUCERS as r}
              {@const on = (groupSpec(name).aggregations ?? []).includes(r.id)}
              <button type="button" class="chip" class:on title={r.description} onclick={() => toggleAggregation(name, r.id, !on)}>{r.name}</button>
            {/each}
          </div>
        {/if}
      </div>
    {/each}
  </div>
{/if}

<style>
  .organize {
    align-items: center;
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.8rem;
  }

  .organize .input {
    flex: 1;
  }

  .condition {
    gap: 0.35rem;
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
