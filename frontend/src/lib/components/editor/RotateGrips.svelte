<script lang="ts">
  // RotateGrips is the rotation affordance a turnable node draws: four unmarked
  // squares just off its corners, the one patch of edge no socket reaches. They
  // must be rendered as direct children of the element that turns, which is the
  // box the gesture measures its angle about, and only while the node is
  // selected — the pane around an idle node belongs to the canvas.
  import { startNodeRotate } from './rotation';

  // size is both the grip's side and how far it hangs past the corner, so the
  // grips sit wholly outside a node of any size.
  let {
    nodeId,
    rotation,
    size = 18,
  }: { nodeId: string; rotation: number; size?: number } = $props();

  const corners = ['tl', 'tr', 'bl', 'br'];
</script>

{#each corners as corner (corner)}
  <button
    type="button"
    class="rotate-grip {corner} nodrag nopan"
    style="--grip-size: {size}px"
    tabindex="-1"
    aria-label="Rotate node"
    title="Drag to rotate, hold Shift to lock to quarter turns"
    onpointerdown={(e: PointerEvent) => startNodeRotate(e, nodeId, rotation)}
  ></button>
{/each}

<style>
  /* Nothing is drawn: the rotation cursor is the whole affordance. */
  .rotate-grip {
    position: absolute;
    width: var(--grip-size);
    height: var(--grip-size);
    padding: 0;
    border: none;
    background: none;
    cursor:
      url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='24' height='24'><path d='M12 4 A8 8 0 1 1 4 12' fill='none' stroke='white' stroke-width='4' stroke-linecap='round'/><path d='M12 4 A8 8 0 1 1 4 12' fill='none' stroke='black' stroke-width='2' stroke-linecap='round'/><path d='M12 0.5 L12 7.5 L16.5 4 Z' fill='black' stroke='white' stroke-width='1.5' stroke-linejoin='round'/></svg>")
        12 12,
      alias;
  }

  .rotate-grip.tl {
    left: calc(-1 * var(--grip-size));
    top: calc(-1 * var(--grip-size));
  }

  .rotate-grip.tr {
    right: calc(-1 * var(--grip-size));
    top: calc(-1 * var(--grip-size));
  }

  .rotate-grip.bl {
    left: calc(-1 * var(--grip-size));
    bottom: calc(-1 * var(--grip-size));
  }

  .rotate-grip.br {
    right: calc(-1 * var(--grip-size));
    bottom: calc(-1 * var(--grip-size));
  }
</style>
