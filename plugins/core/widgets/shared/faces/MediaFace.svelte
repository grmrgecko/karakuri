<script lang="ts">
  import { onDestroy } from "svelte";
  import SliderInput from "./SliderInput.svelte";
  import PickerInput from "./PickerInput.svelte";
  import { choices, hasFeature } from "../controlKinds";

  // A media player reads as what is playing and offers the transport the
  // player has. Every control is gated by the kind's options, which each
  // source fills from what the player reports supporting, so a speaker
  // without volume draws no slider rather than one the player refuses.
  let {
    value,
    options,
    readOnly = false,
    busy = false,
    compact = false,
    send,
  }: {
    value: unknown;
    options: Record<string, unknown>;
    readOnly?: boolean;
    busy?: boolean;
    compact?: boolean;
    send?: (attribute: string, value: unknown) => void | Promise<void>;
  } = $props();

  let fields = $derived(
    value && typeof value === "object" ? (value as Record<string, unknown>) : {},
  );
  let playerState = $derived(typeof fields.state === "string" ? fields.state : "");
  let playing = $derived(playerState === "playing");
  let off = $derived(playerState === "off");
  let title = $derived(text(fields.title));
  let subtitle = $derived([text(fields.artist), text(fields.album)].filter(Boolean).join(" · "));
  let art = $derived(text(fields.art));

  // Only an absolute URL is drawn here. A Home Assistant player's own art is a
  // path behind its token, which the Now Playing widget relays.
  let showArt = $derived(!compact && /^https?:\/\//i.test(art));

  let can = $derived({
    volume: hasFeature(options, "volume"),
    mute: hasFeature(options, "mute"),
    skip: hasFeature(options, "skip"),
    stop: hasFeature(options, "stop"),
    power: hasFeature(options, "power"),
    source: hasFeature(options, "source"),
  });

  // A player that is off takes nothing but power; pressing play on it would
  // either be refused or wake it in a way the owner did not ask for.
  let disabled = $derived(busy || off);

  // Sources come from the value first, because a Home Assistant player's list
  // changes as devices come and go, and from the options for a control whose
  // author fixed it.
  let sources = $derived.by(() => {
    const reported = Array.isArray(fields.sources) ? fields.sources.map(String) : [];
    if (reported.length > 0) return reported.map((s) => ({ value: s, label: s }));
    return choices(options, "source_list");
  });

  // Progress is a position at a time; it advances here while playing so a
  // player that reports rarely still shows a moving bar.
  let now = $state(Date.now());
  let ticker: ReturnType<typeof setInterval> | null = null;
  // arrived is when the position or the play state last changed, which a
  // player that reports no position time is measured from. Other values, such
  // as the volume, arrive without moving the position.
  let reportedPosition = $derived(num(fields.position));
  let arrived = $derived.by(() => {
    void reportedPosition;
    void playerState;
    return Date.now();
  });
  $effect(() => {
    if (!playing || compact) return;
    now = Date.now();
    ticker = setInterval(() => (now = Date.now()), 1000);
    return () => {
      if (ticker) clearInterval(ticker);
      ticker = null;
    };
  });
  onDestroy(() => {
    if (ticker) clearInterval(ticker);
  });

  let duration = $derived(num(fields.duration));
  let position = $derived.by(() => {
    const reported = reportedPosition;
    if (!Number.isFinite(reported)) return NaN;
    let pos = reported;
    if (playing) {
      const at = typeof fields.position_at === "string" ? Date.parse(fields.position_at) : NaN;
      pos += (now - (Number.isFinite(at) ? at : arrived)) / 1000;
    }
    return duration > 0 ? Math.min(Math.max(pos, 0), duration) : Math.max(pos, 0);
  });
  let hasProgress = $derived(duration > 0 && Number.isFinite(position) && (playing || playerState === "paused"));

  function text(v: unknown): string {
    return typeof v === "string" ? v.trim() : "";
  }

  function num(v: unknown): number {
    const n = typeof v === "number" ? v : Number(v);
    return Number.isFinite(n) ? n : NaN;
  }

  function label(s: string): string {
    return s ? s[0].toUpperCase() + s.slice(1) : "Not reported";
  }

  function clock(seconds: number): string {
    const total = Math.max(0, Math.floor(seconds));
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = String(total % 60).padStart(2, "0");
    return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
  }

  function press(attribute: string, next: unknown): void {
    if (readOnly) return;
    void send?.(attribute, next);
  }
</script>

{#snippet transport()}
  {#if can.skip && !compact}
    <button type="button" class="key" title="Previous" aria-label="Previous" {disabled}
            onclick={() => press("command", "previous")}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 5h2v14H6zM20 5v14L9 12z" /></svg>
    </button>
  {/if}
  <button type="button" class="key main" title={playing ? "Pause" : "Play"}
          aria-label={playing ? "Pause" : "Play"} {disabled}
          onclick={() => press("state", playing ? "paused" : "playing")}>
    {#if playing}
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 5h4v14H6zM14 5h4v14h-4z" /></svg>
    {:else}
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 4v16l13-8z" /></svg>
    {/if}
  </button>
  {#if can.skip && !compact}
    <button type="button" class="key" title="Next" aria-label="Next" {disabled}
            onclick={() => press("command", "next")}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M16 5h2v14h-2zM4 5v14l11-7z" /></svg>
    </button>
  {/if}
  {#if can.stop && !compact}
    <button type="button" class="key" title="Stop" aria-label="Stop" {disabled}
            onclick={() => press("state", "idle")}>
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6h12v12H6z" /></svg>
    </button>
  {/if}
{/snippet}

<span class="media" class:stack={!compact}>
  <span class="head">
    {#if showArt}
      <img class="art" src={art} alt="" referrerpolicy="no-referrer" />
    {/if}
    <span class="meta">
      <span class="title">{title || label(playerState)}</span>
      {#if !compact && subtitle}
        <span class="dim">{subtitle}</span>
      {/if}
      {#if !compact && title}
        <span class="dim state">{label(playerState)}</span>
      {/if}
    </span>
    {#if !readOnly && compact}
      <span class="controls">{@render transport()}</span>
    {/if}
  </span>

  {#if hasProgress && !compact}
    <span class="progress">
      <span class="bar"><span class="fill" style:width="{(position / duration) * 100}%"></span></span>
      <span class="dim times">{clock(position)} / {clock(duration)}</span>
    </span>
  {/if}

  {#if !readOnly && !compact}
    <span class="controls">
      {@render transport()}
      {#if can.power}
        <button type="button" class="key" class:on={!off} title={off ? "Turn on" : "Turn off"}
                aria-label={off ? "Turn on" : "Turn off"} disabled={busy}
                onclick={() => press("state", off ? "on" : "off")}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path class="line" d="M12 3v9M6.3 6.3a8 8 0 1 0 11.4 0" />
          </svg>
        </button>
      {/if}
    </span>
  {/if}

  {#if !readOnly && (can.volume || can.mute) && !(compact && !can.volume)}
    <span class="controls">
      {#if can.mute && !compact}
        <button type="button" class="key" class:on={fields.muted === true}
                title={fields.muted === true ? "Unmute" : "Mute"}
                aria-label={fields.muted === true ? "Unmute" : "Mute"} {disabled}
                onclick={() => press("muted", fields.muted !== true)}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 9h4l5-4v14l-5-4H4z" />
            {#if fields.muted === true}
              <path class="line" d="M16 9l5 6M21 9l-5 6" />
            {:else}
              <path class="line" d="M16 8.5a5 5 0 0 1 0 7" />
            {/if}
          </svg>
        </button>
      {/if}
      {#if can.volume}
        <SliderInput
          value={num(fields.volume)}
          min={0}
          max={100}
          step={1}
          unit="%"
          {disabled}
          oncommit={(next) => press("volume", next)}
        />
      {/if}
    </span>
  {/if}

  {#if !readOnly && !compact && can.source && sources.length > 0}
    <PickerInput
      value={text(fields.source)}
      options={sources}
      display="dropdown"
      {disabled}
      onpick={(next) => press("source", next)}
    />
  {/if}
</span>

<style>
  .media {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
  }

  .media.stack {
    flex-direction: column;
    align-items: stretch;
    gap: 0.35rem;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    min-width: 0;
  }

  .art {
    width: 2.75rem;
    height: 2.75rem;
    border-radius: 4px;
    object-fit: cover;
    flex-shrink: 0;
  }

  .meta {
    display: flex;
    flex-direction: column;
    min-width: 0;
    flex: 1 1 auto;
  }

  .title {
    font-size: 0.9rem;
    color: var(--theme-subtext0, var(--theme-text-muted, #a6adc8));
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .stack .title {
    color: var(--theme-text, #cdd6f4);
  }

  .dim {
    font-size: 0.75rem;
    opacity: 0.7;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .controls {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    min-width: 0;
  }

  .key {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1.8rem;
    height: 1.8rem;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    cursor: pointer;
    flex-shrink: 0;
  }

  .key.main {
    width: 2.1rem;
  }

  .key:hover:not(:disabled) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .key:disabled {
    cursor: default;
    opacity: 0.5;
  }

  .key.on {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-color: var(--theme-accent, #89b4fa);
  }

  .key svg {
    width: 1rem;
    height: 1rem;
    fill: currentColor;
  }

  .key svg .line {
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linecap: round;
  }

  .progress {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }

  .bar {
    flex: 1 1 auto;
    height: 3px;
    border-radius: 999px;
    background: var(--theme-surface1, #45475a);
    overflow: hidden;
  }

  .bar .fill {
    display: block;
    height: 100%;
    background: var(--theme-accent, #89b4fa);
    transition: width 1s linear;
  }

  .times {
    font-variant-numeric: tabular-nums;
  }
</style>
