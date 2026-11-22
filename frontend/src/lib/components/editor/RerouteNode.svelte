<script lang="ts">
  // RerouteNode draws the reroute type as a bare movable dot rather than a
  // card: it is an elbow in a wire, and the circle is the
  // whole node. The handle grab areas stay small so the middle of the circle
  // remains a drag surface for moving it.
  import { Handle, useUpdateNodeInternals, type NodeProps } from '@xyflow/svelte';
  import { carriedTypes } from '../../stores/carriedTypes';
  import { editor } from '../../stores/editor';
  import { portTypeLabel, PortAny } from '../../utils/payloadPorts';
  import RotateGrips from './RotateGrips.svelte';
  import { handleSides, nodeRotation } from './rotation';
  import type { EditorNode } from './types';

  let { id, data, selected }: NodeProps<EditorNode> = $props();

  // A reroute turns like a card does, and it is the one node where turning is
  // the whole point: the circle looks the same at every angle, so all the
  // rotation does is decide which way the elbow's wires leave, which is how a
  // wire is bent downward rather than around.
  let rotation = $derived(nodeRotation(data.config));
  let sides = $derived(handleSides(rotation));

  // Turning moves the sockets without changing the size Svelte Flow re-measures
  // on its own, so the edges only follow once the internals are refreshed.
  const updateInternals = useUpdateNodeInternals();
  let measuredRotation = 0;
  $effect(() => {
    if (rotation === measuredRotation) return;
    measuredRotation = rotation;
    updateInternals(id);
  });

  // The carried type is the only thing a reroute could say about itself, so
  // it is surfaced as the tooltip.
  let carried = $derived($carriedTypes[id]?.out);
  let title = $derived(
    carried && carried !== PortAny ? `Reroute (carries a ${portTypeLabel(carried).toLowerCase()})` : 'Reroute'
  );
</script>

<div
  class="reroute"
  class:selected
  class:rotated={rotation !== 0}
  style={rotation ? `--node-rotation: ${rotation}deg` : undefined}
  {title}
>
  <Handle type="target" position={sides.input} id="in" />
  <Handle type="source" position={sides.output} id="out" />
  {#if selected && !$editor.canvasReadOnly}
    <!-- The dot is 22px across, so its grips are smaller than a card's and
         still clear the sockets on the rim. -->
    <RotateGrips nodeId={id} {rotation} size={14} />
  {/if}
</div>

<style>
  .reroute {
    position: relative;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    background-color: var(--theme-surface1, #45475a);
    border: 2px solid var(--theme-overlay0, #6c7086);
    box-shadow: 0 2px 6px rgba(0, 0, 0, 0.3);
  }

  .reroute.selected {
    border-color: var(--theme-accent, #89b4fa);
    box-shadow: 0 0 0 1px var(--theme-accent, #89b4fa);
  }

  /* The dot turns about its own centre, which moves the sockets and nothing
     else: the circle draws the same at every angle. */
  .reroute.rotated {
    transform: rotate(var(--node-rotation, 0deg));
  }

  /* Sockets stay on the rim however far the dot is turned, whatever Position
     the handle declares — the declared position aims the wire, it does not
     place the socket. Counter-turning the handle keeps its box square to the
     screen, which is what Svelte Flow measures the edge endpoint from. */
  .reroute :global(.svelte-flow__handle.target) {
    top: 50%;
    bottom: auto;
    left: 0;
    right: auto;
    transform: translate(-50%, -50%) rotate(calc(-1 * var(--node-rotation, 0deg)));
  }

  .reroute :global(.svelte-flow__handle.source) {
    top: 50%;
    bottom: auto;
    left: auto;
    right: 0;
    transform: translate(50%, -50%) rotate(calc(-1 * var(--node-rotation, 0deg)));
  }

  /* Handles hug the rim: small enough that the circle's middle still drags
     the node, drawn as dots the same way FlowNode's sockets are. */
  .reroute :global(.svelte-flow__handle) {
    width: 12px;
    height: 12px;
    background-color: transparent;
    border: none;
    border-radius: 50%;
  }

  .reroute :global(.svelte-flow__handle)::after {
    content: '';
    position: absolute;
    top: 50%;
    left: 50%;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    transform: translate(-50%, -50%);
    background-color: var(--theme-overlay0, #6c7086);
    border: 1px solid var(--theme-crust, #11111b);
    transition:
      background-color 120ms,
      transform 120ms;
  }

  .reroute :global(.svelte-flow__handle:hover)::after,
  .reroute :global(.svelte-flow__handle.connectingto)::after,
  .reroute :global(.svelte-flow__handle.valid)::after {
    background-color: var(--theme-accent, #89b4fa);
    transform: translate(-50%, -50%) scale(1.3);
  }
</style>
