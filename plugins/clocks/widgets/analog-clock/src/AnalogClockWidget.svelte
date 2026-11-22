<svelte:options customElement="analog-clock-widget" />

<script lang="ts">
  import ClassicDial from "./layouts/ClassicDial.svelte";
  import Minimalist from "./layouts/Minimalist.svelte";
  import Station from "./layouts/Station.svelte";
  import Bauhaus from "./layouts/Bauhaus.svelte";
  import DualTime from "./layouts/DualTime.svelte";
  import Skeleton from "./layouts/Skeleton.svelte";
  import SquareFace from "./layouts/SquareFace.svelte";

  // The config attribute is a JSON string passed from the dashboard host.
  let { config: configAttr = "{}" } = $props();

  const DEFAULTS = {
    timezone: "",
    secondHand: "tick",
    faceStyle: "dark",
    accentColor: "",
    layout: "classic",
    secondTimezone: "Europe/London",
  };

  // Reactively parse the config attribute whenever it changes.
  let cfg = $derived((() => {
    let parsed: Record<string, any> = {};
    if (typeof configAttr === "string") {
      try {
        parsed = JSON.parse(configAttr);
      } catch {
        parsed = {};
      }
    } else if (typeof configAttr === "object" && configAttr !== null) {
      parsed = configAttr;
    }
    return { ...DEFAULTS, ...parsed };
  })());

  // Time state updated by the timer loop.
  let hours = $state(0);
  let minutes = $state(0);
  let seconds = $state(0);
  let secondHours = $state(0);
  let secondMinutes = $state(0);

  let animFrame: number | null = null;
  let tickInterval: ReturnType<typeof setInterval> | null = null;

  // Extract time for a timezone using the Intl API, returning fractional values
  // for smooth hand positioning.
  function getTimeComponents(timezone: string, sweep: boolean): { hours: number; minutes: number; seconds: number } {
    const now = new Date();
    const fmt = new Intl.DateTimeFormat("en-US", {
      timeZone: timezone || undefined,
      hour: "numeric",
      minute: "numeric",
      second: "numeric",
      hour12: false,
    });
    const parts = fmt.formatToParts(now);
    let h = 0, m = 0, s = 0;
    for (const p of parts) {
      if (p.type === "hour") h = parseInt(p.value, 10);
      if (p.type === "minute") m = parseInt(p.value, 10);
      if (p.type === "second") s = parseInt(p.value, 10);
    }
    // Handle the edge case where hour 24 is returned instead of 0.
    if (h === 24) h = 0;
    // Add millisecond fraction for smooth sweep motion.
    if (sweep) s += now.getMilliseconds() / 1000;
    return {
      hours: (h % 12) + m / 60,
      minutes: m + s / 60,
      seconds: s,
    };
  }

  // Refresh the displayed time from the configured timezone.
  function updateTime(): void {
    const sweep = cfg.secondHand === "sweep";
    const primary = getTimeComponents(cfg.timezone, sweep);
    hours = primary.hours;
    minutes = primary.minutes;
    seconds = primary.seconds;

    // The dual-time layout needs a second timezone.
    if (cfg.layout === "dual-time") {
      const secondary = getTimeComponents(cfg.secondTimezone, false);
      secondHours = secondary.hours;
      secondMinutes = secondary.minutes;
    }
  }

  // Start or restart the timer based on the second hand mode.
  function startTimer(): void {
    stopTimer();
    updateTime();
    if (cfg.secondHand === "sweep") {
      function frame() {
        updateTime();
        animFrame = requestAnimationFrame(frame);
      }
      animFrame = requestAnimationFrame(frame);
    } else {
      tickInterval = setInterval(updateTime, 1000);
    }
  }

  function stopTimer(): void {
    if (animFrame !== null) {
      cancelAnimationFrame(animFrame);
      animFrame = null;
    }
    if (tickInterval !== null) {
      clearInterval(tickInterval);
      tickInterval = null;
    }
  }

  // Restart the timer whenever relevant config fields change.
  $effect(() => {
    const _deps = `${cfg.secondHand}:${cfg.timezone}:${cfg.layout}:${cfg.secondTimezone}`;
    startTimer();
    return () => stopTimer();
  });

  let sweep = $derived(cfg.secondHand === "sweep");
</script>

<div class="clock-container">
  {#if cfg.layout === "minimalist"}
    <Minimalist {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} />
  {:else if cfg.layout === "station"}
    <Station {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} />
  {:else if cfg.layout === "bauhaus"}
    <Bauhaus {hours} {minutes} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} />
  {:else if cfg.layout === "dual-time"}
    <DualTime {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} {secondHours} {secondMinutes} />
  {:else if cfg.layout === "skeleton"}
    <Skeleton {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} />
  {:else if cfg.layout === "square-face"}
    <SquareFace {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} />
  {:else}
    <ClassicDial {hours} {minutes} {seconds} faceStyle={cfg.faceStyle} accentColor={cfg.accentColor} {sweep} />
  {/if}
</div>

<style>
  :host {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    width: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    box-sizing: border-box;
  }

  .clock-container {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: 0.5rem;
    box-sizing: border-box;
  }
</style>
