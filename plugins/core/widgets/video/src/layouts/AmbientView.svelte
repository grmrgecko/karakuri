<script lang="ts">
  let {
    source = "",
    aspectRatio = "",
    fit = "cover",
    mute = true,
  }: {
    source?: string;
    aspectRatio?: string;
    fit?: string;
    mute?: boolean;
  } = $props();

  let wrapperEl = $state<HTMLElement | null>(null);
  let viewW = $state<string>("100%");
  let viewH = $state<string>("100%");

  // Parse ratio string like "16/9" into a numeric value.
  function parseRatio(r: string): number {
    if (!r) return 0;
    const parts = r.split("/");
    const val = parseFloat(parts[0]) / parseFloat(parts[1] || "1");
    return isFinite(val) ? val : 0;
  }

  // Compute explicit view dimensions to fit the aspect ratio within the wrapper.
  $effect(() => {
    const ratio = parseRatio(aspectRatio);
    if (!wrapperEl || !ratio) {
      viewW = "100%";
      viewH = "100%";
      return;
    }

    const observer = new ResizeObserver((entries) => {
      const { width: cw, height: ch } = entries[0].contentRect;
      if (cw <= 0 || ch <= 0) return;
      if (cw / ch > ratio) {
        viewH = "100%";
        viewW = `${ch * ratio}px`;
      } else {
        viewW = "100%";
        viewH = `${cw / ratio}px`;
      }
    });
    observer.observe(wrapperEl);

    return () => observer.disconnect();
  });
</script>

<div class="ambient-wrapper" bind:this={wrapperEl}>
  <div
    class="ambient-view"
    style:width={viewW}
    style:height={viewH}
  >
    <video
      class="video"
      src={source}
      style:object-fit={fit}
      autoplay
      loop
      muted={mute}
      playsinline
      preload="auto"
      aria-hidden="true"
    >
      <track kind="captions" />
    </video>
  </div>
</div>

<style>
  .ambient-wrapper {
    width: 100%;
    height: 100%;
    overflow: hidden;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #000;
  }

  .ambient-view {
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .video {
    width: 100%;
    height: 100%;
    display: block;
  }
</style>
