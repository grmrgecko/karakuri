<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    title,
    onclose,
    maxWidth = 'md',
    maxHeight,
    zIndex = 50,
    children,
  }: {
    title: string;
    onclose: () => void;
    maxWidth?: 'md' | 'lg' | '2xl' | '4xl' | 'wide';
    maxHeight?: string;
    // zIndex lets a modal stack above others (e.g. confirm over a modal).
    zIndex?: number;
    children: Snippet;
  } = $props();

  const widthClass: Record<string, string> = {
    md: 'max-w-md',
    lg: 'max-w-lg',
    '2xl': 'max-w-2xl',
    '4xl': 'max-w-4xl',
    // wide is for the script editor: as much room as the viewport allows.
    wide: 'max-w-[min(1400px,94vw)]',
  };
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  class="modal-backdrop fixed inset-0"
  style="background: rgba(0,0,0,0.5); z-index: {zIndex};"
  onclick={(e: MouseEvent) => { if (e.target === e.currentTarget) onclose(); }}
  onkeydown={(e: KeyboardEvent) => { if (e.key === 'Escape') onclose(); }}
  role="dialog"
  aria-modal="true"
  tabindex="-1"
>
  <div
    class="modal-card card p-6 w-full {widthClass[maxWidth]} relative {maxHeight
      ? 'flex flex-col'
      : ''}"
    style={maxHeight ? `max-height: ${maxHeight};` : ''}
  >
    <button
      class="absolute top-4 right-4 opacity-60 hover:opacity-100 text-lg"
      onclick={onclose}
      aria-label="Close"
    >
      <i class="las la-times"></i>
    </button>
    <h2 class="text-xl font-bold mb-4">{title}</h2>
    <div class={maxHeight ? 'flex flex-col gap-4 flex-1 overflow-y-auto' : 'flex flex-col gap-4'}>
      {@render children()}
    </div>
  </div>
</div>

<style>
  /* A dialog taller than the viewport has to scroll, and the backdrop is what
     scrolls: the card keeps its own height so its content is not squeezed.

     Centring is done with `margin: auto` on the card rather than
     `align-items: center` on the backdrop. They look identical while the card
     fits, but a flex item centred by `align-items` overflows *both* ends of a
     scroll container and the top is then unreachable — you can scroll down to
     the rest of a long form but never back up to its first field. Auto margins
     centre the same way and collapse to the start when there is no room, so the
     top stays where scrolling can reach it. */
  .modal-backdrop {
    display: flex;
    justify-content: center;
    overflow-y: auto;
    overscroll-behavior: contain;
    padding: 2rem 1rem;
  }

  /* No height cap here on purpose. A card that asked for a maxHeight scrolls
     inside itself and gets one inline; a card that did not has no inner
     scroller, so capping it would clip the rest of the form instead of letting
     the backdrop scroll to it. */
  .modal-card {
    margin: auto;
  }
</style>
