<svelte:options customElement="media-player-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { clock, isBound, parseConfig, stateLabel } from "./config";
  import { errorMessage, isAbort, relayFetch } from "./relay";
  import {
    applyDeviceMessage,
    commandRequest,
    haService,
    hasFeature,
    hostOperates,
    loadControlKinds,
    resolveControl,
    resolveDevice,
    resolveHA,
    type EntitySource,
    type Resolved,
  } from "../../shared/entitySource";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  // resolved is the bound player as the media kind, whichever source it came
  // from. entity is kept for a Home Assistant player's app name, which the
  // kind does not carry.
  let resolved = $state<Resolved | null>(null);
  let entity = $state<any>(null);
  let device: any = null;
  let error = $state("");
  let actionError = $state("");
  let busy = $state(false);
  // denied is the server saying this dashboard may not operate the player.
  // Controls stay hidden after it, because every further press would be
  // refused the same way.
  let denied = $state(false);
  let now = $state(Date.now());
  let artSrc = $state("");
  // volumeDraft holds the slider while it is being dragged, so a state push
  // mid-drag does not yank it back.
  let volumeDraft = $state<number | null>(null);

  let artURL = "";
  let artFor = "";
  let artController: AbortController | null = null;
  let generation = 0;
  let unsubscribe: (() => void) | null = null;
  let ticker: ReturnType<typeof setInterval> | null = null;
  let actionTimer: ReturnType<typeof setTimeout> | null = null;

  const bridge = () => (window as any).__karakuri;

  let bound = $derived(isBound(cfg));
  let bindKey = $derived(
    JSON.stringify([cfg.source, cfg.connectionId, cfg.entityId, cfg.controlId, cfg.deviceId]),
  );
  let src = $derived<EntitySource>({
    source: cfg.source,
    connectionId: cfg.connectionId,
    entityId: cfg.entityId,
    controlId: cfg.controlId,
    deviceId: cfg.deviceId,
    displayOnly: cfg.displayOnly,
  });

  $effect(() => {
    void bindKey;
    untrack(() => void bind());
    return () => unbind();
  });

  onDestroy(() => {
    unbind();
    clearArt();
    if (ticker) clearInterval(ticker);
    if (actionTimer) clearTimeout(actionTimer);
  });

  function unbind(): void {
    generation += 1;
    unsubscribe?.();
    unsubscribe = null;
  }

  // show takes a fresh reading and releases the slider.
  function show(next: Resolved): void {
    resolved = next;
    error = "";
    volumeDraft = null;
  }

  async function bind(): Promise<void> {
    const gen = ++generation;
    resolved = null;
    entity = null;
    device = null;
    error = "";
    denied = false;
    if (!bound) return;
    // The catalog says which attributes a control or device kind has; Home
    // Assistant players are derived without it.
    await loadControlKinds();
    if (gen !== generation) return;

    const s = untrack(() => src);
    // Subscribe before reading so a change between the two is not lost.
    let off: (() => void) | undefined;
    switch (s.source) {
      case "control":
        off = bridge()?.subscribeTopic(`control:${s.controlId}`, (data: any) => {
          if (gen !== generation) return;
          if (data?.deleted) {
            error = "the control was deleted";
            resolved = null;
            return;
          }
          if (data?.control) show(resolveControl(data.control, s));
        });
        break;
      case "mqtt":
        off = bridge()?.subscribeTopic(`mqttdevice:${s.deviceId}`, (data: any) => {
          if (gen !== generation || !device) return;
          if (data?.error) return;
          applyDeviceMessage(device, data);
          show(resolveDevice(device, device.state, s));
        });
        break;
      default:
        off = bridge()?.subscribeTopic(`ha:${s.connectionId}:state:${s.entityId}`, (data: any) => {
          if (gen === generation && data?.new_state) {
            entity = data.new_state;
            show(resolveHA(data.new_state, s));
          }
        });
    }
    if (gen !== generation) {
      off?.();
      return;
    }
    unsubscribe = off ?? null;

    try {
      switch (s.source) {
        case "control": {
          const res = await bridge().api.get("/controls");
          const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
          if (gen !== generation) return;
          const found = list.find((c) => c.id === s.controlId);
          if (found) show(resolveControl(found, s));
          else error = "the control was not found";
          break;
        }
        case "mqtt": {
          const res = await bridge().api.get("/mqtt_devices");
          const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
          if (gen !== generation) return;
          device = list.find((d) => d.id === s.deviceId) ?? null;
          if (device) show(resolveDevice(device, device.state, s));
          else error = "the device was not found";
          break;
        }
        default: {
          const res = await bridge().api.get(`/connections/${encodeURIComponent(s.connectionId!)}/ha/states`);
          const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
          if (gen !== generation) return;
          const found = list.find((e) => e.entity_id === s.entityId);
          if (found) {
            entity = found;
            show(resolveHA(found, s));
          } else error = `${s.entityId} was not found`;
        }
      }
    } catch (e) {
      if (gen === generation) error = errorMessage(e, "cannot read the media player");
    }
  }

  let media = $derived(
    resolved?.value && typeof resolved.value === "object"
      ? (resolved.value as Record<string, any>)
      : ({} as Record<string, any>),
  );
  let options = $derived(resolved?.options ?? {});
  let isMedia = $derived(resolved?.kind === "media");
  let playerState = $derived(String(media.state ?? ""));
  let available = $derived(!!resolved && !resolved.unavailable);
  let playing = $derived(playerState === "playing");

  let title = $derived(String(media.title ?? ""));
  let subtitle = $derived(
    [media.artist, media.album]
      .filter((part) => part !== undefined && part !== null && String(part).trim() !== "")
      .map(String)
      .join(" · "),
  );
  let appName = $derived(String(entity?.attributes?.app_name ?? ""));
  let playerName = $derived(resolved?.name || cfg.entityId);

  // Progress is extrapolated from the last reported position: players report
  // it when something changes, not every second. One that reports no time is
  // measured from when its position or play state last changed, not from any
  // value, since a volume change leaves the position where it was.
  let duration = $derived(Number(media.duration));
  let reportedPosition = $derived(Number(media.position));
  let arrived = $derived.by(() => {
    void reportedPosition;
    void playerState;
    return Date.now();
  });
  let position = $derived.by(() => {
    const reported = reportedPosition;
    if (!Number.isFinite(reported)) return NaN;
    let pos = reported;
    if (playing) {
      const at = Date.parse(String(media.position_at ?? ""));
      pos += (now - (Number.isFinite(at) ? at : arrived)) / 1000;
    }
    return Number.isFinite(duration) && duration > 0 ? Math.min(Math.max(pos, 0), duration) : Math.max(pos, 0);
  });
  let hasProgress = $derived(Number.isFinite(duration) && duration > 0 && Number.isFinite(position));

  $effect(() => {
    if (!playing || !hasProgress) return;
    now = Date.now();
    ticker = setInterval(() => (now = Date.now()), 1000);
    return () => {
      if (ticker) clearInterval(ticker);
      ticker = null;
    };
  });

  // ---- Art ----
  //
  // An absolute URL is somewhere the browser can reach itself. A relative one
  // is Home Assistant's own proxy, which needs the connection's token, so the
  // server relays it and the token never reaches the page. Only a Home
  // Assistant player has such a path; any other player's art is its URL.

  let picture = $derived(cfg.showArt ? String(media.art ?? "") : "");

  $effect(() => {
    const pic = picture;
    untrack(() => loadArt(pic));
  });

  function clearArt(): void {
    artController?.abort();
    artController = null;
    if (artURL) URL.revokeObjectURL(artURL);
    artURL = "";
  }

  function loadArt(pic: string): void {
    if (pic === artFor) return;
    artFor = pic;
    if (!pic) {
      clearArt();
      artSrc = "";
      return;
    }
    if (/^https?:\/\//i.test(pic) || cfg.source !== "ha") {
      clearArt();
      artSrc = /^https?:\/\//i.test(pic) ? pic : "";
      return;
    }
    artController?.abort();
    const controller = new AbortController();
    artController = controller;
    const path = `/connections/${encodeURIComponent(cfg.connectionId)}/ha/media/${encodeURIComponent(cfg.entityId)}/art`;
    relayFetch(path, { signal: controller.signal, cache: "no-store" })
      .then(async (res) => {
        if (!res.ok) throw new Error(`art returned ${res.status}`);
        const blob = await res.blob();
        if (artFor !== pic) return;
        const url = URL.createObjectURL(blob);
        const previous = artURL;
        artURL = url;
        artSrc = url;
        if (previous) URL.revokeObjectURL(previous);
      })
      .catch((e) => {
        if (isAbort(e) || artFor !== pic) return;
        // Missing art is drawn as the placeholder rather than an error: plenty
        // of sources simply have none.
        if (artURL) URL.revokeObjectURL(artURL);
        artURL = "";
        artSrc = "";
      });
  }

  // ---- Controls ----
  //
  // What a player can do comes from the kind's options, which each source
  // fills from what the player reports supporting.

  let showControls = $derived(
    cfg.showControls && !cfg.displayOnly && !denied && hostOperates() && available && isMedia && !resolved?.readOnly,
  );
  let canPlayPause = $derived(playerState !== "off");
  let canSkip = $derived(hasFeature(options, "skip") && playerState !== "off");
  let canVolume = $derived(
    cfg.showVolume && hasFeature(options, "volume") && Number.isFinite(Number(media.volume)),
  );
  let volume = $derived(volumeDraft ?? Math.round(Number(media.volume ?? 0)));

  // write is the one path a press takes: a control set, a device command, or
  // the media_player service the kind write maps to.
  async function write(attribute: string, value: unknown): Promise<void> {
    if (busy || !resolved) return;
    busy = true;
    try {
      switch (cfg.source) {
        case "control":
          await bridge().api.post(`/controls/${encodeURIComponent(cfg.controlId)}/set`, { attribute, value });
          break;
        case "mqtt":
          await bridge().api.post(
            `/mqtt_devices/${encodeURIComponent(cfg.deviceId)}/command`,
            commandRequest(resolved, attribute, value, resolved.value),
          );
          break;
        default: {
          const call = haService(src, resolved, attribute, value);
          if (!call) throw new Error("the player cannot do that");
          await bridge().api.post(`/connections/${encodeURIComponent(cfg.connectionId)}/ha/service`, {
            domain: call.domain,
            service: call.service,
            service_data: call.data,
            target: { entity_id: cfg.entityId },
          });
        }
      }
      actionError = "";
    } catch (e: any) {
      if (e?.code === "FORBIDDEN" || e?.code === "UNAUTHORIZED") {
        denied = true;
        return;
      }
      actionError = errorMessage(e, "the player did not respond");
      if (actionTimer) clearTimeout(actionTimer);
      actionTimer = setTimeout(() => (actionError = ""), 4000);
    } finally {
      busy = false;
    }
  }

  // setVolume keeps the dragged level on screen until the player reports
  // its new state, so the slider does not jump back while the call is out.
  async function setVolume(level: number): Promise<void> {
    await write("volume", Math.min(Math.max(Math.round(level), 0), 100));
    if (actionError || denied) volumeDraft = null;
  }
</script>

{#snippet art()}
  {#if cfg.showArt}
    <div class="art">
      {#if artSrc}
        <img src={artSrc} alt="" referrerpolicy="no-referrer" />
      {:else}
        <svg class="placeholder" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M9 18V5l12-2v13" />
          <circle cx="6" cy="18" r="3" />
          <circle cx="18" cy="16" r="3" />
        </svg>
      {/if}
    </div>
  {/if}
{/snippet}

{#snippet controls()}
  {#if showControls}
    <div class="controls">
      {#if canSkip}
        <button type="button" class="control" title="Previous track" aria-label="Previous track"
                disabled={busy} onclick={() => write("command", "previous")}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path class="fill" d="M6 5h2v14H6zM20 5v14L9 12z" /></svg>
        </button>
      {/if}
      {#if canPlayPause}
        <button type="button" class="control main" title={playing ? "Pause" : "Play"}
                aria-label={playing ? "Pause" : "Play"}
                disabled={busy} onclick={() => write("state", playing ? "paused" : "playing")}>
          {#if playing}
            <svg viewBox="0 0 24 24" aria-hidden="true"><path class="fill" d="M6 5h4v14H6zM14 5h4v14h-4z" /></svg>
          {:else}
            <svg viewBox="0 0 24 24" aria-hidden="true"><path class="fill" d="M7 4v16l13-8z" /></svg>
          {/if}
        </button>
      {/if}
      {#if canSkip}
        <button type="button" class="control" title="Next track" aria-label="Next track"
                disabled={busy} onclick={() => write("command", "next")}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path class="fill" d="M16 5h2v14h-2zM4 5v14l11-7z" /></svg>
        </button>
      {/if}
    </div>
    {#if canVolume}
      <label class="volume">
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path class="fill" d="M4 9h4l5-4v14l-5-4H4z" />
          <path d="M16 8.5a5 5 0 0 1 0 7M18.5 6a8.5 8.5 0 0 1 0 12" />
        </svg>
        <input type="range" min="0" max="100" step="1" value={volume} aria-label="Volume"
               oninput={(e: Event) => (volumeDraft = Number((e.target as HTMLInputElement).value))}
               onchange={(e: Event) => setVolume(Number((e.target as HTMLInputElement).value))} />
      </label>
    {/if}
  {/if}
{/snippet}

<div class="frame">
<div class="player {cfg.layout}">
  {#if !bound}
    <span class="hint">Pick a media player in the widget settings.</span>
  {:else if error}
    <span class="error">{error}</span>
  {:else if !resolved}
    <span class="hint">Loading the player</span>
  {:else if !isMedia}
    <span class="hint">{playerName} is not a media player.</span>
  {:else}
    {@render art()}
    <div class="body">
      <div class="meta">
        <span class="status">
          <span class="state" class:active={playing}>{stateLabel(playerState)}</span>
          {#if appName}<span class="app">{appName}</span>{/if}
        </span>
        <span class="title">{title || playerName}</span>
        {#if subtitle}
          <span class="subtitle">{subtitle}</span>
        {:else if title}
          <span class="subtitle">{playerName}</span>
        {/if}
      </div>
      {#if hasProgress && (playing || playerState === "paused")}
        <div class="progress">
          <div class="bar"><div class="fill" style:width="{(position / duration) * 100}%"></div></div>
          <div class="times"><span>{clock(position)}</span><span>{clock(duration)}</span></div>
        </div>
      {/if}
      {@render controls()}
      {#if actionError}
        <span class="error small">{actionError}</span>
      {/if}
    </div>
  {/if}
</div>
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  /* The cell is a size container so text and buttons scale with it: the same
     widget reads on a phone and on a wall panel across the room. */
  .frame {
    width: 100%;
    height: 100%;
    container-type: size;
    overflow: hidden;
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
  }

  .player {
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 4cqmin;
    padding: 4cqmin;
  }

  .player.card {
    flex-direction: column;
  }

  /* A card in a wide, short cell puts the art beside the text rather than
     shrinking both to fit a column. */
  @container (min-aspect-ratio: 7 / 5) {
    .player.card {
      flex-direction: row;
    }

    .player.card .art {
      height: 100%;
    }

    .player.card .body {
      flex: 1;
      align-items: stretch;
      text-align: left;
    }

    .player.card .status,
    .player.card .controls {
      justify-content: flex-start;
    }
  }

  .art {
    flex-shrink: 0;
    aspect-ratio: 1;
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    background: var(--theme-surface0, #313244);
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .card .art {
    height: 52cqh;
    max-width: 100%;
  }

  .compact .art {
    height: 100%;
    max-height: 100%;
  }

  .art img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    display: block;
  }

  .placeholder {
    width: 45%;
    height: 45%;
    fill: none;
    stroke: var(--theme-subtext, #a6adc8);
    stroke-width: 1.5;
    stroke-linecap: round;
    stroke-linejoin: round;
    opacity: 0.6;
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: 2cqmin;
    min-width: 0;
    width: 100%;
  }

  .compact .body {
    flex: 1;
  }

  .card .body {
    align-items: center;
    text-align: center;
  }

  .meta {
    display: flex;
    flex-direction: column;
    gap: 0.6cqmin;
    min-width: 0;
    width: 100%;
  }

  .status {
    display: flex;
    gap: 0.6em;
    font-size: clamp(10px, 4cqmin, 20px);
    color: var(--theme-subtext, #a6adc8);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .card .status {
    justify-content: center;
  }

  .state.active {
    color: var(--theme-green, var(--theme-success, #a6e3a1));
  }

  .title,
  .subtitle,
  .app {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .title {
    font-size: clamp(14px, 8cqmin, 44px);
    font-weight: 600;
    line-height: 1.15;
  }

  .subtitle {
    font-size: clamp(12px, 5.5cqmin, 28px);
    color: var(--theme-subtext, #a6adc8);
  }

  .progress {
    width: 100%;
  }

  .bar {
    height: max(3px, 1.2cqmin);
    border-radius: 999px;
    background: var(--theme-surface1, #45475a);
    overflow: hidden;
  }

  .bar .fill {
    height: 100%;
    background: var(--theme-accent, #89b4fa);
    transition: width 1s linear;
  }

  .times {
    display: flex;
    justify-content: space-between;
    margin-top: 0.4em;
    font-size: clamp(10px, 3.5cqmin, 16px);
    color: var(--theme-subtext, #a6adc8);
    font-variant-numeric: tabular-nums;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 3cqmin;
  }

  .card .controls {
    justify-content: center;
  }

  .control {
    width: clamp(32px, 13cqmin, 72px);
    height: clamp(32px, 13cqmin, 72px);
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 50%;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .control.main {
    width: clamp(40px, 17cqmin, 92px);
    height: clamp(40px, 17cqmin, 92px);
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
  }

  .control:hover:not(:disabled) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .control:disabled {
    opacity: 0.5;
    cursor: default;
  }

  /* The icon is sized against the button. Percentage padding would resolve
     against the controls row's width and leave the icon no room. */
  .control svg,
  .volume svg {
    width: 56%;
    height: 56%;
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linecap: round;
  }

  svg .fill {
    fill: currentColor;
    stroke: none;
  }

  .volume {
    display: flex;
    align-items: center;
    gap: 0.6em;
    width: 100%;
    max-width: 24em;
    color: var(--theme-subtext, #a6adc8);
  }

  .volume svg {
    width: clamp(16px, 6cqmin, 32px);
    height: clamp(16px, 6cqmin, 32px);
    flex-shrink: 0;
  }

  .volume input {
    flex: 1;
    accent-color: var(--theme-accent, #89b4fa);
  }

  .hint {
    font-size: clamp(12px, 5cqmin, 20px);
    opacity: 0.6;
    text-align: center;
  }

  .error {
    font-size: clamp(12px, 5cqmin, 20px);
    color: var(--theme-error, #f38ba8);
    text-align: center;
  }

  .error.small {
    font-size: clamp(10px, 3.5cqmin, 16px);
  }
</style>
