<svelte:options customElement="button-config" />

<script lang="ts">
  import BindingPanel from "../../shared/BindingPanel.svelte";
  import { parseConfig, type ButtonConfig } from "./config";

  let { config = "{}" }: { config: string | Record<string, unknown> } = $props();
</script>

<BindingPanel {config} parse={parseConfig} keep={["showLabel", "showValue", "confirmFirst"]}>
  {#snippet extras(cfg: ButtonConfig, update: (patch: Partial<ButtonConfig>) => void)}
    <label class="check">
      <input type="checkbox" checked={cfg.showLabel}
             onchange={(e: Event) => update({ showLabel: (e.target as HTMLInputElement).checked })} />
      <span>Show the label</span>
    </label>
    <label class="check">
      <input type="checkbox" checked={cfg.showValue}
             onchange={(e: Event) => update({ showValue: (e.target as HTMLInputElement).checked })} />
      <span>Show the reading</span>
    </label>
    <label class="check">
      <input type="checkbox" checked={!!cfg.confirmFirst}
             onchange={(e: Event) => update({ confirmFirst: (e.target as HTMLInputElement).checked || undefined })} />
      <span>Ask for a second tap</span>
    </label>
    <span class="hint">
      A press toggles a switch, light, or fan, locks or unlocks a lock, runs a scene, script, or
      single-action control, and fires the first trigger of a flow trigger. Anything else, and a
      display-only binding, shows its reading without sending.
    </span>
  {/snippet}
</BindingPanel>

<style>
  :host {
    display: block;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  .check {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: 0.85rem;
    cursor: pointer;
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.7;
  }
</style>
