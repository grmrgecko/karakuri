<script lang="ts">
  import type { Snippet } from 'svelte';
  import { navigate } from 'svelte-routing';
  import { api } from '../api/client';

  // ResolveRef turns a flow or dashboard address into its ID. An ID renders
  // the page at once. A slug is looked up among the items the viewer can see:
  // one match replaces the address with its ID, several offer a choice, and
  // none is not found. Slugs are not unique, so the ID is what every page and
  // link works with.
  let {
    kind,
    ref,
    suffix = '',
    children,
  }: {
    kind: 'flow' | 'dashboard';
    ref: string;
    suffix?: string;
    children: Snippet<[string]>;
  } = $props();

  interface SlugMatch {
    id: string;
    name: string;
    owner?: string;
  }

  const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

  let isID = $derived(uuidPattern.test(ref));
  let matches = $state<SlugMatch[] | null>(null);
  let failed = $state('');

  let base = $derived(kind === 'flow' ? '/flows' : '/dashboard');
  let noun = $derived(kind === 'flow' ? 'flow' : 'dashboard');

  function open(id: string) {
    navigate(`${base}/${id}${suffix}${window.location.search}`, { replace: true });
  }

  $effect(() => {
    if (isID) return;
    const slug = ref;
    matches = null;
    failed = '';
    const path = kind === 'flow' ? `/flows/by_slug/${encodeURIComponent(slug)}` : `/dashboards/by_slug/${encodeURIComponent(slug)}`;
    api
      .get<SlugMatch[]>(path)
      .then((found) => {
        if (slug !== ref) return;
        if (found.length === 1) {
          open(found[0].id);
          return;
        }
        matches = found;
      })
      .catch((e: any) => {
        failed = e?.message || `The ${noun} could not be looked up.`;
      });
  });
</script>

{#if isID}
  {@render children(ref)}
{:else if failed}
  <div class="resolve-message">{failed}</div>
{:else if matches === null}
  <div class="resolve-message animate-pulse">Loading...</div>
{:else if matches.length === 0}
  <div class="resolve-message">No {noun} you can open uses the address "{ref}".</div>
{:else}
  <div class="resolve-choice">
    <h1 class="text-xl font-bold mb-1">Several {noun}s use "{ref}"</h1>
    <p class="text-sm mb-4 opacity-70">Choose one. Its link uses the {noun}'s ID from now on.</p>
    <div class="flex flex-col gap-2">
      {#each matches as match (match.id)}
        <button class="resolve-item" onclick={() => open(match.id)}>
          <span class="font-medium">{match.name}</span>
          {#if match.owner}<span class="text-xs opacity-60">owned by {match.owner}</span>{/if}
        </button>
      {/each}
    </div>
  </div>
{/if}

<style>
  .resolve-message {
    padding: 3rem 1.5rem;
    text-align: center;
    opacity: 0.7;
  }

  .resolve-choice {
    max-width: 32rem;
    margin: 0 auto;
    padding: 2rem 1.5rem;
  }

  .resolve-item {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 1rem;
    width: 100%;
    padding: 0.75rem 1rem;
    border-radius: 0.5rem;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-surface0, #313244);
    color: inherit;
    text-align: left;
    cursor: pointer;
  }

  .resolve-item:hover {
    border-color: var(--theme-accent, #89b4fa);
  }
</style>
