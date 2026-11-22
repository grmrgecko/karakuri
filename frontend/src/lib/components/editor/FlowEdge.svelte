<script lang="ts">
  // FlowEdge is the default edge renderer: the standard bezier path plus a
  // clickable dot at the midpoint. The dot opens the edge
  // action menu (insert a node, add a reroute, convert to Set/Get, delete),
  // which EditorShell owns — this component only names the edge and where the
  // click landed.
  import { BaseEdge, EdgeLabel, getBezierPath, type EdgeProps } from '@xyflow/svelte';
  import { onDestroy } from 'svelte';
  import { editor } from '../../stores/editor';

  let {
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    markerStart,
    markerEnd,
    interactionWidth,
    style,
    data,
  }: EdgeProps = $props();

  let [path, labelX, labelY] = $derived(
    getBezierPath({
      sourceX,
      sourceY,
      targetX,
      targetY,
      sourcePosition,
      targetPosition,
    })
  );

  // typeColor is the color of the payload type this edge carries, computed by
  // EditorShell's type pass. It is handed to the path as CSS variables rather
  // than a stroke, so the semantic styles — selection, error branch, invalid —
  // still win over it, and the midpoint dot takes the same color.
  let typeColor = $derived((data as { typeColor?: string } | undefined)?.typeColor);
  let pathStyle = $derived(
    typeColor
      ? `${style ?? ''};--xy-edge-stroke: ${typeColor};--edge-type-color: ${typeColor}`
      : style
  );

  // A press on the dot is either a click or a drag. A click opens the edge
  // menu; dragging bends the wire straight away, so the shell inserts a
  // reroute where the press landed and the elbow follows the pointer from
  // there. The gesture only counts as a drag once the pointer has travelled
  // far enough that it cannot be a click.
  const dragThreshold = 4;

  let press: { x: number; y: number } | null = null;
  let dragged = false;

  function startPress(event: PointerEvent) {
    if (event.button !== 0) return;
    // The dot owns the gesture: letting it through would start a canvas pan
    // under the reroute the drag is about to create.
    event.stopPropagation();
    press = { x: event.clientX, y: event.clientY };
    dragged = false;
    window.addEventListener('pointermove', trackPress);
    window.addEventListener('pointerup', endPress);
    window.addEventListener('pointercancel', endPress);
  }

  function trackPress(event: PointerEvent) {
    if (!press) return;
    if (
      Math.abs(event.clientX - press.x) < dragThreshold &&
      Math.abs(event.clientY - press.y) < dragThreshold
    ) {
      return;
    }
    // Handing the gesture over replaces this edge with the two hops through
    // the reroute, destroying this component, so the listeners come off first.
    const { x, y } = press;
    dragged = true;
    endPress();
    editor.requestEdgeReroute(id, x, y);
  }

  function endPress() {
    press = null;
    window.removeEventListener('pointermove', trackPress);
    window.removeEventListener('pointerup', endPress);
    window.removeEventListener('pointercancel', endPress);
  }

  onDestroy(endPress);

  // openMenu answers the click that ends a press that never became a drag.
  // A bent wire has already done its work, so the menu stays shut.
  function openMenu(event: MouseEvent) {
    event.stopPropagation();
    if (dragged) {
      dragged = false;
      return;
    }
    editor.requestEdgeMenu(id, event.clientX, event.clientY);
  }
</script>

<BaseEdge {path} {markerStart} {markerEnd} {interactionWidth} style={pathStyle} />

{#if !$editor.canvasReadOnly}
  <EdgeLabel x={labelX} y={labelY} transparent>
    <button
      class="edge-dot nodrag nopan"
      style={typeColor ? `--edge-dot-color: ${typeColor}` : undefined}
      aria-label="Edge actions"
      title="Click for edge actions, drag to bend the wire"
      onpointerdown={startPress}
      onclick={openMenu}
    ></button>
  </EdgeLabel>
{/if}

<style>
  .edge-dot {
    display: block;
    width: 9px;
    height: 9px;
    padding: 0;
    border-radius: 50%;
    background-color: var(--edge-dot-color, var(--theme-overlay0, #6c7086));
    border: 1px solid var(--theme-crust, #11111b);
    cursor: pointer;
    transition:
      transform 120ms,
      background-color 120ms;
  }

  .edge-dot:hover {
    transform: scale(1.7);
    background-color: var(--theme-accent, #89b4fa);
  }
</style>
