<script lang="ts">
  // Text commits on blur or Enter, not on every keystroke: each commit is a
  // write that reaches every other dashboard showing the same control.
  let {
    value = "",
    multiline = false,
    disabled = false,
    oncommit,
  }: {
    value?: string;
    multiline?: boolean;
    disabled?: boolean;
    oncommit?: (next: string) => void | Promise<void>;
  } = $props();
</script>

{#if multiline}
  <textarea
    class="field"
    rows="2"
    {disabled}
    value={value ?? ""}
    onchange={(e) => oncommit?.(e.currentTarget.value)}
  ></textarea>
{:else}
  <input
    class="field"
    type="text"
    {disabled}
    value={value ?? ""}
    onchange={(e) => oncommit?.(e.currentTarget.value)}
  />
{/if}

<style>
  .field {
    padding: 0.15rem 0.35rem;
    max-width: 9rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.8rem;
  }

  .field:disabled {
    opacity: 0.6;
  }
</style>
