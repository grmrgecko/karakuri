<svelte:options customElement="nvr-events-config" />

<script lang="ts">
  import NVRCameraPicker from "../../shared/NVRCameraPicker.svelte";
  import { parseConfig, type NVREventsConfig } from "./config";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $state<NVREventsConfig>(parseConfig("{}"));
  let host = $state<HTMLElement | null>(null);

  // The attribute seeds the panel once per placement; the panel owns its
  // state from then on, since every edit comes back a keystroke behind.
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

  function update(patch: Partial<NVREventsConfig>): void {
    cfg = { ...cfg, ...patch, source: "nvr", displayOnly: true };
    host?.dispatchEvent(
      new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }),
    );
  }
</script>

<div class="config-form" use:setHost>
  <NVRCameraPicker idPrefix="ev" allowAll connectionId={cfg.connectionId} camera={cfg.camera}
                   onchange={(patch) => update(patch)} />

  <div class="field">
    <label class="label" for="ev-labels">Labels</label>
    <input id="ev-labels" class="input" placeholder="person, car" value={cfg.labels}
           onchange={(e: Event) => update({ labels: (e.target as HTMLInputElement).value })} />
    <span class="hint">Only these objects, comma separated. Empty lists every label.</span>
  </div>

  <div class="field">
    <label class="label" for="ev-zones">Zones</label>
    <input id="ev-zones" class="input" placeholder="driveway" value={cfg.zones}
           onchange={(e: Event) => update({ zones: (e.target as HTMLInputElement).value })} />
  </div>

  <div class="field">
    <label class="label" for="ev-limit">Events shown</label>
    <input id="ev-limit" class="input" type="number" min="1" max="100" step="1" value={cfg.limit}
           onchange={(e: Event) => {
             const n = Number((e.target as HTMLInputElement).value);
             update({ limit: Number.isFinite(n) && n >= 1 ? Math.min(Math.round(n), 100) : 10 });
           }} />
  </div>

  <label class="check">
    <input type="checkbox" checked={cfg.showThumbnails}
           onchange={(e: Event) => update({ showThumbnails: (e.target as HTMLInputElement).checked })} />
    <span>Show thumbnails</span>
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

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.8rem;
  }
</style>
