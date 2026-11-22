<script lang="ts">
  import type { Thresholds } from "../data/types";

  // ThresholdsEditor edits steps from highest to lowest.
  // The base step has no value and covers everything below the next.
  let { value, onchange }: { value: Thresholds | undefined; onchange: (next: Thresholds) => void } = $props();

  const uid = Math.random().toString(36).slice(2, 8);
  const COLORS = ["green", "red", "orange", "yellow", "blue", "purple", "teal", "sky", "lavender", "pink", "text", "transparent"];

  let t = $derived<Thresholds>(value ?? { mode: "absolute", steps: [{ value: null, color: "green" }, { value: 80, color: "red" }] });
  let ordered = $derived(
    t.steps.map((s, i) => ({ s, i })).sort((a, b) => (b.s.value ?? -Infinity) - (a.s.value ?? -Infinity)),
  );

  function edit(i: number, patch: Partial<{ value: number | null; color: string }>): void {
    onchange({ ...t, steps: t.steps.map((s, k) => (k === i ? { ...s, ...patch } : s)) });
  }

  function add(): void {
    const top = Math.max(0, ...t.steps.map((s) => s.value ?? 0));
    onchange({ ...t, steps: [...t.steps, { value: top + 10, color: COLORS[t.steps.length % 6] }] });
  }

  function remove(i: number): void {
    onchange({ ...t, steps: t.steps.filter((_, k) => k !== i) });
  }
</script>

<div class="field">
  <div class="row">
    <select class="input" aria-label="Thresholds mode" value={t.mode}
            onchange={(e) => onchange({ ...t, mode: (e.target as HTMLSelectElement).value as Thresholds["mode"] })}>
      <option value="absolute">Absolute</option>
      <option value="percentage">Percentage</option>
    </select>
    <button type="button" class="add" onclick={add}>+ Add threshold</button>
  </div>
  {#each ordered as { s, i } (i)}
    <div class="row step">
      <input class="input color" list="th-colors-{uid}" value={s.color} aria-label="Threshold colour"
             onchange={(e) => edit(i, { color: (e.target as HTMLInputElement).value.trim() || "green" })} />
      {#if s.value === null || s.value === undefined}
        <span class="base">Base</span>
      {:else}
        <input class="input" type="number" step="any" value={s.value} aria-label="Threshold value"
               onchange={(e) => {
                 const n = Number((e.target as HTMLInputElement).value);
                 if (Number.isFinite(n)) edit(i, { value: n });
               }} />
        <button type="button" class="remove" aria-label="Remove threshold" onclick={() => remove(i)}>×</button>
      {/if}
    </div>
  {/each}
  <span class="hint">Colours are names such as green or dark-red, or hex values. {t.mode === "percentage" ? "Values are percentages of min to max." : ""}</span>
</div>
<datalist id="th-colors-{uid}">
  {#each COLORS as color}<option value={color}></option>{/each}
</datalist>

<style>
  .base {
    flex: 1;
    font-size: 0.8rem;
    color: var(--theme-text-muted, #a6adc8);
    padding: 0.4rem 0.2rem;
  }
</style>
