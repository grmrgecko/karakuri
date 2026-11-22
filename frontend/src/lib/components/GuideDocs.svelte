<script lang="ts">
  import { navigate } from 'svelte-routing';
  import Modal from './Modal.svelte';
  import GuideBody from './GuideBody.svelte';
  import { guides, type Guide } from '../stores/guides';

  // GuideDocs shows one reference page: the server renders the markdown, this
  // shows the HTML and styles it. Every page is displayed through here, so a
  // guide opened from the script editor and one opened from a dashboard widget
  // read identically and there is one copy of the prose styling.
  let {
    guideId,
    title = '',
    sections = [],
    current = '',
    onclose,
  }: {
    guideId: string;
    // title overrides the page's own heading in the modal bar. Callers that
    // know the page by a name of their own set it; the rest let the page say
    // what it is once it arrives.
    title?: string;
    // sections are the headings worth jumping to, by the anchor goldmark
    // generated for them. Empty means the page is short enough to scroll.
    sections?: Array<{ anchor: string; label: string }>;
    // current is the section to open on, for a page whose caller knows which
    // part answers the question the reader most likely has.
    current?: string;
    onclose: () => void;
  } = $props();

  let guide = $state<Guide | null>(null);
  let error = $state('');
  let surface = $state<HTMLDivElement | null>(null);

  let barTitle = $derived(title || guide?.title || 'Reference');

  $effect(() => {
    guides
      .load(guideId)
      .then((doc) => {
        guide = doc;
      })
      .catch((e: any) => {
        error = e?.message || 'Failed to load the reference';
      });
  });

  // Jump to the opening section once the page is on screen. It runs after the
  // HTML lands rather than on open, since the anchor does not exist until then.
  $effect(() => {
    if (!guide || !surface) return;
    jump(current);
  });

  // openInHelp follows a link to another guide by leaving the editor for the
  // help pages, where the reader can keep moving between guides.
  function openInHelp(id: string, anchor: string) {
    onclose();
    navigate(`/help/${id}${anchor ? `#${anchor}` : ''}`);
  }

  // jump scrolls the reference to a heading. The offset is computed against the
  // scrolling element rather than using scrollIntoView, which would also scroll
  // the page behind the modal.
  function jump(anchor: string) {
    if (!surface) return;
    if (!sections.some((s) => s.anchor === anchor)) {
      surface.scrollTop = 0;
      return;
    }
    const target = surface.querySelector<HTMLElement>(`#${CSS.escape(anchor)}`);
    if (!target) return;
    surface.scrollTop = target.offsetTop - surface.offsetTop;
  }
</script>

<Modal title={barTitle} {onclose} maxWidth="wide" zIndex={60}>
  {#if sections.length > 0}
    <div class="docs-nav">
      <button type="button" class="docs-jump" onclick={() => jump('top')}>
        <i class="las la-arrow-up"></i> Top
      </button>
      {#each sections as section (section.anchor)}
        <button
          type="button"
          class="docs-jump"
          class:current={section.anchor === current}
          onclick={() => jump(section.anchor)}
        >
          {section.label}
        </button>
      {/each}
    </div>
  {/if}

  <div class="docs-surface" bind:this={surface}>
    {#if error}
      <p class="docs-error">{error}</p>
    {:else if !guide}
      <p class="docs-loading">Loading…</p>
    {:else}
      <GuideBody html={guide.html} onnavigate={openInHelp} />
    {/if}
  </div>
</Modal>

<style>
  .docs-nav {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 11px;
  }

  .docs-jump {
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: transparent;
    color: inherit;
    font-size: 11px;
    cursor: pointer;
    opacity: 0.75;
  }

  .docs-jump:hover {
    opacity: 1;
    border-color: var(--theme-accent, #89b4fa);
  }

  .docs-jump.current {
    opacity: 1;
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .docs-surface {
    max-height: 76vh;
    overflow-y: auto;
    padding: 4px 16px 16px;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background-color: var(--theme-crust, #11111b);
  }

  .docs-error {
    font-size: 12px;
    color: var(--theme-error, #f38ba8);
  }

  .docs-loading {
    font-size: 12px;
    opacity: 0.6;
  }

</style>
