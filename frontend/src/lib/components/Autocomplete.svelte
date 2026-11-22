<script lang="ts">
  import type { Snippet } from 'svelte';

  // Wraps an input in a positioned dropdown. The caller owns the input, the
  // suggestion list, and all keyboard handling; this only handles placement.
  let {
    show,
    maxHeight = '240px',
    children,
    items,
  }: {
    show: boolean;
    maxHeight?: string;
    children: Snippet;
    items: Snippet;
  } = $props();
</script>

<div class="autocomplete-wrapper">
  {@render children()}
  {#if show}
    <div class="autocomplete-dropdown" style="max-height: {maxHeight};">
      {@render items()}
    </div>
  {/if}
</div>

<style>
  .autocomplete-wrapper {
    position: relative;
    width: 100%;
  }

  .autocomplete-dropdown {
    position: absolute;
    top: 100%;
    left: 0;
    right: 0;
    z-index: 50;
    overflow-y: auto;
    border-radius: var(--theme-radius-md, 8px);
    background-color: var(--theme-surface0, #313244);
    border: 1px solid var(--theme-surface1, #45475a);
    box-shadow: var(--theme-shadow-md, 0 4px 6px rgba(0, 0, 0, 0.1));
    margin-top: 2px;
  }
</style>
