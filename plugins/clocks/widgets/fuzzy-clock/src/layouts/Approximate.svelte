<script lang="ts">
  import type { ApproximateTimeResult } from "../fuzzyTime.ts";

  let {
    fuzzyData = {} as ApproximateTimeResult,
    typography = "serif",
    fuzziness = 3,
  }: { fuzzyData: ApproximateTimeResult; typography: string; fuzziness: number } = $props();

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

  let label = $derived(fuzzyData.label || "");
  let icon = $derived(fuzzyData.icon || "stars");

  let displayKey = $derived(`${label}-${icon}`);

  let containerEl: HTMLElement;
  let contentEl: HTMLElement;

  $effect(() => {
    if (!containerEl || !contentEl) return;
    const cleanup = (window as any).__karakuri?.autoScale(containerEl, contentEl);
    return () => cleanup?.();
  });
</script>

<div class="approximate" bind:this={containerEl}>
  <div class="scale-content" bind:this={contentEl} style:font-family={fontFamily} style:font-style={fontStyle}>
    {#key displayKey}
      <div class="content">
      <div class="icon-wrap">
        {#if icon === "sunrise"}
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <line x1="32" y1="8" x2="32" y2="16" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="12" y1="22" x2="17.6" y2="27.6" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="52" y1="22" x2="46.4" y2="27.6" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="4" y1="40" x2="12" y2="40" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="52" y1="40" x2="60" y2="40" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <path d="M16 40a16 16 0 0 1 32 0" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <line x1="6" y1="48" x2="58" y2="48" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
          </svg>
        {:else if icon === "coffee"}
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path d="M20 16c0-4 4-4 4-8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <path d="M30 16c0-4 4-4 4-8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <path d="M40 16c0-4 4-4 4-8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <rect x="12" y="22" width="36" height="26" rx="3" stroke="currentColor" stroke-width="2.5" fill="none" />
            <path d="M48 28h4a6 6 0 0 1 0 12h-4" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <line x1="8" y1="54" x2="56" y2="54" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
          </svg>
        {:else if icon === "sun"}
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <circle cx="32" cy="32" r="12" stroke="currentColor" stroke-width="2.5" fill="none" />
            <line x1="32" y1="4" x2="32" y2="14" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="32" y1="50" x2="32" y2="60" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="4" y1="32" x2="14" y2="32" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="50" y1="32" x2="60" y2="32" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="12.2" y1="12.2" x2="19.3" y2="19.3" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="44.7" y1="44.7" x2="51.8" y2="51.8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="51.8" y1="12.2" x2="44.7" y2="19.3" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="19.3" y1="44.7" x2="12.2" y2="51.8" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
          </svg>
        {:else if icon === "sunset"}
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <line x1="32" y1="20" x2="32" y2="12" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="12" y1="26" x2="17.6" y2="31.6" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="52" y1="26" x2="46.4" y2="31.6" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="4" y1="44" x2="12" y2="44" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="52" y1="44" x2="60" y2="44" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <path d="M16 44a16 16 0 0 1 32 0" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" fill="none" />
            <line x1="6" y1="52" x2="58" y2="52" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <line x1="32" y1="44" x2="32" y2="36" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" />
            <polyline points="26,38 32,44 38,38" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" fill="none" />
          </svg>
        {:else if icon === "moon"}
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <path
              d="M40 10a22 22 0 1 0 14 44 22 22 0 0 1-14-44z"
              stroke="currentColor"
              stroke-width="2.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              fill="none"
            />
          </svg>
        {:else}
          <!-- stars -->
          <svg viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
            <polygon points="32,4 35,20 48,10 38,22 54,22 40,28 50,42 34,32 32,50 30,32 14,42 24,28 10,22 26,22 16,10 29,20" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" fill="none" />
            <circle cx="14" cy="12" r="1.5" fill="currentColor" />
            <circle cx="52" cy="16" r="1" fill="currentColor" />
            <circle cx="8" cy="38" r="1.2" fill="currentColor" />
            <circle cx="56" cy="42" r="1" fill="currentColor" />
            <circle cx="48" cy="52" r="1.5" fill="currentColor" />
            <circle cx="16" cy="54" r="1" fill="currentColor" />
          </svg>
        {/if}
      </div>
      <span class="label">{label}</span>
      </div>
    {/key}
  </div>
</div>

<style>
  .approximate {
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
    gap: 1em;
    animation: fadeIn 0.5s ease-out;
  }

  .icon-wrap {
    width: 8rem;
    height: 8rem;
    color: var(--theme-accent, #89b4fa);
    opacity: 0.85;
  }

  .icon-wrap svg {
    width: 100%;
    height: 100%;
  }

  .label {
    font-size: 4rem;
    font-weight: 300;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--theme-text, #cdd6f4);
    text-align: center;
    line-height: 1.2;
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
      transform: scale(0.96);
    }
    to {
      opacity: 1;
      transform: scale(1);
    }
  }
</style>
