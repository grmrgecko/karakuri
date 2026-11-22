<svelte:options customElement="variable-buttons-widget" />

<script lang="ts">
  import { onDestroy } from "svelte";
  import { placementIds } from "../../shared/dashboardLive";
  import { optionsOf, setVariable, watchVariables, type BridgeVariables } from "../../shared/variables";
  import { parseConfig } from "./config";
  import { hostOperates } from "../../shared/entitySource";

  // Variable buttons draws a dashboard variable's options as buttons, for a
  // touch screen where a dropdown is awkward. A press switches this screen,
  // or every screen when the author chose that and the viewer may operate the
  // dashboard, which the server decides.
  let { config: configAttr = "{}" }: { config?: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));
  let dashboardId = $derived(placementIds(configAttr).dashboardId);

  let state = $state<BridgeVariables>({ dashboardId: "", variables: [], values: {} });
  const stop = watchVariables((next) => (state = next));
  onDestroy(stop);

  let variable = $derived(state.variables.find((v) => v.name === cfg.variable));
  let options = $derived(variable && variable.type !== "constant" ? optionsOf(variable) : []);
  let current = $derived(state.values[cfg.variable] ?? "");

  let busy = $state("");
  let error = $state("");
  let errorTimer: ReturnType<typeof setTimeout> | null = null;

  const bridge = () => (window as any).__karakuri;

  // locked is a dashboard-wide picker on a dashboard this viewer may only
  // watch. Switching this screen alone changes nobody else's, so it stays open.
  let locked = $derived(cfg.scope === "dashboard" && !hostOperates());

  async function press(value: string): Promise<void> {
    if (busy || locked || value === current) return;
    error = "";
    if (cfg.scope !== "dashboard") {
      setVariable(cfg.variable, value);
      return;
    }
    busy = value;
    try {
      await bridge().api.post(
        `/dashboards/${encodeURIComponent(dashboardId)}/variables/${encodeURIComponent(cfg.variable)}/value`,
        { value },
      );
      // The live state reaches every screen, this one included. Switching
      // locally too would write the value into this screen's address, which
      // would then outrank later changes when the page reloads.
    } catch (e: any) {
      error = e?.message || "The value could not be set";
      if (errorTimer) clearTimeout(errorTimer);
      errorTimer = setTimeout(() => (error = ""), 5000);
    } finally {
      busy = "";
    }
  }

  onDestroy(() => {
    if (errorTimer) clearTimeout(errorTimer);
  });
</script>

<div class="buttons-widget size-{cfg.size}">
  {#if !cfg.variable}
    <p class="empty">Choose a variable in this widget's settings.</p>
  {:else if !variable}
    <p class="empty">This dashboard has no variable named {cfg.variable}.</p>
  {:else if options.length === 0}
    <p class="empty">{variable.label || variable.name} has no values to choose from.</p>
  {:else}
    {#if cfg.showLabel}
      <div class="label">{variable.label || variable.name}</div>
    {/if}
    <div class="buttons layout-{cfg.layout}" role="radiogroup" aria-label={variable.label || variable.name}>
      {#each options as option, i (i)}
        <button
          type="button"
          role="radio"
          aria-checked={option.value === current}
          class="choice"
          class:active={option.value === current}
          class:busy={busy === option.value}
          disabled={locked}
          onclick={() => press(option.value)}
        >
          {option.label || option.value}
        </button>
      {/each}
    </div>
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}
  {/if}
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    container-type: size;
  }

  .buttons-widget {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 0.5em;
    width: 100%;
    height: 100%;
    box-sizing: border-box;
    padding: 0.25em;
    overflow: hidden;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    color: var(--theme-text, #cdd6f4);
  }

  /* Each size is a ceiling: a short placement scales the text down with its
     height, so a label and a row of buttons always fit. */
  .size-small { font-size: min(0.85rem, 18cqh); }
  .size-medium { font-size: min(1.1rem, 20cqh); }
  .size-large { font-size: min(1.6rem, 24cqh); }
  .size-fit { font-size: clamp(0.8rem, min(24cqh, 8cqw), 3rem); }

  .label {
    flex: none;
    font-size: 0.8em;
    opacity: 0.7;
  }

  .buttons {
    display: flex;
    gap: 0.5em;
    flex: 1;
    min-height: 0;
  }

  .layout-row { flex-direction: row; }
  .layout-column { flex-direction: column; }
  .layout-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(6em, 1fr));
  }

  /* Buttons fill the space the placement gives them rather than asking for
     more, so a short placement shrinks them instead of clipping them. */
  .choice {
    flex: 1;
    min-width: 0;
    min-height: 0;
    padding: 0.4em 0.8em;
    border-radius: 0.6em;
    border: 1px solid var(--theme-surface1, #45475a);
    background: var(--theme-surface0, #313244);
    color: inherit;
    font: inherit;
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    transition: background-color 0.15s ease, border-color 0.15s ease;
  }

  .choice:hover:not(:disabled) {
    border-color: var(--theme-accent, #89b4fa);
  }

  .choice.active {
    background: var(--theme-accent, #89b4fa);
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-base, #1e1e2e);
    font-weight: 600;
  }

  .choice.busy {
    opacity: 0.6;
  }

  .choice:disabled {
    cursor: default;
  }

  .empty,
  .error {
    margin: 0;
    font-size: 0.8rem;
    text-align: center;
    color: var(--theme-subtext, #a6adc8);
  }

  .error {
    color: var(--theme-error, #f38ba8);
  }
</style>
