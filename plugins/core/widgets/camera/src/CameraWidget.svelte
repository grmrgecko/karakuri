<svelte:options customElement="camera-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { cameraPath, isBound, parseConfig, streamHeight } from "./config";
  import { boundaryOf, readMJPEG } from "./mjpeg";
  import { errorMessage, isAbort, relayFetch, responseError } from "../../shared/relay";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let src = $state("");
  let error = $state("");
  let name = $state("");

  // shownURL is the object URL on screen, revoked when the next frame replaces
  // it so a long-running stream does not hold every frame it has shown.
  let shownURL = "";
  // generation tells callbacks from a stopped run apart from the live one; a
  // frame or timer that outlived its run is dropped rather than drawn.
  let generation = 0;
  let controller: AbortController | null = null;
  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let pollToken = 0;

  const STREAM_BACKOFF_MIN = 5_000;
  const STREAM_BACKOFF_MAX = 60_000;
  let backoff = STREAM_BACKOFF_MIN;

  const bridge = () => (window as any).__karakuri;

  let bound = $derived(isBound(cfg));
  let cellHeight = $state(0);

  // The NVR is asked for a stream sized to the cell, in steps, so it encodes
  // no more pixels than the panel shows. Resizing within a step keeps the
  // stream running.
  let height = $derived(cfg.source === "nvr" ? streamHeight(cellHeight, window.devicePixelRatio) : 0);

  let runKey = $derived(
    JSON.stringify([cfg.source, cfg.connectionId, cfg.entityId, cfg.camera, cfg.bbox, cfg.mode,
                    cfg.interval, cfg.showName, height, cellHeight > 0]),
  );

  $effect(() => {
    void runKey;
    untrack(() => start());
    return () => stop();
  });

  onDestroy(() => {
    stop();
    if (shownURL) URL.revokeObjectURL(shownURL);
    shownURL = "";
  });

  function start(): void {
    const gen = ++generation;
    error = "";
    name = "";
    backoff = STREAM_BACKOFF_MIN;
    if (!bound) return;
    // An NVR stream waits for the cell's size, which picks its height.
    if (cfg.source === "nvr" && cellHeight === 0) return;
    controller = new AbortController();
    if (cfg.showName && cfg.source === "ha") void loadName(gen);
    if (cfg.mode === "snapshot") startPolling(gen);
    else void stream(gen);
  }

  function stop(): void {
    generation += 1;
    pollToken += 1;
    controller?.abort();
    controller = null;
    if (pollTimer) clearTimeout(pollTimer);
    if (retryTimer) clearTimeout(retryTimer);
    pollTimer = null;
    retryTimer = null;
  }

  function show(frame: Blob): void {
    const url = URL.createObjectURL(frame);
    const previous = shownURL;
    shownURL = url;
    src = url;
    if (previous) URL.revokeObjectURL(previous);
  }

  async function loadName(gen: number): Promise<void> {
    try {
      const res = await bridge().api.get(`/connections/${encodeURIComponent(cfg.connectionId)}/ha/states`);
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      const entity = list.find((s) => s.entity_id === cfg.entityId);
      if (gen === generation) name = String(entity?.attributes?.friendly_name ?? "");
    } catch {
      // The overlay falls back to the entity ID; the image matters more.
    }
  }

  async function snapshot(gen: number): Promise<void> {
    try {
      const res = await relayFetch(cameraPath(cfg, "snapshot", height), {
        signal: controller?.signal,
        cache: "no-store",
      });
      if (!res.ok) throw new Error(await responseError(res));
      const frame = await res.blob();
      if (gen !== generation) return;
      show(frame);
      error = "";
    } catch (e) {
      if (gen !== generation || isAbort(e)) return;
      error = `Camera unavailable: ${errorMessage(e, "the snapshot failed")}`;
    }
  }

  // startPolling refreshes the still on the configured interval. Each request
  // finishes before the next is scheduled, so a slow camera is never asked
  // twice at once.
  function startPolling(gen: number): void {
    const token = ++pollToken;
    const tick = async (): Promise<void> => {
      if (gen !== generation || token !== pollToken) return;
      await snapshot(gen);
      if (gen !== generation || token !== pollToken) return;
      pollTimer = setTimeout(tick, cfg.interval * 1000);
    };
    void tick();
  }

  // stream relays the MJPEG stream until it ends or fails, then shows
  // snapshots while it waits to try again. The wait doubles on each failure
  // and resets once a frame arrives.
  async function stream(gen: number): Promise<void> {
    try {
      const res = await relayFetch(cameraPath(cfg, "stream", height), { signal: controller?.signal });
      if (!res.ok) throw new Error(await responseError(res));
      const boundary = boundaryOf(res.headers.get("Content-Type"));
      if (!boundary || !res.body) throw new Error("the camera did not send a stream");
      const reader = res.body.getReader();
      await readMJPEG(reader, boundary, (frame) => {
        if (gen !== generation) {
          void reader.cancel().catch(() => {});
          return;
        }
        show(frame);
        error = "";
        backoff = STREAM_BACKOFF_MIN;
      });
      if (gen !== generation) return;
    } catch (e) {
      if (gen !== generation || isAbort(e)) return;
      error = `Camera stream lost: ${errorMessage(e, "the stream failed")}. Retrying.`;
    }
    if (gen !== generation) return;

    startPolling(gen);
    const wait = backoff;
    backoff = Math.min(backoff * 2, STREAM_BACKOFF_MAX);
    retryTimer = setTimeout(() => {
      if (gen !== generation) return;
      pollToken += 1;
      if (pollTimer) clearTimeout(pollTimer);
      void stream(gen);
    }, wait);
  }

  let label = $derived(cfg.source === "nvr" ? cfg.camera : name || cfg.entityId);
</script>

<div class="camera" bind:clientHeight={cellHeight}>
  {#if !bound}
    <span class="hint">Pick a camera in the widget settings.</span>
  {:else}
    {#if src}
      <img src={src} alt={label} style:object-fit={cfg.fit} />
    {/if}
    {#if error}
      <div class="error" class:overlay={!!src}>{error}</div>
    {:else if !src}
      <span class="hint">Connecting to the camera</span>
    {/if}
    {#if cfg.showName && label}
      <div class="name">{label}</div>
    {/if}
  {/if}
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  .camera {
    position: relative;
    width: 100%;
    height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    overflow: hidden;
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
    container-type: size;
  }

  img {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    display: block;
  }

  .hint {
    font-size: clamp(12px, 5cqmin, 20px);
    opacity: 0.6;
    text-align: center;
    padding: 0 1em;
  }

  .error {
    position: relative;
    max-width: 90%;
    font-size: clamp(12px, 5cqmin, 20px);
    color: var(--theme-error, #f38ba8);
    text-align: center;
  }

  /* With a last frame still on screen, the error is a banner over it rather
     than a blank cell: an old picture with a warning beats no picture. */
  .error.overlay {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    max-width: none;
    padding: 0.4em 0.6em;
    background: color-mix(in srgb, var(--theme-crust, #11111b) 80%, transparent);
  }

  .name {
    position: absolute;
    top: 0;
    left: 0;
    max-width: 100%;
    box-sizing: border-box;
    padding: 0.35em 0.7em;
    font-size: clamp(12px, 6cqmin, 28px);
    font-weight: 600;
    color: #fff;
    text-shadow: 0 1px 3px rgba(0, 0, 0, 0.8);
    background: linear-gradient(to bottom, rgba(0, 0, 0, 0.55), transparent);
    border-bottom-right-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
