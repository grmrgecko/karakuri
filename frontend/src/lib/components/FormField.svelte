<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    label,
    id,
    error,
    children,
  }: {
    label: string;
    id: string;
    error?: string;
    children: Snippet;
  } = $props();
</script>

<div>
  <label for={id} class="block text-sm font-medium mb-1">{label}</label>
  {@render children()}
  {#if error}
    <p class="text-xs mt-1" style="color: var(--theme-error, #f38ba8);">{error}</p>
  {/if}
</div>
