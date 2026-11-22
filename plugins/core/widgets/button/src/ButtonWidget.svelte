<svelte:options customElement="button-widget" />

<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { parseConfig, singleAction, isOn, type Action } from "./config";
  import {
    BLANK_RESOLVED,
    configuredPresses,
    displayOnly,
    formatValue,
    pressable,
    primaryAttribute,
    triggerPresses,
    type Resolved,
  } from "../../shared/entitySource";
  import { bindSource, firePress, sendCommand } from "../../shared/bindingSession";
  import { matchState, safeColor, stateLabel } from "../../shared/appearance";
  import { iconFontStack, iconGlyph, loadIcons } from "../../shared/icons";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  let resolved = $state<Resolved>(BLANK_RESOLVED);
  let error = $state("");
  let staleSince = $state("");
  let catalogVersion = $state(0);
  let busy = $state(false);
  // armed is true while a confirm-first press waits for its second tap.
  let armed = $state(false);
  let flash = $state<"" | "ok" | "error">("");

  let flashTimer: ReturnType<typeof setTimeout> | null = null;
  let armTimer: ReturnType<typeof setTimeout> | null = null;

  const bridge = () => (window as any).__karakuri;

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
    }),
  );

  $effect(() => {
    const key = bindingKey;
    void key;
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

  let bound = $derived(
    (cfg.source === "control" && !!cfg.controlId) ||
      (cfg.source === "ha" && !!cfg.entityId) ||
      (cfg.source === "mqtt" && !!cfg.deviceId) ||
      (cfg.source === "flow-trigger" && configuredPresses(cfg).length > 0),
  );

  // action is what a press sends. It is null for a reading with nothing to
  // press, a display-only binding, and a viewer who may not operate, so the
  // button is then a plain tile with no pointer and no role.
  let action = $derived.by<Action | null>(() => {
    void catalogVersion;
    if (!bound || error && resolved === BLANK_RESOLVED) return null;
    if (!pressable(cfg, resolved) || displayOnly(cfg)) return null;
    return singleAction(cfg, resolved, primaryAttribute);
  });

  // ---- Pressing ----

  function setFlash(result: "ok" | "error"): void {
    flash = result;
    if (flashTimer) clearTimeout(flashTimer);
    flashTimer = setTimeout(() => (flash = ""), 2000);
  }

  // confirms reports whether this press must be armed first: the button's own
  // setting, or the trigger press's.
  function confirms(a: Action): boolean {
    if (cfg.confirmFirst) return true;
    if (a.kind !== "trigger") return false;
    return !!triggerPresses(cfg).find((p) => p.id === a.pressId)?.confirmFirst;
  }

  async function press(): Promise<void> {
    const a = action;
    if (!a || busy) return;
    // A confirm-first press arms on the first tap and fires on a second
    // within three seconds, so a stray touch on a wall panel sends nothing.
    if (confirms(a) && !armed) {
      armed = true;
      if (armTimer) clearTimeout(armTimer);
      armTimer = setTimeout(() => (armed = false), 3000);
      return;
    }
    armed = false;
    if (armTimer) clearTimeout(armTimer);

    busy = true;
    try {
      if (a.kind === "trigger") {
        const p = triggerPresses(cfg).find((t) => t.id === a.pressId);
        if (!p) throw new Error("this button names no trigger yet");
        await firePress(p);
      } else {
        await sendCommand(cfg, resolved, a.attribute, a.value);
      }
      error = "";
      setFlash("ok");
    } catch (e: any) {
      error = e?.message || "failed";
      setFlash("error");
    } finally {
      busy = false;
    }
  }

  // ---- Drawing ----

  const icons = loadIcons();
  const fontStack = iconFontStack();

  let ready = $derived(resolved !== BLANK_RESOLVED && !(error && !flash));
  let look = $derived(matchState(cfg.states, resolved, ready));
  let icon = $derived(iconGlyph(look?.icon || cfg.icon || "", icons));
  let baseLabel = $derived(resolved.name || cfg.name || "");
  let label = $derived(look?.label ? stateLabel(look.label, resolved, baseLabel) : baseLabel);

  // valueText is the reading under the label. A trigger has none; a switch
  // reads as On or Off rather than true.
  let valueText = $derived.by(() => {
    if (cfg.source === "flow-trigger" || resolved === BLANK_RESOLVED) return "";
    if (resolved.unavailable) return "Unavailable";
    if (resolved.kind === "boolean") return isOn(resolved) ? "On" : "Off";
    if (resolved.kind === "light" || resolved.kind === "fan") return isOn(resolved) ? "On" : "Off";
    if (resolved.kind === "buttons") return "";
    const value =
      resolved.value && typeof resolved.value === "object"
        ? (resolved.value as Record<string, unknown>)[primaryAttribute(resolved.kind)]
        : resolved.value;
    return formatValue(value, resolved.options);
  });

  // caption replaces the reading while a press is in flight or its result is
  // showing, so the outcome is read where the eye already is.
  let caption = $derived(
    armed ? "Tap again" : busy ? "…" : flash === "ok" && cfg.source === "flow-trigger" ? "Sent" : "",
  );

  let on = $derived(bound && !error && isOn(resolved));
  let lookStyle = $derived(
    [
      safeColor(look?.background) ? `background:${safeColor(look?.background)}` : "",
      safeColor(look?.color) ? `color:${safeColor(look?.color)}` : "",
    ]
      .filter(Boolean)
      .join(";") || null,
  );
  let iconColor = $derived(safeColor(look?.iconColor) || null);
  let tileClass = $derived(
    [
      "button",
      action ? "pressable" : "",
      on && !lookStyle ? "on" : "",
      busy ? "busy" : "",
      armed ? "armed" : "",
      flash === "ok" ? "ok" : "",
      flash === "error" ? "failed" : "",
    ]
      .filter(Boolean)
      .join(" "),
  );

  function staleTitle(since: string): string {
    const at = new Date(since);
    const when = Number.isNaN(at.getTime()) ? "before the server restarted" : at.toLocaleString();
    return `Last reported ${when}; not confirmed since the server restarted.`;
  }

  // The face is drawn at its natural size and the host scales it to fill the
  // cell, so a button without a label grows its icon into the space.
  let tileEl = $state<HTMLElement | null>(null);
  let faceEl = $state<HTMLElement | null>(null);

  $effect(() => {
    if (!tileEl || !faceEl) return;
    const cleanup = bridge()?.autoScale?.(tileEl, faceEl, { padding: 6, maxScale: 8 });
    return () => cleanup?.();
  });
</script>

{#snippet face()}
  {#if !bound}
    <span class="hint">Pick a source in the widget settings.</span>
  {:else}
    {#if icon.text}
      <span class="icon" class:glyph={icon.glyph} style:font-family={icon.glyph ? fontStack : null}
            style:color={iconColor}>{icon.text}</span>
    {/if}
    {#if cfg.showLabel && label}
      <span class="label" title={label}>{label}</span>
    {/if}
    {#if caption}
      <span class="value">{caption}</span>
    {:else if error}
      <span class="error" title={error}>{error}</span>
    {:else if cfg.showValue && valueText}
      <span class="value">{valueText}</span>
    {/if}
    {#if staleSince && !error}
      <span class="stale" title={staleTitle(staleSince)}>last known</span>
    {/if}
  {/if}
{/snippet}

{#if action}
  <button type="button" class={tileClass} style={lookStyle} title={error || null} onclick={press} bind:this={tileEl}>
    <div class="face" bind:this={faceEl}>{@render face()}</div>
  </button>
{:else}
  <div class={tileClass} style={lookStyle} title={error || null} bind:this={tileEl}>
    <div class="face" bind:this={faceEl}>{@render face()}</div>
  </div>
{/if}

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  /* The button is the frame and the face inside it scales, as in Entity
     Control, so the border stays crisp at any cell size. */
  .button {
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
      color 0.15s,
      transform 0.08s;
  }

  .face {
    position: absolute;
    top: 0;
    left: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.2rem;
  }

  .button.pressable {
    cursor: pointer;
    user-select: none;
    -webkit-tap-highlight-color: transparent;
  }

  .button.pressable:hover {
    border-color: var(--theme-accent, #89b4fa);
  }

  /* A press sinks the button a little, the feedback a finger expects before
     the source answers. */
  .button.pressable:active {
    transform: scale(0.97);
  }

  .button.on {
    border-color: var(--theme-accent, #89b4fa);
    color: var(--theme-accent, #89b4fa);
    background: color-mix(in srgb, var(--theme-accent, #89b4fa) 16%, transparent);
  }

  .button.armed {
    border-color: var(--theme-warning, #f9e2af);
    color: var(--theme-warning, #f9e2af);
  }

  .button.ok {
    border-color: var(--theme-success, #a6e3a1);
  }

  .button.failed {
    border-color: var(--theme-error, #f38ba8);
  }

  .button.busy {
    opacity: 0.6;
  }

  .icon {
    font-size: 3rem;
    line-height: 1;
  }

  .icon.glyph {
    font-weight: 900;
    font-synthesis: none;
    color: var(--theme-accent, #89b4fa);
  }

  .button.on .icon.glyph {
    color: inherit;
  }

  .label {
    max-width: 12ch;
    font-size: 0.95rem;
    font-weight: 500;
    line-height: 1.2;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .value {
    max-width: 12ch;
    font-size: 0.8rem;
    opacity: 0.75;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .error {
    max-width: 14ch;
    font-size: 0.7rem;
    color: var(--theme-error, #f38ba8);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .stale {
    font-size: 0.65rem;
    opacity: 0.55;
  }

  .hint {
    max-width: 16ch;
    font-size: 0.75rem;
    opacity: 0.6;
  }
</style>
