<script lang="ts">
  let {
    sources = [],
    fit = "cover",
    aspectRatio = "",
    refreshInterval = 0,
    slideInterval = 5,
    transition = "fade",
  }: {
    sources?: string[];
    fit?: string;
    aspectRatio?: string;
    refreshInterval?: number;
    slideInterval?: number;
    transition?: string;
  } = $props();

  let currentIndex = $state<number>(0);
  let nextIndex = $state<number>(-1);
  let transitioning = $state<boolean>(false);

  // Ken Burns uses a randomized scale/translate per image.
  let kbStyleFront = $state<string>("");
  let kbStyleBack = $state<string>("");

  // Generate a random Ken Burns transform for slow zoom/pan.
  function randomKenBurns(): string {
    const scale = 1.1 + Math.random() * 0.2;
    const tx = (Math.random() - 0.5) * 6;
    const ty = (Math.random() - 0.5) * 6;
    return `scale(${scale}) translate(${tx}%, ${ty}%)`;
  }

  // Advance to next image with the configured transition.
  function nextSlide(): void {
    if (sources.length <= 1 || transitioning) return;

    const next = (currentIndex + 1) % sources.length;
    nextIndex = next;

    if (transition === "kenburns") {
      kbStyleBack = randomKenBurns();
    }

    transitioning = true;

    const duration = transition === "none" ? 0 : transition === "kenburns" ? 1000 : 400;
    setTimeout(() => {
      currentIndex = next;
      nextIndex = -1;
      transitioning = false;
      if (transition === "kenburns") {
        kbStyleFront = kbStyleBack;
      }
    }, duration);
  }

  // Initialize Ken Burns on mount.
  $effect(() => {
    if (transition === "kenburns") {
      kbStyleFront = randomKenBurns();
    }
  });

  $effect(() => {
    currentIndex = 0;
    nextIndex = -1;
    transitioning = false;

    let timer: ReturnType<typeof setInterval> | null = null;
    const interval = (slideInterval || 5) * 1000;
    if (sources.length > 1) {
      timer = setInterval(nextSlide, interval);
    }

    return () => {
      if (timer) clearInterval(timer);
    };
  });

  let currentSrc = $derived(sources[currentIndex] || "");
  let nextSrc = $derived(nextIndex >= 0 ? (sources[nextIndex] || "") : "");

  // Append cache-buster when refreshInterval is set.
  let cacheBuster = $state<number>(0);

  function bustCache(src: string): string {
    if (!src || cacheBuster === 0) return src;
    const sep = src.includes("?") ? "&" : "?";
    return `${src}${sep}_t=${cacheBuster}`;
  }

  let displaySrc = $derived(bustCache(currentSrc));
  let displayNextSrc = $derived(bustCache(nextSrc));

  $effect(() => {
    cacheBuster = 0;

    let timer: ReturnType<typeof setInterval> | null = null;
    if (refreshInterval > 0 && currentSrc) {
      timer = setInterval(() => {
        cacheBuster = Date.now();
      }, refreshInterval * 1000);
    }

    return () => {
      if (timer) clearInterval(timer);
    };
  });

  // Determine CSS class for the wrapper based on flip transition.
  let wrapperTransitionClass = $derived(
    transitioning && transition === "flip" ? "flip-active" : ""
  );

  // CSS classes for front (current) image during transition.
  let frontClass = $derived((() => {
    if (!transitioning) return "";
    switch (transition) {
      case "fade": return "fade-exit";
      case "slide": return "slide-exit";
      case "slide-up": return "slide-up-exit";
      case "zoom": return "zoom-exit";
      case "blur": return "blur-exit";
      case "flip": return "flip-front";
      case "crossfade": return "crossfade-exit";
      case "kenburns": return "kb-exit";
      default: return "";
    }
  })());

  // CSS classes for back (next) image during transition.
  let backClass = $derived((() => {
    if (!transitioning) return "";
    switch (transition) {
      case "fade": return "fade-enter";
      case "slide": return "slide-enter";
      case "slide-up": return "slide-up-enter";
      case "zoom": return "zoom-enter";
      case "blur": return "blur-enter";
      case "flip": return "flip-back";
      case "crossfade": return "crossfade-enter";
      case "kenburns": return "kb-enter";
      default: return "";
    }
  })());
</script>

<div
  class="slideshow-wrapper {wrapperTransitionClass}"
  style:aspect-ratio={aspectRatio || undefined}
>
  <!-- Back image (next slide, rendered behind front). -->
  {#if transitioning && nextIndex >= 0 && transition !== "none"}
    <img
      class="image back {backClass}"
      src={displayNextSrc}
      alt=""
      style:object-fit={fit}
      style:transform={transition === "kenburns" ? kbStyleBack : undefined}
    />
  {/if}

  <!-- Front image (current slide). -->
  <img
    class="image front {frontClass}"
    src={displaySrc}
    alt=""
    style:object-fit={fit}
    style:transform={transition === "kenburns" ? kbStyleFront : undefined}
  />

  {#if sources.length > 1}
    <div class="indicators">
      {#each sources as _, i}
        <div class="dot" class:active={i === currentIndex}></div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .slideshow-wrapper {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    overflow: hidden;
    position: relative;
    perspective: 1000px;
  }

  .image {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    display: block;
  }

  .image.front {
    z-index: 2;
  }

  .image.back {
    z-index: 1;
  }

  /* --- Fade --- */
  .image.fade-exit {
    animation: fadeOut 0.4s ease forwards;
  }
  .image.fade-enter {
    opacity: 1;
  }

  @keyframes fadeOut {
    from { opacity: 1; }
    to { opacity: 0; }
  }

  /* --- Slide (horizontal) --- */
  .image.slide-exit {
    animation: slideOutLeft 0.4s ease forwards;
  }
  .image.slide-enter {
    animation: slideInRight 0.4s ease forwards;
  }

  @keyframes slideOutLeft {
    from { transform: translateX(0); opacity: 1; }
    to { transform: translateX(-100%); opacity: 0; }
  }
  @keyframes slideInRight {
    from { transform: translateX(100%); opacity: 0; }
    to { transform: translateX(0); opacity: 1; }
  }

  /* --- Slide Up (vertical) --- */
  .image.slide-up-exit {
    animation: slideOutUp 0.4s ease forwards;
  }
  .image.slide-up-enter {
    animation: slideInUp 0.4s ease forwards;
  }

  @keyframes slideOutUp {
    from { transform: translateY(0); opacity: 1; }
    to { transform: translateY(-100%); opacity: 0; }
  }
  @keyframes slideInUp {
    from { transform: translateY(100%); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }

  /* --- Zoom --- */
  .image.zoom-exit {
    animation: zoomOut 0.4s ease forwards;
  }
  .image.zoom-enter {
    opacity: 1;
  }

  @keyframes zoomOut {
    from { transform: scale(1); opacity: 1; }
    to { transform: scale(1.3); opacity: 0; }
  }

  /* --- Blur --- */
  .image.blur-exit {
    animation: blurOut 0.4s ease forwards;
  }
  .image.blur-enter {
    opacity: 1;
  }

  @keyframes blurOut {
    from { filter: blur(0); opacity: 1; }
    to { filter: blur(20px); opacity: 0; }
  }

  /* --- Flip (3D Y-axis) --- */
  .slideshow-wrapper.flip-active {
    transform-style: preserve-3d;
  }

  .image.flip-front {
    animation: flipFront 0.4s ease forwards;
    backface-visibility: hidden;
  }
  .image.flip-back {
    animation: flipBack 0.4s ease forwards;
    backface-visibility: hidden;
  }

  @keyframes flipFront {
    from { transform: rotateY(0deg); }
    to { transform: rotateY(180deg); }
  }
  @keyframes flipBack {
    from { transform: rotateY(-180deg); }
    to { transform: rotateY(0deg); }
  }

  /* --- Crossfade --- */
  .image.crossfade-exit {
    animation: fadeOut 0.4s ease forwards;
  }
  .image.crossfade-enter {
    animation: fadeIn 0.4s ease forwards;
  }

  @keyframes fadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }

  /* --- Ken Burns --- */
  .image.kb-exit {
    animation: fadeOut 1s ease forwards;
  }
  .image.kb-enter {
    animation: fadeIn 1s ease forwards;
  }

  /* --- Indicators --- */
  .indicators {
    position: absolute;
    bottom: 8px;
    left: 50%;
    transform: translateX(-50%);
    display: flex;
    gap: 5px;
    z-index: 10;
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.4);
    transition: background 0.2s;
  }

  .dot.active {
    background: rgba(255, 255, 255, 0.9);
  }
</style>
