<svelte:options customElement="entity-control-config" />

<script lang="ts">
  import BindingPanel from "../../shared/BindingPanel.svelte";
  import { parseConfig, type TileConfig } from "./config";

  let { config = "{}" }: { config: string | Record<string, unknown> } = $props();
</script>

<BindingPanel {config} parse={parseConfig} keep={["showName", "showState"]}>
  {#snippet extras(cfg: TileConfig, update: (patch: Partial<TileConfig>) => void)}
    <label class="check">
      <input type="checkbox" checked={cfg.showName}
             onchange={(e: Event) => update({ showName: (e.target as HTMLInputElement).checked })} />
      <span>Show the name</span>
    </label>
    <label class="check">
      <input type="checkbox" checked={cfg.showState}
             onchange={(e: Event) => update({ showState: (e.target as HTMLInputElement).checked })} />
      <span>Show the current state</span>
    </label>
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
</style>
