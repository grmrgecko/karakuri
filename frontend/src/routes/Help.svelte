<script lang="ts">
  import { tick } from 'svelte';
  import { link, navigate, useLocation } from 'svelte-routing';
  import GuideBody from '../lib/components/GuideBody.svelte';
  import { guides, type Guide, type GuideEntry } from '../lib/stores/guides';

  // Help lists every guide the server ships and shows one at a time. With no
  // id it is the index; with one it is that guide beside the list, and the
  // address's hash names the section to open on.
  let { id = '' }: { id?: string } = $props();

  const location = useLocation();

  let entries = $state<GuideEntry[]>([]);
  let indexError = $state('');
  let guide = $state<Guide | null>(null);
  let guideError = $state('');
  let body = $state<GuideBody | null>(null);
  let sections = $state<Array<{ anchor: string; label: string }>>([]);

  // groups keeps the catalog's order: a group appears where its first guide does.
  let groups = $derived.by(() => {
    const out: Array<{ name: string; items: GuideEntry[] }> = [];
    for (const entry of entries) {
      let group = out.find((g) => g.name === entry.group);
      if (!group) {
        group = { name: entry.group, items: [] };
        out.push(group);
      }
      group.items.push(entry);
    }
    return out;
  });

  guides
    .index()
    .then((list) => (entries = list))
    .catch((e: any) => (indexError = e?.message || 'Failed to load the guide list'));

  $effect(() => {
    const wanted = id;
    guide = null;
    guideError = '';
    sections = [];
    if (!wanted) return;
    guides
      .load(wanted)
      .then(async (doc) => {
        if (id !== wanted) return;
        guide = doc;
        await tick();
        sections = body?.sections() ?? [];
        openSection($location.hash);
      })
      .catch((e: any) => {
        if (id !== wanted) return;
        guideError = e?.status === 404 ? 'There is no guide with that name.' : e?.message || 'Failed to load the guide';
      });
  });

  // A hash change on the same guide, from a section link in the list, scrolls
  // rather than reloading.
  $effect(() => {
    const hash = $location.hash;
    if (guide) openSection(hash);
  });

  // openSection scrolls to the section the hash names, or to the top.
  function openSection(hash: string) {
    const anchor = decodeURIComponent((hash || '').replace(/^#/, ''));
    if (anchor && body?.scrollTo(anchor)) return;
    document.querySelector('main')?.scrollTo({ top: 0 });
  }

  function follow(target: string, anchor: string) {
    navigate(`/help/${target}${anchor ? `#${anchor}` : ''}`);
  }
</script>

<div class="help-layout">
  <aside class="sidebar">
    <a href="/help" use:link class="sidebar-item index-link" class:active={!id}>
      <i class="las la-book"></i>
      <span>All guides</span>
    </a>
    {#each groups as group (group.name)}
      <div class="sidebar-group">{group.name}</div>
      {#each group.items as entry (entry.id)}
        <a href="/help/{entry.id}" use:link class="sidebar-item" class:active={entry.id === id}>
          <span>{entry.title}</span>
        </a>
        {#if entry.id === id && sections.length > 0}
          <nav class="sections" aria-label="Sections of {entry.title}">
            {#each sections as section (section.anchor)}
              <a href="/help/{entry.id}#{section.anchor}" use:link class="section-link"
                 class:current={$location.hash === `#${section.anchor}`}>{section.label}</a>
            {/each}
          </nav>
        {/if}
      {/each}
    {/each}
  </aside>

  <section class="help-content">
    {#if !id}
      <h1 class="text-2xl font-bold mb-2">Help</h1>
      <p class="text-sm mb-6 text-text-muted">
        Guides for setting up and using Karakuri. The same guides open from the editor panels
        that link to them.
      </p>
      {#if indexError}
        <p class="error">{indexError}</p>
      {/if}
      {#each groups as group (group.name)}
        <h2 class="group-heading">{group.name}</h2>
        <div class="cards">
          {#each group.items as entry (entry.id)}
            <a href="/help/{entry.id}" use:link class="card guide-card">
              <span class="guide-title">{entry.title}</span>
              <span class="guide-summary">{entry.summary}</span>
            </a>
          {/each}
        </div>
      {/each}
    {:else if guideError}
      <p class="error">{guideError}</p>
      <p class="text-sm"><a href="/help" use:link class="link">See every guide</a></p>
    {:else if !guide}
      <p class="text-sm text-text-muted">Loading…</p>
    {:else}
      <GuideBody bind:this={body} html={guide.html} onnavigate={follow} />
    {/if}
  </section>
</div>

<style>
  /* Fills the scroll area <main> gives it. It must not shrink to that height
     as a flex item, or the sticky sidebar would scroll away with the page. */
  .help-layout {
    display: flex;
    flex-shrink: 0;
    min-height: 100%;
  }

  .sidebar {
    position: sticky;
    top: 0;
    align-self: flex-start;
    width: 260px;
    flex-shrink: 0;
    max-height: calc(100dvh - 4rem);
    overflow-y: auto;
    padding: 1rem 0.5rem;
    border-right: 1px solid var(--theme-border, var(--theme-surface0, #313244));
    background-color: var(--theme-nav-bg, var(--theme-mantle, #181825));
  }

  .sidebar-group {
    margin: 1rem 0 0.25rem;
    padding: 0 1rem;
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    opacity: 0.6;
  }

  .sidebar-item {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.4rem 1rem;
    border-radius: 4px;
    border-left: 3px solid transparent;
    font-size: 0.85rem;
    color: var(--theme-text, #cdd6f4);
    text-decoration: none;
  }

  .sidebar-item:hover {
    background-color: var(--theme-surface0, #313244);
  }

  .sidebar-item.active {
    background-color: var(--theme-surface0, #313244);
    color: var(--theme-accent, #89b4fa);
    border-left-color: var(--theme-accent, #89b4fa);
  }

  .index-link {
    font-weight: 500;
  }

  .sections {
    display: flex;
    flex-direction: column;
    margin: 0.15rem 0 0.4rem 1.5rem;
    border-left: 1px solid var(--theme-surface1, #45475a);
  }

  .section-link {
    padding: 0.2rem 0.75rem;
    font-size: 0.78rem;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.75;
    text-decoration: none;
  }

  .section-link:hover,
  .section-link.current {
    opacity: 1;
    color: var(--theme-accent, #89b4fa);
  }

  .help-content {
    flex: 1;
    min-width: 0;
    padding: 1.5rem 2rem 4rem;
  }

  .group-heading {
    margin: 1.5rem 0 0.75rem;
    font-size: 1rem;
    font-weight: 600;
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 0.75rem;
  }

  .guide-card {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    padding: 0.9rem 1rem;
    text-decoration: none;
    color: var(--theme-text, #cdd6f4);
    transition: border-color 0.15s;
  }

  .guide-card:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .guide-title {
    font-weight: 600;
    color: var(--theme-accent, #89b4fa);
  }

  .guide-summary {
    font-size: 0.8rem;
    line-height: 1.45;
    opacity: 0.8;
  }

  .error {
    color: var(--theme-error, #f38ba8);
    font-size: 0.9rem;
  }

  @media (max-width: 760px) {
    .help-layout {
      flex-direction: column;
    }

    .sidebar {
      position: static;
      width: 100%;
      max-height: none;
      border-right: none;
      border-bottom: 1px solid var(--theme-border, var(--theme-surface0, #313244));
    }

    .help-content {
      padding: 1rem;
    }
  }
</style>
