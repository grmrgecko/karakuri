<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    title,
    level = 'page',
    children,
  }: {
    title: string;
    level?: 'page' | 'section';
    children?: Snippet;
  } = $props();
</script>

{#if children}
  <div class="flex items-center justify-between {level === 'section' ? 'mb-4' : 'mb-6'}">
    {#if level === 'section'}
      <h3 class="text-lg font-semibold">{title}</h3>
    {:else}
      <h2 class="text-xl font-bold">{title}</h2>
    {/if}
    {@render children()}
  </div>
{:else}
  {#if level === 'section'}
    <h3 class="text-lg font-semibold mb-4">{title}</h3>
  {:else}
    <h2 class="text-xl font-bold mb-6">{title}</h2>
  {/if}
{/if}
