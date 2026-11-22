<svelte:options customElement="camera-config" />

<script lang="ts">
  import EntityPicker from "../../shared/EntityPicker.svelte";
  import NVRCameraPicker from "../../shared/NVRCameraPicker.svelte";
  import { parseConfig, type CameraConfig } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $state<CameraConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);

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

  function update(patch: Partial<CameraConfig>): void {
    cfg = { ...cfg, ...patch, displayOnly: true };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }),
    );
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <label class="label" for="cam-source">Source</label>
    <select id="cam-source" class="input" value={cfg.source}
            onchange={(e: Event) =>
              update({ source: (e.target as HTMLSelectElement).value as CameraConfig["source"],
                       connectionId: "", entityId: "", camera: "" })}>
      <option value="ha">Home Assistant camera</option>
      <option value="nvr">NVR camera (Frigate)</option>
    </select>
  </div>

  {#if cfg.source === "nvr"}
    <NVRCameraPicker idPrefix="cam-nvr" connectionId={cfg.connectionId} camera={cfg.camera}
                     onchange={(patch) => update(patch)} />
    <label class="check">
      <input type="checkbox" checked={cfg.bbox}
             onchange={(e: Event) => update({ bbox: (e.target as HTMLInputElement).checked })} />
      <span>Draw boxes around tracked objects</span>
    </label>
  {:else}
    <EntityPicker idPrefix="cam" domain="camera" connectionId={cfg.connectionId}
                  entityId={cfg.entityId} onchange={(patch) => update(patch)} />
  {/if}

  <div class="field">
    <label class="label" for="cam-mode">Mode</label>
    <select id="cam-mode" class="input" value={cfg.mode}
            onchange={(e: Event) =>
              update({ mode: (e.target as HTMLSelectElement).value as CameraConfig["mode"] })}>
      <option value="live">Live stream</option>
      <option value="snapshot">Snapshots</option>
    </select>
  </div>

  <div class="field">
    <label class="label" for="cam-interval">Snapshot interval (seconds)</label>
    <input id="cam-interval" class="input" type="number" min="1" step="1" value={cfg.interval}
           onchange={(e: Event) => {
             const n = Number((e.target as HTMLInputElement).value);
             update({ interval: Number.isFinite(n) && n >= 1 ? n : 10 });
           }} />
    <span class="hint">
      {cfg.mode === "live"
        ? "A live stream shows snapshots at this interval while it reconnects."
        : "How often the still image refreshes."}
    </span>
  </div>

  <div class="field">
    <label class="label" for="cam-fit">Fit</label>
    <select id="cam-fit" class="input" value={cfg.fit}
            onchange={(e: Event) =>
              update({ fit: (e.target as HTMLSelectElement).value as CameraConfig["fit"] })}>
      <option value="cover">Fill the cell and crop</option>
      <option value="contain">Show the whole image</option>
    </select>
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showName}
           onchange={(e: Event) => update({ showName: (e.target as HTMLInputElement).checked })} />
    <span>Show the camera name</span>
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

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
