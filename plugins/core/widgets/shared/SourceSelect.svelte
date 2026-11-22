<script lang="ts">
  import { onDestroy } from "svelte";
  import { hasReference, reference, resolve, watchVariables, type BridgeVariables } from "./variables";

  // SourceSelect chooses a connection, control, or device for a binding. Next
  // to the real choices it offers the dashboard's variables as ${name}, and a
  // stored reference reads back as itself rather than as an empty choice.
  let {
    id,
    value = "",
    options,
    placeholder,
    ariaLabel,
    disabled = false,
    onchange,
  }: {
    id?: string;
    value?: string;
    options: { value: string; label: string }[];
    placeholder: string;
    ariaLabel?: string;
    disabled?: boolean;
    onchange: (value: string) => void;
  } = $props();

  let state = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  const stop = watchVariables((next) => (state = next));
  onDestroy(stop);

  let references = $derived(state.variables.map((v) => ({ value: reference(v.name), label: reference(v.name), name: v.label || v.name })));
  // A reference the dashboard no longer defines, or a value missing from the
  // list, still shows, so opening the panel never blanks a binding.
  let stray = $derived(
    value && !options.some((o) => o.value === value) && !references.some((r) => r.value === value) ? value : ""
  );
  let resolvedName = $derived.by(() => {
    if (!hasReference(value)) return "";
    const target = resolve(value, state);
    return options.find((o) => o.value === target)?.label ?? "";
  });
</script>

<select {id} class="input" {value} {disabled} aria-label={ariaLabel}
        onchange={(e: Event) => onchange((e.target as HTMLSelectElement).value)}>
  <option value="">{placeholder}</option>
  {#each options as option (option.value)}
    <option value={option.value}>{option.label}</option>
  {/each}
  {#if references.length > 0}
    <optgroup label="Dashboard variables">
      {#each references as ref (ref.value)}
        <option value={ref.value}>{ref.label} ({ref.name})</option>
      {/each}
    </optgroup>
  {/if}
  {#if stray}
    <option value={stray}>{stray}</option>
  {/if}
</select>
{#if resolvedName}
  <span class="resolves">Now {resolvedName}</span>
{/if}

<style>
  /* The panel's own input styles are scoped to it and do not reach this
     component, so the select carries the shared panel look itself. */
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

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .resolves {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
  }
</style>
