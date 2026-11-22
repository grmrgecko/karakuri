<svelte:options customElement="countdown-widget" />

<script lang="ts">
  import { parseConfig, upcoming } from "./config";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let now = $state(new Date());

  // Each tick lands just after a minute boundary, so a timed countdown and the
  // switch to the next day change when a clock beside it does.
  $effect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const schedule = () => {
      const d = new Date();
      timer = setTimeout(() => {
        now = new Date();
        schedule();
      }, 60_000 - (d.getSeconds() * 1000 + d.getMilliseconds()) + 50);
    };
    now = new Date();
    schedule();
    return () => clearTimeout(timer);
  });

  let items = $derived(upcoming(cfg, now));
  let shown = $derived(cfg.layout === "list" ? items.slice(0, cfg.maxItems) : items.slice(0, 1));

  function formatDate(d: Date, timed: boolean): string {
    const opts: Intl.DateTimeFormatOptions = { weekday: "short", month: "short", day: "numeric" };
    if (d.getFullYear() !== now.getFullYear()) opts.year = "numeric";
    if (timed) {
      opts.hour = "numeric";
      opts.minute = "2-digit";
    }
    return d.toLocaleString([], opts);
  }

  let box = $state<HTMLDivElement | null>(null);
  let body = $state<HTMLDivElement | null>(null);

  $effect(() => {
    if (!box || !body) return;
    const el = body;
    const off = (window as any).__karakuri?.fitText(box, el, { maxFontSize: 400 });
    return () => {
      off?.();
      el.style.fontSize = "";
    };
  });
</script>

<div class="root" bind:this={box}>
  <div class="body layout-{cfg.layout}" bind:this={body}>
    {#if shown.length === 0}
      <span class="empty">{cfg.events.length === 0 ? "No events" : "No upcoming events"}</span>
    {:else if cfg.layout === "list"}
      <table class="list">
        <tbody>
          {#each shown as item}
            <tr class:today={item.days === 0} class:past={item.days < 0}>
              <td class="list-label">{item.event.label || item.event.date}</td>
              <td class="list-value">{item.value}{#if item.unit}<span class="list-unit"> {item.unit}</span>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      {@const item = shown[0]}
      <div class="single" class:past={item.days < 0}>
        <div class="single-label">{item.event.label || item.event.date}</div>
        <div class="single-value" class:today={item.days === 0}>{item.value}</div>
        {#if item.unit}<div class="single-unit">{item.unit}</div>{/if}
        <div class="single-date">{formatDate(item.at, !!item.event.time)}</div>
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
    color: var(--theme-text, #cdd6f4);
    background: var(--theme-widget-bg, transparent);
    border-radius: var(--theme-radius-md, 8px);
    overflow: hidden;
    box-sizing: border-box;
  }

  .root {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    padding: 0.5rem 0.75rem;
    box-sizing: border-box;
    overflow: hidden;
  }

  /* fitText measures the body, so it is shrink-wrapped to its content. */
  .body {
    display: inline-block;
    line-height: 1.15;
  }

  .empty {
    color: var(--theme-text-muted, #a6adc8);
  }

  .single {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
  }

  .single-label {
    font-size: 0.6em;
    font-weight: 500;
  }

  .single-value {
    font-size: 1.8em;
    font-weight: 600;
    line-height: 1;
    font-variant-numeric: tabular-nums;
    color: var(--theme-heading-color, var(--theme-text, #cdd6f4));
  }

  .single-value.today {
    color: var(--theme-accent, #89b4fa);
  }

  .single-unit {
    font-size: 0.5em;
    color: var(--theme-text-muted, #a6adc8);
  }

  .single-date {
    margin-top: 0.3em;
    font-size: 0.32em;
    color: var(--theme-text-muted, #a6adc8);
  }

  .list {
    border-collapse: collapse;
  }

  .list td {
    padding: 0.12em 0;
  }

  .list-label {
    padding-right: 1em !important;
  }

  .list-value {
    text-align: right;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .list-unit {
    font-size: 0.7em;
    font-weight: 400;
    color: var(--theme-text-muted, #a6adc8);
  }

  .list tr.today .list-value {
    color: var(--theme-accent, #89b4fa);
  }

  .past {
    opacity: 0.6;
  }
</style>
