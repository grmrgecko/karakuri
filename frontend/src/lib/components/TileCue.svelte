<script lang="ts">
  import type { StatusCue } from '../stores/dashboards';
  import { safeColor } from '../tileStyle';

  // TileCue draws a status cue's border over a placement's cell. It takes no
  // pointer events, so the widget under it stays pressable, and it inherits
  // the cell's corner radius.
  let { cue, still = false }: { cue: StatusCue; still?: boolean } = $props();

  let color = $derived(safeColor(cue.color) || 'var(--theme-warning, #f9e2af)');
  let animation = $derived(cue.animation || 'static');
</script>

<div
  class="cue {animation}"
  class:still
  style="--cue:{color};"
  data-cue={animation}
  aria-hidden="true"
></div>

<style>
  @property --cue-angle {
    syntax: '<angle>';
    initial-value: 0deg;
    inherits: false;
  }

  .cue {
    position: absolute;
    inset: 0;
    z-index: 5;
    pointer-events: none;
    border-radius: inherit;
    border: 3px solid var(--cue);
    box-sizing: border-box;
  }

  .pulse {
    animation: cue-pulse 1.6s ease-in-out infinite;
  }

  .blink {
    animation: cue-blink 1s steps(1, end) infinite;
  }

  .breathe {
    animation: cue-breathe 3s ease-in-out infinite alternate;
  }

  /* A bright arc runs around a dim ring: the gradient turns, and the mask
     keeps only the border band of it. */
  .comet {
    border: none;
    padding: 3px;
    background:
      conic-gradient(from var(--cue-angle), transparent 0 70%, var(--cue) 100%),
      color-mix(in srgb, var(--cue) 30%, transparent);
    mask:
      linear-gradient(#000 0 0) content-box exclude,
      linear-gradient(#000 0 0);
    animation: cue-turn 2s linear infinite;
  }

  .ants {
    border: none;
    background-image:
      linear-gradient(90deg, var(--cue) 50%, transparent 50%),
      linear-gradient(90deg, var(--cue) 50%, transparent 50%),
      linear-gradient(0deg, var(--cue) 50%, transparent 50%),
      linear-gradient(0deg, var(--cue) 50%, transparent 50%);
    background-repeat: repeat-x, repeat-x, repeat-y, repeat-y;
    background-size: 14px 3px, 14px 3px, 3px 14px, 3px 14px;
    background-position: 0 0, 0 100%, 0 0, 100% 0;
    animation: cue-ants 0.8s linear infinite;
  }

  /* A render and a viewer asking for less motion keep the color and drop the
     movement. A comet without motion is its ring at full strength. */
  .still,
  .still.comet,
  .still.ants {
    animation: none;
  }

  .still.comet {
    background: var(--cue);
  }

  @media (prefers-reduced-motion: reduce) {
    .cue {
      animation: none !important;
    }
    .comet {
      background: var(--cue);
    }
  }

  @keyframes cue-pulse {
    0%,
    100% {
      box-shadow: 0 0 0 0 color-mix(in srgb, var(--cue) 70%, transparent);
    }
    50% {
      box-shadow: 0 0 14px 4px color-mix(in srgb, var(--cue) 70%, transparent);
    }
  }

  @keyframes cue-blink {
    50% {
      opacity: 0;
    }
  }

  @keyframes cue-breathe {
    from {
      opacity: 1;
    }
    to {
      opacity: 0.2;
    }
  }

  @keyframes cue-turn {
    to {
      --cue-angle: 360deg;
    }
  }

  @keyframes cue-ants {
    to {
      background-position: 14px 0, -14px 100%, 0 -14px, 100% 14px;
    }
  }
</style>
