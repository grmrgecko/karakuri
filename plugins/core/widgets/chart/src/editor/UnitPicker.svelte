<script lang="ts">
  import { UNIT_OPTIONS } from "../data/units";

  // UnitPicker chooses one of the built-in units, or a custom prefix or suffix.
  let { id, value = "", onchange }: { id?: string; value?: string; onchange: (unit: string) => void } = $props();

  let groups = $derived.by(() => {
    const out = new Map<string, typeof UNIT_OPTIONS>();
    for (const u of UNIT_OPTIONS) {
      if (!out.has(u.group)) out.set(u.group, []);
      out.get(u.group)!.push(u);
    }
    return [...out.entries()];
  });

  let custom = $derived(value !== "" && !UNIT_OPTIONS.some((u) => u.id === value));
  let customText = $derived(custom ? value.replace(/^(suffix|prefix):/, "") : "");
  let customKind = $derived(value.startsWith("prefix:") ? "prefix" : "suffix");

  function pick(v: string): void {
    if (v === "__custom") onchange(`suffix:${customText}`);
    else onchange(v);
  }
</script>

<select {id} class="input" value={custom ? "__custom" : value} onchange={(e) => pick((e.target as HTMLSelectElement).value)}>
  <option value="">Default</option>
  {#each groups as [group, units]}
    <optgroup label={group}>
      {#each units as u}<option value={u.id}>{u.name}</option>{/each}
    </optgroup>
  {/each}
  <option value="__custom">Custom</option>
</select>
{#if custom}
  <div class="row">
    <select class="input" aria-label="Custom unit position" value={customKind}
            onchange={(e) => onchange(`${(e.target as HTMLSelectElement).value}:${customText}`)}>
      <option value="suffix">Suffix</option>
      <option value="prefix">Prefix</option>
    </select>
    <input class="input" aria-label="Custom unit text" value={customText}
           onchange={(e) => onchange(`${customKind}:${(e.target as HTMLInputElement).value}`)} />
  </div>
{/if}
