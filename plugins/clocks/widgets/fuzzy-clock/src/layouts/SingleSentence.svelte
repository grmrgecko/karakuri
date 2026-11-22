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

  // Track the previous sentence so we can apply a fade transition when it
  // changes. The key triggers Svelte's keyed each/if block re-render.
  let displayKey = $derived(fuzzyData.sentence || "");

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="single-sentence" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl} style:font-family={fontFamily} style:font-style={fontStyle}>
    {#key displayKey}
      <p class="sentence">
        <span class="prefix">it is </span>{fuzzyData.sentence || ""}
      </p>
    {/key}
  </div>
</div>

<style>
  .single-sentence {
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
    text-align: center;
  }

  .sentence {
    font-size: 6rem;
    font-weight: 300;
    line-height: 1.3;
    color: var(--theme-text, #cdd6f4);
    margin: 0;
    animation: fadeIn 0.4s ease-out;
  }

  .prefix {
    color: var(--theme-subtext, #a6adc8);
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
