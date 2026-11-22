<script lang="ts">
  import type { TimeComponents } from "../timeUtils.ts";

  let {
    time,
    showSeconds = true,
    format = "24h",
  }: {
    time: TimeComponents;
    showSeconds?: boolean;
    format?: string;
  } = $props();

  // Build the visible time string and the ghost string that shows all segments.
  let display: string = $derived(
    showSeconds
      ? `${time.hours}:${time.minutes}:${time.seconds}`
      : `${time.hours}:${time.minutes}`
  );

  // The ghost layer shows "88:88:88" or "88:88" behind the real digits to
  // simulate unlit LCD segments.
  let ghost: string = $derived(
    showSeconds ? "88:88:88" : "88:88"
  );

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="retro-lcd" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl}>
    <div class="lcd-screen">
      <div class="lcd-display">
        <span class="ghost" aria-hidden="true">{ghost}</span>
        <span class="digits">{display}</span>
      </div>
      {#if time.period}
        <span class="lcd-period">{time.period}</span>
      {/if}
    </div>
  </div>
</div>

<style>
  .retro-lcd {
    flex: 1;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .scale-content {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0.75rem;
    box-sizing: border-box;
  }

  .lcd-screen {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 0.3em;
    background: #0a0e0a;
    border-radius: 12px;
    padding: 1.5rem 2rem;
    box-shadow:
      inset 0 2px 8px rgba(0, 0, 0, 0.6),
      0 1px 0 rgba(255, 255, 255, 0.05);
    border: 1px solid #1a1f1a;
    max-width: 100%;
    max-height: 100%;
  }

  .lcd-display {
    position: relative;
    display: inline-flex;
    align-items: center;
    line-height: 1;
  }

  .ghost {
    font-family: "Courier New", "Lucida Console", monospace;
    font-size: 8rem;
    font-weight: 700;
    color: rgba(50, 200, 80, 0.08);
    letter-spacing: 0.05em;
    user-select: none;
    line-height: 1;
  }

  .digits {
    position: absolute;
    top: 0;
    left: 0;
    font-family: "Courier New", "Lucida Console", monospace;
    font-size: 8rem;
    font-weight: 700;
    color: #32c850;
    letter-spacing: 0.05em;
    text-shadow:
      0 0 8px rgba(50, 200, 80, 0.6),
      0 0 20px rgba(50, 200, 80, 0.3),
      0 0 40px rgba(50, 200, 80, 0.1);
    line-height: 1;
  }

  .lcd-period {
    font-family: "Courier New", "Lucida Console", monospace;
    font-size: 2.5rem;
    font-weight: 700;
    color: #32c850;
    text-shadow:
      0 0 8px rgba(50, 200, 80, 0.6),
      0 0 20px rgba(50, 200, 80, 0.3);
    line-height: 1;
    align-self: flex-end;
    margin-bottom: 0.1em;
  }
</style>
