<svelte:options customElement="variable-buttons-config" />

<script lang="ts">
  import { onDestroy } from "svelte";
  import { watchVariables, type BridgeVariables } from "../../shared/variables";
  import { parseConfig, type ButtonsConfig } from "./config";

  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $state<ButtonsConfig>(parseConfig("{}"));
  let host = $state<Element | null>(null);

  // The attribute seeds the panel once per placement; the panel owns its
  // state from then on, because every edit round trips through a save.
  let seededFor: string | null = null;
  $effect(() => {
    let id = "";
    try {
      id = String((typeof configAttr === "string" ? JSON.parse(configAttr) : configAttr)?.dashboardWidgetId ?? "");
    } catch {
      id = "";
    }
    if (id === seededFor) return;
    seededFor = id;
    cfg = parseConfig(configAttr);
  });

  let state = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  const stop = watchVariables((next) => (state = next));
  onDestroy(stop);
  let pickable = $derived(state.variables.filter((v) => v.type !== "constant"));

  function update(patch: Partial<ButtonsConfig>): void {
    cfg = { ...cfg, ...patch };
    cfg.displayOnly = cfg.scope !== "dashboard";
    host?.dispatchEvent(new CustomEvent("configchange", { detail: { ...cfg }, bubbles: true, composed: true }));
  }

  function setHost(node: Element): void {
    host = (node.getRootNode() as ShadowRoot)?.host || node;
  }
</script>

<div class="config-form" use:setHost>
  <div class="field">
    <label class="label" for="vb-variable">Variable</label>
    <select id="vb-variable" class="input" value={cfg.variable}
            onchange={(e: Event) => update({ variable: (e.target as HTMLSelectElement).value })}>
      <option value="">Select a variable...</option>
      {#each pickable as v (v.name)}
        <option value={v.name}>{v.label || v.name}</option>
      {/each}
      {#if cfg.variable && !pickable.some((v) => v.name === cfg.variable)}
        <option value={cfg.variable}>{cfg.variable}</option>
      {/if}
    </select>
    {#if pickable.length === 0}
      <span class="hint">Add a variable under the dashboard's settings, Variables tab.</span>
    {/if}
  </div>

  <div class="field">
    <span class="label">A press switches</span>
    <label class="choice">
      <input type="radio" name="vb-scope" checked={cfg.scope === "screen"} onchange={() => update({ scope: "screen" })} />
      <span>This screen only</span>
    </label>
    <label class="choice">
      <input type="radio" name="vb-scope" checked={cfg.scope === "dashboard"} onchange={() => update({ scope: "dashboard" })} />
      <span>Every screen</span>
    </label>
    <span class="hint">
      {#if cfg.scope === "dashboard"}
        Like a flow's Set variable command. Only people who may operate this dashboard can press it: those you shared it
        with, or anyone when anonymous control is on.
      {:else}
        Like the picker above the dashboard: other screens keep their own value.
      {/if}
    </span>
  </div>

  <div class="field">
    <label class="label" for="vb-layout">Layout</label>
    <select id="vb-layout" class="input" value={cfg.layout}
            onchange={(e: Event) => update({ layout: (e.target as HTMLSelectElement).value as ButtonsConfig["layout"] })}>
      <option value="row">Row</option>
      <option value="column">Column</option>
      <option value="grid">Grid</option>
    </select>
  </div>

  <div class="field">
    <label class="label" for="vb-size">Size</label>
    <select id="vb-size" class="input" value={cfg.size}
            onchange={(e: Event) => update({ size: (e.target as HTMLSelectElement).value as ButtonsConfig["size"] })}>
      <option value="small">Small</option>
      <option value="medium">Medium</option>
      <option value="large">Large</option>
      <option value="fit">Fit the widget</option>
    </select>
  </div>

  <label class="choice">
    <input type="checkbox" checked={cfg.showLabel} onchange={(e: Event) => update({ showLabel: (e.target as HTMLInputElement).checked })} />
    <span>Show the variable's label</span>
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
    gap: 0.85rem;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }

  .label {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--theme-subtext, #a6adc8);
  }

  .input {
    width: 100%;
    box-sizing: border-box;
    padding: 0.4rem 0.5rem;
    border-radius: 6px;
    border: 1px solid var(--theme-surface1, #45475a);
    background: var(--theme-base, #1e1e2e);
    color: inherit;
    font: inherit;
    font-size: 0.85rem;
  }

  .choice {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    font-size: 0.85rem;
  }

  .hint {
    font-size: 0.7rem;
    color: var(--theme-subtext, #a6adc8);
    opacity: 0.8;
  }
</style>
