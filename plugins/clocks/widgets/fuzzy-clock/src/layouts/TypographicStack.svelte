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

  // Split the words array into individual lines. Each word from the fuzzy
  // time output becomes its own row in the stack.
  let lines = $derived(fuzzyData.words || []);
  let hourUpper = $derived((fuzzyData.hourWord || "").toUpperCase());

  let displayKey = $derived(lines.join("-"));

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="typographic-stack" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl} style:font-family={fontFamily} style:font-style={fontStyle}>
    {#key displayKey}
      <div class="stack">
        {#each lines as word}
          <span
            class="word"
            class:accent={word === hourUpper}
          >
            {word}
          </span>
        {/each}
      </div>
    {/key}
  </div>
</div>

<style>
  .typographic-stack {
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

  .stack {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    justify-content: center;
    gap: 0;
    animation: fadeIn 0.4s ease-out;
  }

  .word {
    font-size: 6rem;
    font-weight: 800;
    line-height: 1.05;
    letter-spacing: -0.02em;
    text-transform: uppercase;
    color: var(--theme-text, #cdd6f4);
  }

  .word.accent {
    color: var(--theme-accent, #89b4fa);
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>
