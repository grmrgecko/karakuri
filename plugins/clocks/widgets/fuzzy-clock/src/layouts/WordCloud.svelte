<script lang="ts">
  import { getGridCells } from "../fuzzyTime.ts";
  import type { GridCell } from "../fuzzyTime.ts";

  let {
    fuzzyData = new Set<string>(),
    typography = "serif",
    fuzziness = 3,
  }: { fuzzyData: Set<string>; typography: string; fuzziness: number } = $props();

  // The grid cells are static and never change, so we compute them once.
  const cells: GridCell[] = getGridCells();

  let fontFamily = $derived(
    typography === "sans"
      ? 'var(--theme-font-family, "Inter", sans-serif)'
      : typography === "mono"
        ? '"JetBrains Mono", "Fira Code", monospace'
        : typography === "handwritten"
          ? '"Comic Sans MS", "Segoe Script", cursive'
          : '"Georgia", "Times New Roman", serif'
  );

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="word-cloud" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl} style:font-family={fontFamily}>
    <div class="grid">
    {#each cells as cell}
      <span
        class="cell"
        class:active={cell.wordId && fuzzyData.has(cell.wordId)}
      >
        {cell.letter}
      </span>
    {/each}
    </div>
  </div>
</div>

<style>
  .word-cloud {
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

  .grid {
    display: grid;
    grid-template-columns: repeat(11, 1fr);
    grid-template-rows: repeat(10, 1fr);
    gap: 4px;
    aspect-ratio: 11 / 10;
  }

  .cell {
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 3rem;
    font-weight: 700;
    line-height: 1;
    letter-spacing: 0.05em;
    color: var(--theme-text, #cdd6f4);
    opacity: 0.08;
    transition: opacity 0.4s ease, color 0.4s ease, text-shadow 0.4s ease;
    user-select: none;
  }

  .cell.active {
    opacity: 1;
    color: var(--theme-accent, #89b4fa);
    text-shadow:
      0 0 8px color-mix(in srgb, var(--theme-accent, #89b4fa) 50%, transparent),
      0 0 20px color-mix(in srgb, var(--theme-accent, #89b4fa) 25%, transparent);
  }
</style>
