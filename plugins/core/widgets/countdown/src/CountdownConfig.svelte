<svelte:options customElement="countdown-config" />

<script lang="ts">
  import { parseConfig, type CountdownConfig, type CountdownEvent } from "./config";
  import { placementIds } from "../../shared/dashboardLive";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<CountdownConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);

  // Every edit comes back as a new attribute one keystroke behind the input,
  // so the attribute seeds the panel once per placement and the panel owns
  // its state after that.
  let seededFor: string | null = null;
  $effect(() => {
    const { widgetId } = placementIds(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function setHost(node: HTMLElement): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }

  function update(patch: Partial<CountdownConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: $state.snapshot(cfg), bubbles: true, composed: true }),
    );
  }

  function today(): string {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  }

  function addEvent(): void {
    update({ events: [...cfg.events, { label: "", date: today() }] });
  }

  function removeEvent(index: number): void {
    update({ events: cfg.events.filter((_, i) => i !== index) });
  }

  // editEvent drops an emptied time and an unset yearly flag rather than
  // saving them as "" and false.
  function editEvent(index: number, patch: Partial<CountdownEvent>): void {
    update({
      events: cfg.events.map((e, i) => {
        if (i !== index) return e;
        const next: CountdownEvent = { ...e, ...patch };
        if (!next.time) delete next.time;
        if (!next.yearly) delete next.yearly;
        return next;
      }),
    });
  }

  function maxItemsInput(e: Event): number {
    const input = e.target as HTMLInputElement;
    let n = Math.round(Number(input.value));
    if (input.value.trim() === "" || !Number.isFinite(n) || n < 1) n = cfg.maxItems;
    input.value = String(n);
    return n;
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Events</span>
    {#each cfg.events as event, index}
      <div class="event">
        <div class="event-row">
          <input class="input" placeholder="Label" value={event.label}
                 aria-label="Event {index + 1} label"
                 oninput={(e: Event) => editEvent(index, { label: (e.target as HTMLInputElement).value })} />
          <button type="button" class="remove" title="Remove event" aria-label="Remove event {index + 1}"
                  onclick={() => removeEvent(index)}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
        <div class="event-row">
          <input class="input" type="date" value={event.date}
                 aria-label="Event {index + 1} date"
                 onchange={(e: Event) => editEvent(index, { date: (e.target as HTMLInputElement).value })} />
          <input class="input" type="time" value={event.time ?? ""}
                 aria-label="Event {index + 1} time"
                 onchange={(e: Event) => editEvent(index, { time: (e.target as HTMLInputElement).value })} />
        </div>
        <label class="checkbox-label">
          <input type="checkbox" checked={!!event.yearly}
                 onchange={(e: Event) => editEvent(index, { yearly: (e.target as HTMLInputElement).checked })} />
          <span>Every year</span>
        </label>
      </div>
    {/each}
    <button type="button" class="add" onclick={addEvent}>+ Add event</button>
    <span class="hint">Time is optional. Dates count in the viewer's local time.</span>
  </div>

  <div class="row">
    <div class="field">
      <label class="label" for="cd-layout">Layout</label>
      <select id="cd-layout" class="input" value={cfg.layout}
              onchange={(e: Event) => update({ layout: (e.target as HTMLSelectElement).value as CountdownConfig["layout"] })}>
        <option value="single">Next event</option>
        <option value="list">List</option>
      </select>
    </div>
    <div class="field">
      <label class="label" for="cd-units">Units</label>
      <select id="cd-units" class="input" value={cfg.units}
              onchange={(e: Event) => update({ units: (e.target as HTMLSelectElement).value as CountdownConfig["units"] })}>
        <option value="days">Days</option>
        <option value="auto">Days, then hours and minutes</option>
      </select>
    </div>
  </div>

  <div class="row">
    {#if cfg.layout === "list"}
      <div class="field">
        <label class="label" for="cd-max">Events shown</label>
        <input id="cd-max" class="input" type="number" min="1" value={cfg.maxItems}
               onchange={(e: Event) => update({ maxItems: maxItemsInput(e) })} />
      </div>
    {/if}
    <div class="field">
      <label class="label" for="cd-today">Today label</label>
      <input id="cd-today" class="input" value={cfg.todayLabel}
             oninput={(e: Event) => update({ todayLabel: (e.target as HTMLInputElement).value })} />
    </div>
  </div>

  <label class="checkbox-label">
    <input type="checkbox" checked={cfg.showPast}
           onchange={(e: Event) => update({ showPast: (e.target as HTMLInputElement).checked })} />
    <span>Show past events as days ago</span>
  </label>
</div>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .config-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    min-width: 0;
  }

  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-text-muted, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-text-muted, #a6adc8);
    opacity: 0.8;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-input-border, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-input-bg, var(--theme-surface0, #313244));
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    min-width: 0;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .event {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    padding: 0.4rem;
    border: 1px solid var(--theme-border-translucent, var(--theme-border, #45475a));
    border-radius: var(--theme-radius-sm, 4px);
  }

  .event-row {
    display: flex;
    gap: 0.3rem;
  }

  .remove {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    flex-shrink: 0;
    padding: 0;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: var(--theme-surface0, #313244);
    color: var(--theme-error, #f38ba8);
    cursor: pointer;
  }

  .remove:hover {
    border-color: var(--theme-error, #f38ba8);
  }

  .remove svg {
    width: 14px;
    height: 14px;
  }

  .add {
    align-self: flex-start;
    padding: 0.35rem 0.75rem;
    border: 1px dashed var(--theme-border, #45475a);
    border-radius: var(--theme-radius-sm, 4px);
    background: transparent;
    color: var(--theme-accent, #89b4fa);
    font-size: 0.8rem;
    font-family: inherit;
    cursor: pointer;
  }

  .add:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .checkbox-label input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--theme-accent, #89b4fa);
  }
</style>
