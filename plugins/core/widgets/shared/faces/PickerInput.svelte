<script lang="ts">
  import ActionButton from "./ActionButton.svelte";
  import type { Choice } from "../controlKinds";

  // A selection, drawn as a dropdown or as a row of keys. Which one is the
  // kind's own setting, declared where the control is: two or three short
  // options on a wall panel are worth a row, and twenty are not.
  let {
    value = "",
    options = [],
    display = "dropdown",
    disabled = false,
    onpick,
  }: {
    value?: string;
    options?: Choice[];
    display?: string;
    disabled?: boolean;
    onpick?: (next: string) => void | Promise<void>;
  } = $props();
</script>

{#if display === "segmented"}
  <span class="segments">
    {#each options as option (option.value)}
      <ActionButton
        label={option.label}
        active={option.value === value}
        {disabled}
        onpress={() => onpick?.(option.value)}
      />
    {/each}
  </span>
{:else}
  <select
    class="picker"
    {disabled}
    value={value ?? ""}
    onchange={(e) => onpick?.(e.currentTarget.value)}
  >
    {#if !options.some((o) => o.value === value)}
      <option value={value ?? ""}>{value || "—"}</option>
    {/if}
    {#each options as option (option.value)}
      <option value={option.value}>{option.label}</option>
    {/each}
  </select>
{/if}

<style>
  .segments {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    justify-content: flex-end;
  }

  .picker {
    padding: 0.15rem 0.35rem;
    max-width: 9rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.8rem;
  }

  .picker:disabled {
    opacity: 0.6;
  }
</style>
