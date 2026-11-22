<svelte:options customElement="media-player-config" />

<script lang="ts">
  import EntityPicker from "../../shared/EntityPicker.svelte";
  import SourceSelect from "../../shared/SourceSelect.svelte";
  import { parseConfig, type MediaConfig, type MediaSource } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $state<MediaConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);
  // Only players are offered: a control or device of another kind would draw
  // as "not a media player".
  let controls = $state<{ id: string; name: string }[]>([]);
  let devices = $state<{ id: string; name: string }[]>([]);

  const bridge = () => (window as any).__karakuri;

  $effect(() => {
    void load();
  });

  async function load(): Promise<void> {
    try {
      const res = await bridge().api.get("/controls");
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      controls = list.filter((c) => c.kind === "media");
    } catch {
      controls = [];
    }
    try {
      const res = await bridge().api.get("/mqtt_devices");
      const list: any[] = Array.isArray(res) ? res : (res?.data ?? []);
      devices = list.filter((d) => d.control === "media");
    } catch {
      // Reading devices needs connection read; the other sources still work.
      devices = [];
    }
  }

  // Every edit round trips through a save and comes back as a new attribute a
  // keystroke behind, so the attribute seeds the panel once per placement and
  // the panel owns its state from then on.
  let seededFor: string | null = null;
  $effect(() => {
    const widgetId = placementId(configAttr);
    if (widgetId === seededFor) return;
    seededFor = widgetId;
    cfg = parseConfig(configAttr);
  });

  function placementId(attr: string | Record<string, unknown>): string {
    if (typeof attr !== "string") return String((attr as any)?.dashboardWidgetId ?? "");
    try {
      return String(JSON.parse(attr)?.dashboardWidgetId ?? "");
    } catch {
      return "";
    }
  }

  function setHost(node: HTMLElement): void {
    host = ((node.getRootNode() as ShadowRoot)?.host as HTMLElement) || node;
  }

  function update(patch: Partial<MediaConfig>): void {
    cfg = { ...cfg, ...patch };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }),
    );
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <span class="label">Source</span>
    <div class="source-buttons">
      {#each [["ha", "Home Assistant"], ["control", "Control"], ["mqtt", "Device"]] as [value, label] (value)}
        <button type="button" class="source" class:active={cfg.source === value}
                onclick={() => update({ source: value as MediaSource })}>{label}</button>
      {/each}
    </div>
  </div>

  {#if cfg.source === "control"}
    <div class="field">
      <label class="label" for="mp-control">Control</label>
      <SourceSelect id="mp-control" value={cfg.controlId} placeholder="Select a media control..."
                    options={controls.map((c) => ({ value: c.id, label: c.name }))}
                    onchange={(id) => update({ controlId: id })} />
      {#if controls.length === 0}
        <span class="hint">No media controls yet; define one of kind Media player under Settings → Controls.</span>
      {/if}
    </div>
  {:else if cfg.source === "mqtt"}
    <div class="field">
      <label class="label" for="mp-device">Device</label>
      <SourceSelect id="mp-device" value={cfg.deviceId} placeholder="Select a media device..."
                    options={devices.map((d) => ({ value: d.id, label: d.name }))}
                    onchange={(id) => update({ deviceId: id })} />
      {#if devices.length === 0}
        <span class="hint">No media devices yet; an MQTT device of kind Media player or an ESPHome speaker appears here.</span>
      {/if}
    </div>
  {:else}
    <EntityPicker idPrefix="mp" domain="media_player" connectionId={cfg.connectionId}
                  entityId={cfg.entityId} onchange={(patch) => update(patch)} />
  {/if}

  <div class="field">
    <label class="label" for="mp-layout">Layout</label>
    <select id="mp-layout" class="input" value={cfg.layout}
            onchange={(e: Event) =>
              update({ layout: (e.target as HTMLSelectElement).value as MediaConfig["layout"] })}>
      <option value="card">Card</option>
      <option value="compact">Compact row</option>
    </select>
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showArt}
           onchange={(e: Event) => update({ showArt: (e.target as HTMLInputElement).checked })} />
    <span>Show cover art</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.showControls}
           onchange={(e: Event) => update({ showControls: (e.target as HTMLInputElement).checked })} />
    <span>Show playback controls</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.showVolume}
           onchange={(e: Event) => update({ showVolume: (e.target as HTMLInputElement).checked })} />
    <span>Show the volume slider</span>
  </label>
  <label class="check">
    <input type="checkbox" checked={cfg.displayOnly}
           onchange={(e: Event) => update({ displayOnly: (e.target as HTMLInputElement).checked })} />
    <span>Display only</span>
  </label>
  <span class="hint">
    Display only hides the controls and stops anyone pressing them, here and on the server.
    Controls also need a dashboard that allows control.
  </span>
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
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }

  .input {
    padding: 0.4rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    width: 100%;
    box-sizing: border-box;
  }

  .input:focus {
    border-color: var(--theme-accent, #89b4fa);
  }

  .source-buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }

  .source {
    padding: 0.3rem 0.6rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: transparent;
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.8rem;
    cursor: pointer;
  }

  .source.active {
    background: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    border-color: var(--theme-accent, #89b4fa);
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
