<script lang="ts">
  import type { FuzzyTimeResult } from "../fuzzyTime.ts";

  let {
    fuzzyData = {} as FuzzyTimeResult,
    typography = "serif",
    fuzziness = 3,
  }: { fuzzyData: FuzzyTimeResult; typography: string; fuzziness: number } = $props();

  let fontFamily = $derived(
    typography === "sans"
      ? 'var(--theme-font-family, "Inter", sans-serif)'
      : typography === "mono"
        ? '"JetBrains Mono", "Fira Code", monospace'
        : typography === "handwritten"
          ? '"Comic Sans MS", "Segoe Script", cursive'
          : '"Georgia", "Times New Roman", serif'
  );

  let fontStyle = $derived(typography === "handwritten" ? "italic" : "normal");

  let hourDisplay = $derived((fuzzyData.hourWord || "").toUpperCase());
  let phraseDisplay = $derived(fuzzyData.minutePhrase || "");

  let displayKey = $derived(`${hourDisplay}-${phraseDisplay}`);

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="bold-hour" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl} style:font-family={fontFamily} style:font-style={fontStyle}>
    {#key displayKey}
      <div class="content">
        <span class="hour">{hourDisplay}</span>
        {#if phraseDisplay && phraseDisplay !== "o'clock"}
          <span class="phrase">{phraseDisplay}</span>
        {/if}
      </div>
    {/key}
  </div>
</div>

<style>
  .bold-hour {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
  }

  .scale-content {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0.5em;
    box-sizing: border-box;
  }

  .content {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.1em;
    animation: fadeIn 0.4s ease-out;
  }

  .hour {
    font-size: 10rem;
    font-weight: 900;
    line-height: 1;
    letter-spacing: -0.03em;
    color: var(--theme-accent, #89b4fa);
  }

  .phrase {
    font-size: 3rem;
    font-weight: 300;
    line-height: 1.2;
    color: var(--theme-subtext, #a6adc8);
    text-align: center;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(4px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>
