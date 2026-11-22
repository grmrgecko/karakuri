<script lang="ts">
  let {
    source = "",
    fit = "cover",
    aspectRatio = "",
    refreshInterval = 0,
  }: {
    source?: string;
    fit?: string;
    aspectRatio?: string;
    refreshInterval?: number;
  } = $props();

  let cacheBuster = $state<number>(0);

  // The displayed source appends a cache-buster when refresh is active.
  let imgSrc = $derived((() => {
    if (!source) return "";
    if (cacheBuster === 0) return source;
    const sep = source.includes("?") ? "&" : "?";
    return `${source}${sep}_t=${cacheBuster}`;
  })());

  $effect(() => {
    // Reset cache buster when source changes.
    cacheBuster = 0;

    let timer: ReturnType<typeof setInterval> | null = null;
    if (refreshInterval > 0 && source) {
      timer = setInterval(() => {
        cacheBuster = Date.now();
      }, refreshInterval * 1000);
    }

    return () => {
      if (timer) clearInterval(timer);
    };
  });
</script>

<div
  class="image-wrapper"
  style:aspect-ratio={aspectRatio || undefined}
>
  <img
    class="image"
    src={imgSrc}
    alt=""
    style:object-fit={fit}
  />
</div>

<style>
  .image-wrapper {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    overflow: hidden;
  }

  .image {
    width: 100%;
    height: 100%;
    display: block;
  }
</style>
