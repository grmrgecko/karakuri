<script lang="ts">
  let {
    source = "",
    aspectRatio = "",
    fit = "contain",
    autoplay = false,
    loop = false,
    mute = false,
  }: {
    source?: string;
    aspectRatio?: string;
    fit?: string;
    autoplay?: boolean;
    loop?: boolean;
    mute?: boolean;
  } = $props();

  let videoEl = $state<HTMLVideoElement | null>(null);
  let playing = $state<boolean>(false);
  let currentTime = $state<number>(0);
  let duration = $state<number>(0);
  let volume = $state<number>(0);
  let showControls = $state<boolean>(true);
  let hideTimer: ReturnType<typeof setTimeout> | null = null;

  function togglePlay(): void {
    if (!videoEl) return;
    if (videoEl.paused) {
      videoEl.play();
    } else {
      videoEl.pause();
    }
  }

  function onTimeUpdate(): void {
    if (!videoEl) return;
    currentTime = videoEl.currentTime;
  }

  function onLoadedMetadata(): void {
    if (!videoEl) return;
    duration = videoEl.duration;
    volume = videoEl.muted ? 0 : videoEl.volume;
  }

  function onPlay(): void { playing = true; }
  function onPause(): void { playing = false; }

  function seek(e: MouseEvent): void {
    if (!videoEl || !duration) return;
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const pct = (e.clientX - rect.left) / rect.width;
    videoEl.currentTime = pct * duration;
  }

  function seekKeydown(e: KeyboardEvent): void {
    if (!videoEl || !duration) return;
    if (e.key === "ArrowRight") {
      videoEl.currentTime = Math.min(duration, videoEl.currentTime + 5);
    } else if (e.key === "ArrowLeft") {
      videoEl.currentTime = Math.max(0, videoEl.currentTime - 5);
    }
  }

  function setVolume(e: Event): void {
    if (!videoEl) return;
    volume = parseFloat((e.target as HTMLInputElement).value);
    videoEl.volume = volume;
    videoEl.muted = volume === 0;
  }

  function toggleMute(): void {
    if (!videoEl) return;
    if (videoEl.muted || volume === 0) {
      videoEl.muted = false;
      volume = 0.5;
      videoEl.volume = 0.5;
    } else {
      videoEl.muted = true;
      volume = 0;
    }
  }

  function onMouseMove(): void {
    showControls = true;
    if (hideTimer) clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (playing) showControls = false;
    }, 3000);
  }

  function formatTime(sec: number): string {
    if (!sec || !isFinite(sec)) return "0:00";
    const m = Math.floor(sec / 60);
    const s = Math.floor(sec % 60);
    return `${m}:${s.toString().padStart(2, "0")}`;
  }

  let progressPct: number = $derived(duration > 0 ? (currentTime / duration) * 100 : 0);

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

  // Compute explicit player dimensions to fit the aspect ratio within the wrapper.
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

<div class="player-wrapper" bind:this={wrapperEl}>
  <div
    class="player-view"
    style:width={viewW}
    style:height={viewH}
    onmousemove={onMouseMove}
    onmouseleave={() => { if (playing) showControls = false; }}
    role="group"
    aria-label="Video player"
  >
  <video
    bind:this={videoEl}
    class="video"
    src={source}
    style:object-fit={fit}
    autoplay={autoplay}
    loop={loop}
    muted={mute}
    playsinline
    ontimeupdate={onTimeUpdate}
    onloadedmetadata={onLoadedMetadata}
    onplay={onPlay}
    onpause={onPause}
    onclick={togglePlay}
    preload="metadata"
  >
    <track kind="captions" />
  </video>

  <!-- Play overlay when paused -->
  {#if !playing && duration > 0}
    <button class="play-overlay" onclick={togglePlay} aria-label="Play">
      <svg viewBox="0 0 24 24" fill="currentColor">
        <polygon points="5,3 19,12 5,21" />
      </svg>
    </button>
  {/if}

  <!-- Controls bar -->
  <div class="controls" class:visible={showControls}>
    <button class="ctrl-btn" onclick={togglePlay} aria-label={playing ? "Pause" : "Play"}>
      {#if playing}
        <svg viewBox="0 0 24 24" fill="currentColor">
          <rect x="6" y="4" width="4" height="16" rx="1" />
          <rect x="14" y="4" width="4" height="16" rx="1" />
        </svg>
      {:else}
        <svg viewBox="0 0 24 24" fill="currentColor">
          <polygon points="5,3 19,12 5,21" />
        </svg>
      {/if}
    </button>

    <span class="time">{formatTime(currentTime)}</span>

    <div class="progress-bar" onclick={seek} onkeydown={seekKeydown} role="slider" aria-label="Seek" aria-valuenow={currentTime} aria-valuemin={0} aria-valuemax={duration} tabindex="0">
      <div class="progress-track">
        <div class="progress-fill" style="width: {progressPct}%"></div>
      </div>
    </div>

    <span class="time">{formatTime(duration)}</span>

    <button class="ctrl-btn" onclick={toggleMute} aria-label={volume === 0 ? "Unmute" : "Mute"}>
      {#if volume === 0}
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" />
          <line x1="23" y1="9" x2="17" y2="15" />
          <line x1="17" y1="9" x2="23" y2="15" />
        </svg>
      {:else}
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5" fill="currentColor" />
          <path d="M15.54 8.46a5 5 0 0 1 0 7.07" />
          <path d="M19.07 4.93a10 10 0 0 1 0 14.14" />
        </svg>
      {/if}
    </button>

    <input
      class="volume-slider"
      type="range"
      min="0"
      max="1"
      step="0.05"
      value={volume}
      oninput={setVolume}
      aria-label="Volume"
    />
  </div>
  </div>
</div>

<style>
  .player-wrapper {
    width: 100%;
    height: 100%;
    overflow: hidden;
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    background: #000;
  }

  .player-view {
    position: relative;
    width: 100%;
    height: 100%;
    background: #000;
    overflow: hidden;
  }

  .video {
    width: 100%;
    height: 100%;
    display: block;
    cursor: pointer;
  }

  .play-overlay {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: 60px;
    height: 60px;
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.6);
    border: 2px solid rgba(255, 255, 255, 0.8);
    color: white;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    transition: background 0.2s, transform 0.2s;
  }

  .play-overlay:hover {
    background: rgba(0, 0, 0, 0.8);
    transform: translate(-50%, -50%) scale(1.05);
  }

  .play-overlay svg {
    width: 50%;
    height: 50%;
    margin-left: 4%;
  }

  .controls {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    display: flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.3rem 0.5rem;
    background: linear-gradient(transparent, rgba(0, 0, 0, 0.8));
    opacity: 0;
    transition: opacity 0.3s;
  }

  .controls.visible {
    opacity: 1;
  }

  .ctrl-btn {
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    background: none;
    color: white;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  .ctrl-btn svg {
    width: 100%;
    height: 100%;
  }

  .ctrl-btn:hover {
    opacity: 0.8;
  }

  .time {
    font-size: 0.78rem;
    color: rgba(255, 255, 255, 0.8);
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  .progress-bar {
    flex: 1;
    height: 16px;
    display: flex;
    align-items: center;
    cursor: pointer;
    min-width: 0;
  }

  .progress-track {
    width: 100%;
    height: 3px;
    background: rgba(255, 255, 255, 0.2);
    border-radius: 2px;
    overflow: hidden;
    position: relative;
  }

  .progress-fill {
    position: absolute;
    top: 0;
    left: 0;
    height: 100%;
    background: var(--theme-accent, #89b4fa);
    border-radius: 2px;
    transition: width 0.1s linear;
  }

  .volume-slider {
    width: 60px;
    height: 3px;
    accent-color: var(--theme-accent, #89b4fa);
    cursor: pointer;
    flex-shrink: 0;
  }

</style>
