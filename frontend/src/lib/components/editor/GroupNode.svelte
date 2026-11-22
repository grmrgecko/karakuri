<script lang="ts">
  import { NodeResizer, type NodeProps } from '@xyflow/svelte';
  import { editor } from '../../stores/editor';
  import type { EditorNode } from './types';

  let { id, data, selected }: NodeProps<EditorNode> = $props();

  // The same palette the notes use, so an author colour-codes a canvas once.
  const colors: Record<string, { bg: string; border: string; header: string }> = {
    yellow: { bg: 'rgba(249, 226, 175, 0.07)', border: '#f9e2af', header: 'rgba(249, 226, 175, 0.16)' },
    blue: { bg: 'rgba(137, 180, 250, 0.07)', border: '#89b4fa', header: 'rgba(137, 180, 250, 0.16)' },
    green: { bg: 'rgba(166, 227, 161, 0.07)', border: '#a6e3a1', header: 'rgba(166, 227, 161, 0.16)' },
    pink: { bg: 'rgba(245, 194, 231, 0.07)', border: '#f5c2e7', header: 'rgba(245, 194, 231, 0.16)' },
    gray: { bg: 'rgba(108, 112, 134, 0.1)', border: '#6c7086', header: 'rgba(108, 112, 134, 0.25)' },
  };

  let color = $derived(colors[(data.config.color as string) || 'blue'] ?? colors.blue);
  let title = $derived(((data.config.title as string) || '').trim());
  let problem = $derived($editor.problems?.[`node:${id}`]);
</script>

<NodeResizer
  isVisible={!!selected}
  minWidth={180}
  minHeight={120}
  color={color.border}
  onResizeEnd={() => editor.markDirty()}
/>

<div
  class="group-node"
  class:selected
  class:has-problem={!!problem}
  style="background-color: {color.bg}; border-color: {color.border};"
  title={problem || ''}
>
  <!--
    Only the header takes pointer events. The frame sits behind the graph and
    its body is see-through to the mouse, so clicking the empty canvas inside a
    group pans and rubber-band-selects the way it does anywhere else, and the
    frame is picked up by its title the way it reads.
  -->
  <div class="group-header" style="background-color: {color.header};">
    <span class="group-title" class:untitled={!title}>{title || 'Group'}</span>
  </div>
</div>

<style>
  .group-node {
    width: 100%;
    height: 100%;
    border: 1px solid;
    border-radius: var(--theme-radius-sm, 4px);
    pointer-events: none;
  }

  .group-node.selected {
    box-shadow: 0 0 0 1px var(--theme-accent, #89b4fa);
  }

  .group-node.has-problem {
    box-shadow: 0 0 0 2px var(--theme-error, #f38ba8);
  }

  .group-header {
    display: flex;
    align-items: center;
    padding: 4px 10px;
    border-radius: var(--theme-radius-sm, 4px) var(--theme-radius-sm, 4px) 0 0;
    pointer-events: auto;
    cursor: grab;
  }

  .group-title {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--theme-text, #cdd6f4);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .group-title.untitled {
    opacity: 0.45;
    font-style: italic;
  }
</style>
