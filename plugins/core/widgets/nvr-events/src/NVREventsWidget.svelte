<svelte:options customElement="nvr-events-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { eventsPath, mediaPath, parseConfig, title, when, type NVREvent } from "./config";
  import { errorMessage, isAbort, relayFetch, responseError } from "../../shared/relay";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let events = $state<NVREvent[]>([]);
  let loaded = $state(false);
  let error = $state("");
  let thumbs = $state<Record<string, string>>({});
  let playing = $state<NVREvent | null>(null);
  let clipURL = $state("");
  let clipError = $state("");

  let generation = 0;
  let unsubscribe: (() => void) | null = null;
  let refetchTimer: ReturnType<typeof setTimeout> | null = null;
  let clipController: AbortController | null = null;

  // A dashboard with no live channel still refreshes now and then.
  const POLL_MS = 60_000;
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  const bridge = () => (window as any).__karakuri;

  let bound = $derived(!!cfg.connectionId && !!cfg.camera);
  let bindKey = $derived(JSON.stringify([cfg.connectionId, cfg.camera, cfg.labels, cfg.zones, cfg.limit]));

  $effect(() => {
    void bindKey;
    untrack(() => void bind());
    return () => unbind();
  });

  onDestroy(() => {
    unbind();
    closeClip();
    for (const url of Object.values(thumbs)) URL.revokeObjectURL(url);
  });

  function unbind(): void {
    generation += 1;
    unsubscribe?.();
    unsubscribe = null;
    if (refetchTimer) clearTimeout(refetchTimer);
    if (pollTimer) clearInterval(pollTimer);
    refetchTimer = null;
    pollTimer = null;
  }

  async function bind(): Promise<void> {
    const gen = ++generation;
    events = [];
    loaded = false;
    error = "";
    if (!bound) return;

    // An event starting or ending on a shown camera is the signal to read
    // the list again. Several cameras can fire at once, so the read waits
    // for them to settle.
    const off = bridge()?.subscribeTopic(`nvr:${cfg.connectionId}:events:${cfg.camera}`, () => {
      if (gen !== generation) return;
      if (refetchTimer) clearTimeout(refetchTimer);
      refetchTimer = setTimeout(() => void load(gen), 1500);
    });
    if (gen !== generation) {
      off?.();
      return;
    }
    unsubscribe = off ?? null;
    pollTimer = setInterval(() => void load(gen), POLL_MS);
    await load(gen);
  }

  async function load(gen: number): Promise<void> {
    try {
      const res = await bridge().api.get(eventsPath(cfg));
      if (gen !== generation) return;
      const body = res?.events ? res : (res?.data ?? res);
      events = Array.isArray(body?.events) ? body.events : [];
      loaded = true;
      error = "";
      if (cfg.showThumbnails) void loadThumbs(gen);
    } catch (e: any) {
      if (gen !== generation) return;
      error = e?.message || "cannot read the events";
    }
  }

  // loadThumbs fetches thumbnails the list does not hold yet and releases the
  // ones for events that left it.
  async function loadThumbs(gen: number): Promise<void> {
    const keep = new Set(events.map((e) => e.id));
    const next: Record<string, string> = {};
    for (const [id, url] of Object.entries(thumbs)) {
      if (keep.has(id)) next[id] = url;
      else URL.revokeObjectURL(url);
    }
    thumbs = next;
    for (const e of events) {
      if (thumbs[e.id] || !e.has_snapshot) continue;
      try {
        const res = await relayFetch(mediaPath(cfg, e.id, "thumbnail"), { cache: "no-store" });
        if (!res.ok) continue;
        const blob = await res.blob();
        if (gen !== generation) return;
        thumbs = { ...thumbs, [e.id]: URL.createObjectURL(blob) };
      } catch {
        // A missing thumbnail leaves the row without a picture.
      }
    }
  }

  // open plays an event's clip. The clip is fetched through the host, which
  // adds the dashboard's credentials a <video> request cannot carry.
  async function open(e: NVREvent): Promise<void> {
    closeClip();
    playing = e;
    if (!e.has_clip) {
      clipError = "Frigate recorded no clip for this event.";
      return;
    }
    clipController = new AbortController();
    try {
      const res = await relayFetch(mediaPath(cfg, e.id, "clip"), { signal: clipController.signal });
      if (!res.ok) throw new Error(await responseError(res));
      const blob = await res.blob();
      if (playing?.id !== e.id) return;
      clipURL = URL.createObjectURL(blob);
    } catch (err) {
      if (isAbort(err)) return;
      clipError = `Cannot play the clip: ${errorMessage(err, "the request failed")}`;
    }
  }

  function closeClip(): void {
    clipController?.abort();
    clipController = null;
    if (clipURL) URL.revokeObjectURL(clipURL);
    clipURL = "";
    clipError = "";
    playing = null;
  }
</script>

<div class="events">
  {#if !bound}
    <span class="hint">Pick an NVR camera in the widget settings.</span>
  {:else if error && !loaded}
    <span class="error">Events unavailable: {error}</span>
  {:else if !loaded}
    <span class="hint">Loading events</span>
  {:else if events.length === 0}
    <span class="hint">No events yet</span>
  {:else}
    <ul>
      {#each events as e (e.id)}
        <li>
          <button type="button" onclick={() => void open(e)}>
            {#if cfg.showThumbnails}
              {#if thumbs[e.id]}
                <img src={thumbs[e.id]} alt="" />
              {:else}
                <span class="thumb"></span>
              {/if}
            {/if}
            <span class="text">
              <span class="title">{title(e)}</span>
              <span class="detail">
                {e.camera}{e.zones.length ? ", " + e.zones.join(", ") : ""} · {Math.round(e.score * 100)}%
              </span>
            </span>
            <span class="time">{when(e.start_time)}{e.end_time ? "" : " · now"}</span>
          </button>
        </li>
      {/each}
    </ul>
  {/if}

  {#if playing}
    <div class="player" role="dialog" aria-label="Event clip">
      <div class="bar">
        <span>{title(playing)} on {playing.camera}, {when(playing.start_time)}</span>
        <button type="button" class="close" onclick={closeClip} aria-label="Close">×</button>
      </div>
      {#if clipURL}
        <!-- svelte-ignore a11y_media_has_caption -->
        <video src={clipURL} controls autoplay playsinline></video>
      {:else if clipError}
        <span class="error">{clipError}</span>
      {:else}
        <span class="hint">Loading the clip</span>
      {/if}
    </div>
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

  .events {
    position: relative;
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    container-type: size;
  }

  .hint,
  .error {
    margin: auto;
    padding: 0 1em;
    text-align: center;
    font-size: clamp(12px, 5cqmin, 18px);
  }

  .hint {
    opacity: 0.6;
  }

  .error {
    color: var(--theme-error, #f38ba8);
  }

  ul {
    list-style: none;
    margin: 0;
    padding: 0.25em;
    overflow-y: auto;
    flex: 1;
  }

  li button {
    display: flex;
    align-items: center;
    gap: 0.6em;
    width: 100%;
    padding: 0.35em 0.4em;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: inherit;
    font: inherit;
    font-size: clamp(11px, 3.5cqmin, 16px);
    text-align: left;
    cursor: pointer;
  }

  li button:hover {
    background: color-mix(in srgb, var(--theme-surface0, #313244) 70%, transparent);
  }

  img,
  .thumb {
    flex: none;
    width: 4.5em;
    height: 3em;
    border-radius: 4px;
    object-fit: cover;
    background: var(--theme-surface0, #313244);
  }

  .text {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1;
  }

  .title {
    font-weight: 600;
  }

  .detail,
  .time {
    font-size: 0.85em;
    opacity: 0.7;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .time {
    flex: none;
  }

  .player {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    background: var(--theme-crust, #11111b);
    z-index: 1;
  }

  .bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5em;
    padding: 0.3em 0.5em;
    font-size: clamp(11px, 3.5cqmin, 15px);
  }

  .close {
    border: none;
    background: transparent;
    color: inherit;
    font-size: 1.4em;
    line-height: 1;
    cursor: pointer;
  }

  video {
    flex: 1;
    min-height: 0;
    width: 100%;
    background: #000;
  }
</style>
