<svelte:options customElement="entity-control-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import {
    BLANK_RESOLVED,
    displayOnly,
    formatValue,
    parseConfig,
    pressable,
    primaryAttribute,
    configuredPresses,
    triggerPresses,
    truthy,
    type Resolved,
  } from "./config";
  import { bindSource, firePress, sendCommand } from "../../shared/bindingSession";
  import ControlFace from "../../shared/faces/ControlFace.svelte";
  import { iconFontStack, iconGlyph, loadIcons } from "../../shared/icons";
  import { matchState, safeColor, stateLabel } from "../../shared/appearance";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let resolved = $state<Resolved>(BLANK_RESOLVED);
  let error = $state("");
  // staleSince is when a restored device value was last reported; it clears
  // once the device confirms its state after a server restart.
  let staleSince = $state("");
  let busy = $state(false);
  // armed is the press waiting for its second tap; flashed is the press whose
  // result is still showing.
  let armed = $state("");
  let flash = $state<"" | "ok" | "error">("");
  let flashed = $state("");
  let catalogVersion = $state(0);

  let flashTimer: ReturnType<typeof setTimeout> | null = null;
  let armTimer: ReturnType<typeof setTimeout> | null = null;

  const bridge = () => (window as any).__karakuri;

  // Re-bind whenever the binding changes. The signature is the binding itself,
  // so editing the device re-subscribes and toggling the name off does not.
  let bindingKey = $derived(
    JSON.stringify({
      source: cfg.source,
      controlId: cfg.controlId,
      connectionId: cfg.connectionId,
      entityId: cfg.entityId,
      deviceId: cfg.deviceId,
      deviceField: cfg.deviceField,
      triggers: cfg.triggers,
      flowId: cfg.flowId,
      trigger: cfg.trigger,
      name: cfg.name,
      format: cfg.format,
      precision: cfg.precision,
      fieldUnit: cfg.fieldUnit,
      fieldPrecision: cfg.fieldPrecision,
      showCoordinates: cfg.showCoordinates,
    }),
  );

  // The session reads the source and follows it; releasing it on a rebind or
  // teardown drops every subscription, including one a read still in flight
  // would have made.
  $effect(() => {
    const key = bindingKey;
    void key;
    // Only the key re-binds: the session reads the config it was started
    // with, so a change to a state or the name toggle leaves it alone.
    return untrack(() =>
      bindSource(cfg, {
        resolved: (r) => (resolved = r),
        error: (message) => (error = message),
        stale: (since) => (staleSince = since),
        catalog: () => (catalogVersion += 1),
      }),
    );
  });

  onDestroy(() => {
    if (flashTimer) clearTimeout(flashTimer);
    if (armTimer) clearTimeout(armTimer);
  });

  // ---- Interaction ----

  function setFlash(result: "ok" | "error", key: string): void {
    flash = result;
    flashed = key;
    busy = false;
    if (flashTimer) clearTimeout(flashTimer);
    flashTimer = setTimeout(() => {
      flash = "";
      flashed = "";
    }, 2000);
  }

  async function act(run: () => Promise<void>): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      await run();
      busy = false;
      error = "";
    } catch (e: any) {
      busy = false;
      error = e?.message || "failed";
      setFlash("error", "");
    }
  }

  // command is the one path an interaction takes. The face says which attribute
  // moved and what to; where that goes is the source's business.
  function command(attribute: string, value: unknown): Promise<void> {
    if (cfg.source === "flow-trigger") return fireTrigger(String(value));
    return act(() => sendCommand(cfg, resolved, attribute, value));
  }

  // A confirm-first press arms on the first tap and fires on the second, so a
  // wall tablet cannot start something destructive on a stray touch.
  async function fireTrigger(pressId: string): Promise<void> {
    const press = triggerPresses(cfg).find((p) => p.id === pressId);
    if (!press) return;
    if (!press.flowId || !press.trigger) {
      error = "this button names no trigger yet";
      return;
    }

    if (press.confirmFirst && armed !== pressId) {
      armed = pressId;
      if (armTimer) clearTimeout(armTimer);
      armTimer = setTimeout(() => (armed = ""), 3000);
      return;
    }
    armed = "";

    await act(async () => {
      await firePress(press);
      setFlash("ok", pressId);
    });
  }

  // ---- Rendering ----

  const icons = loadIcons();
  const fontStack = iconFontStack();
  // look is the first of the binding's states its reading matches. Nothing
  // matches before the source answers, while it reports an error, or while it
  // is unavailable.
  let look = $derived(error ? null : matchState(cfg.states, resolved, resolved !== BLANK_RESOLVED));
  let icon = $derived(iconGlyph(look?.icon || cfg.icon || "", icons));

  let bound = $derived(
    (cfg.source === "control" && !!cfg.controlId) ||
      (cfg.source === "ha" && !!cfg.entityId) ||
      (cfg.source === "mqtt" && !!cfg.deviceId) ||
      (cfg.source === "flow-trigger" && configuredPresses(cfg).length > 0),
  );

  let readOnly = $derived(!pressable(cfg, resolved) || displayOnly(cfg));
  let baseLabel = $derived(resolved.name || cfg.name || "—");
  let label = $derived(look?.label ? stateLabel(look.label, resolved, baseLabel) : baseLabel);
  let lookStyle = $derived(
    [
      safeColor(look?.background) ? `background:${safeColor(look?.background)}` : "",
      safeColor(look?.color) ? `color:${safeColor(look?.color)}` : "",
    ]
      .filter(Boolean)
      .join(";"),
  );
  let iconColor = $derived(safeColor(look?.iconColor) || null);

  // The whole tile is pressable for the controls that take one action, so a
  // switch is the size of a hand rather than a thumbnail. A row of buttons has
  // its own targets, and anything with an input needs that input hit exactly.
  let single = $derived(
    !readOnly &&
      bound &&
      (resolved.kind === "boolean" ||
        resolved.kind === "lock" ||
        (resolved.kind === "buttons" &&
          (resolved.options?.buttons as unknown[] | undefined)?.length === 1)),
  );

  // stateText is the caption under a switch: what the source reports rather
  // than what the tap asked for.
  let stateText = $derived.by(() => {
    if (cfg.source === "flow-trigger") return armed ? "Tap again" : flash === "ok" ? "Sent" : "";
    if (resolved.kind === "boolean") return truthy(resolved.value) ? "On" : "Off";
    if (resolved.kind === "lock") return String(resolved.value ?? "");
    return formatValue(resolved.value, resolved.options);
  });

  function activate(): void {
    if (resolved.kind === "buttons") {
      const first = (resolved.options?.buttons as Array<{ value: string }> | undefined)?.[0];
      void command("value", first?.value ?? true);
      return;
    }
    if (resolved.kind === "lock") {
      void command("state", resolved.value === "locked" ? "unlocked" : "locked");
      return;
    }
    void command(primaryAttribute(resolved.kind), !truthy(resolved.value));
  }

  // The empty string means nothing is armed and nothing has flashed, so it must
  // never match a button: the lone unnamed action carries an empty value.
  function buttonState(value: string): "" | "armed" | "ok" | "error" {
    if (armed && armed === value) return "armed";
    if (flash && flashed && flashed === value) return flash;
    return "";
  }

  // The tile is a real <button> when it is pressable and a <div> when it is
  // not, so focus and the keyboard come from the element rather than from
  // handlers, and the classes are built once for both.
  let tileClass = $derived(
    [
      "tile",
      // A state that sets its own colors replaces the on tint rather than
      // mixing the accent into them.
      bound && !error && !lookStyle && resolved.kind !== "buttons" && truthy(attributeOn()) ? "on" : "",
      busy ? "busy" : "",
      armed ? "armed" : "",
      flash === "ok" ? "ok" : "",
      flash === "error" ? "failed" : "",
    ]
      .filter(Boolean)
      .join(" "),
  );

  // attributeOn is what tints the tile: whether the thing is on, for the kinds
  // that have an on at all.
  function attributeOn(): unknown {
    const value = resolved.value;
    if (value && typeof value === "object") return (value as Record<string, unknown>).on;
    if (resolved.kind === "lock") return value === "unlocked";
    return value;
  }

  // The face is drawn at its natural size and scaled to fill the cell, rather
  // than sized in fractions of the cell: a tile with no icon, or a short name,
  // has more room and should use it, and a wide short cell should scale by its
  // width instead of being held down by its height. The host owns the
  // measuring, and re-runs it when the cell resizes or the value changes.
  // staleTitle explains a restored value: what the device last said, and that
  // it has not said anything since the server started.
  function staleTitle(since: string): string {
    const at = new Date(since);
    const when = Number.isNaN(at.getTime()) ? "before the server restarted" : at.toLocaleString();
    return `Last reported ${when}; not confirmed since the server restarted.`;
  }

  let tileEl = $state<HTMLElement | null>(null);
  let faceEl = $state<HTMLElement | null>(null);

  $effect(() => {
    if (!tileEl || !faceEl) return;
    const cleanup = bridge()?.autoScale?.(tileEl, faceEl, { padding: 8, maxScale: 6 });
    return () => cleanup?.();
  });
</script>

{#snippet face()}
  {#if !bound}
    <span class="hint">Pick a source in the widget settings.</span>
  {:else}
    {#if icon.text}
      <span class="icon" class:glyph={icon.glyph} style:font-family={icon.glyph ? fontStack : null}
            style:color={iconColor}>
        {icon.text}
      </span>
    {/if}

    {#if cfg.showName}
      <span class="name" title={label}>{label}</span>
    {/if}
    {#if staleSince && !error}
      <span class="stale" title={staleTitle(staleSince)}>last known</span>
    {/if}

    {#if error}
      <span class="error" title={error}>{error}</span>
    {:else if single}
      <!-- The tile itself is the target, so the face carries the reading and
           nothing that would swallow the press. -->
      {#if cfg.showState && stateText}
        <span class="value">{stateText}</span>
      {/if}
    {:else}
      {#key catalogVersion}
        <ControlFace {resolved} {readOnly} {busy} compact={false} {buttonState} {command} />
      {/key}
      {#if cfg.showState && readOnly === false && resolved.kind === "boolean" && stateText}
        <span class="value">{stateText}</span>
      {/if}
    {/if}
  {/if}
{/snippet}

{#if single}
  <button type="button" class="{tileClass} pressable" style={lookStyle || null} onclick={activate} bind:this={tileEl}>
    <div class="face" bind:this={faceEl}>{@render face()}</div>
  </button>
{:else}
  <div class={tileClass} style={lookStyle || null} bind:this={tileEl}>
    <div class="face" bind:this={faceEl}>{@render face()}</div>
  </div>
{/if}

<style>
  .stale {
    font-size: 0.7em;
    opacity: 0.55;
  }

  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  /* The tile is the frame; the face inside it is what scales. Keeping the
     border and tint on the frame means they stay crisp at any cell size while
     the contents grow. No CSS padding here: the scaler measures the tile's
     client box, and padding is passed to it instead so it is not counted
     twice. */
  .tile {
    display: block;
    position: relative;
    width: 100%;
    height: 100%;
    padding: 0;
    box-sizing: border-box;
    border: 1px solid var(--tile-border, var(--theme-border, var(--theme-surface1, #45475a)));
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
    overflow: hidden;
    text-align: center;
    font-family: inherit;
    font-size: 1rem;
    transition:
      background-color 0.15s,
      border-color 0.15s,
      color 0.15s;
  }

  /* The face is laid out at its natural size and the host scales it to fill
     the tile, so what is in it decides how big it gets: drop the icon or the
     name and the rest grows into the space they left. It is positioned at the
     tile's own origin because the scaler centres it by translating from there
     — and a <button>, which is what a pressable tile is, centres its contents
     itself, so left in flow the face would be shifted twice and hang out the
     bottom. */
  .face {
    position: absolute;
    top: 0;
    left: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.3rem;
  }

  .tile.pressable {
    cursor: pointer;
    user-select: none;
  }

  .tile.pressable:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  /* On-state is the whole tile rather than a switch: at wall distance the
     colour is what reads, and it is the reported state, never the tap. */
  .tile.on {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
    background: color-mix(in srgb, var(--theme-accent, #89b4fa) 14%, transparent);
  }

  .tile.armed {
    border-color: var(--theme-warning, #f9e2af);
    color: var(--theme-warning, #f9e2af);
  }

  .tile.ok {
    border-color: var(--theme-success, #a6e3a1);
    color: var(--theme-success, #a6e3a1);
  }

  .tile.failed {
    border-color: var(--theme-error, #f38ba8);
  }

  .tile.busy {
    opacity: 0.6;
  }

  .icon {
    font-size: 2.75rem;
    line-height: 1.1;
  }

  /* A pack glyph is a character in an icon font: no synthetic bold, or the
     brand face — which ships only one weight — comes out smeared. */
  .icon.glyph {
    font-weight: 900;
    font-synthesis: none;
    color: var(--theme-accent, #89b4fa);
  }

  /* A name long enough to dwarf everything else is cut rather than allowed to
     set the scale: the whole face would shrink to fit one long entity id. */
  .name {
    max-width: 16ch;
    font-size: 1rem;
    line-height: 1.2;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .value {
    max-width: 16ch;
    font-size: 1.75rem;
    font-weight: 600;
    line-height: 1.1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .error {
    max-width: 20ch;
    font-size: 0.8rem;
    color: var(--theme-error, #f38ba8);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hint {
    max-width: 20ch;
    font-size: 0.8rem;
    opacity: 0.6;
  }

  .button-row {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    justify-content: center;
  }

  .face-action {
    padding: 3px 10px;
    border-radius: 5px;
    border: 1px solid var(--theme-border, #45475a);
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font: inherit;
    cursor: pointer;
  }

  .face-action:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  .range {
    width: 9rem;
    accent-color: var(--theme-accent, #89b4fa);
  }

  .picker,
  .text-input {
    max-width: 12ch;
    padding: 0.15rem 0.35rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.9rem;
  }
</style>
