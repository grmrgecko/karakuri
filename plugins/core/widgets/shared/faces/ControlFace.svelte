<script lang="ts">
  import SwitchInput from "./SwitchInput.svelte";
  import SliderInput from "./SliderInput.svelte";
  import ActionButton from "./ActionButton.svelte";
  import PickerInput from "./PickerInput.svelte";
  import ColorInput from "./ColorInput.svelte";
  import TextInput from "./TextInput.svelte";
  import LocationFace from "./LocationFace.svelte";
  import MediaFace from "./MediaFace.svelte";
  import {
    attributesOf,
    attributeValue,
    choices,
    formatValue,
    hasFeature,
    numberOption,
    textOption,
    truthy,
  } from "../controlKinds";
  import type { Resolved } from "../entitySource";

  // Every kind is drawn here, once, for both widgets. A card row and a tile
  // differ in how much room they have, not in what a light is, so the only
  // thing that varies is `compact`: a row shows the control and its most
  // useful second attribute, a tile shows everything the source declared.
  let {
    resolved,
    readOnly = false,
    busy = false,
    compact = false,
    buttonState,
    command,
  }: {
    resolved: Resolved;
    readOnly?: boolean;
    busy?: boolean;
    compact?: boolean;
    buttonState?: (value: string) => "" | "armed" | "ok" | "error";
    command?: (attribute: string, value: unknown) => void | Promise<void>;
  } = $props();

  let kind = $derived(resolved.kind);
  let options = $derived(resolved.options ?? {});
  let attributes = $derived(attributesOf(kind, options));

  function has(id: string): boolean {
    return attributes.some((a) => a.id === id);
  }

  function part(id: string): unknown {
    return attributeValue(kind, resolved.value, id);
  }

  function num(id: string, fallback: number): number {
    const raw = part(id);
    const n = typeof raw === "number" ? raw : Number(raw);
    return Number.isFinite(n) ? n : fallback;
  }

  function send(attribute: string, value: unknown): void | Promise<void> {
    if (readOnly) return;
    return command?.(attribute, value);
  }

  function stateOf(value: string): "" | "armed" | "ok" | "error" {
    return buttonState?.(value) ?? "";
  }

  // buttonChoices is what a Buttons control draws. With nothing declared the
  // binding is itself the one action, which is what a scene or a bare command
  // topic is.
  let buttonChoices = $derived(choices(options, "buttons"));

  let unit = $derived(textOption(options, "unit"));

  // A datetime control stores what the kind coerces — a bare date, a bare
  // time, or RFC 3339 — while the input element speaks local time with no
  // zone. These two convert at the edge, the same bargain brightness makes.
  function datetimeShown(mode: string, stored: string): string {
    if (!stored) return "";
    if (mode === "date" || mode === "time") return stored.slice(0, mode === "time" ? 5 : 10);
    const d = new Date(stored);
    if (Number.isNaN(d.getTime())) return "";
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  function datetimeStored(mode: string, local: string): string {
    if (!local) return "";
    if (mode === "date" || mode === "time") return local;
    const d = new Date(local);
    return Number.isNaN(d.getTime()) ? "" : d.toISOString();
  }
</script>

{#if resolved.unavailable}
  <span class="reading unavailable">Unavailable</span>
{:else}

{#if kind === "boolean"}
  {#if readOnly}
    <span class="reading">{formatValue(truthy(resolved.value), options)}</span>
  {:else}
    <SwitchInput checked={truthy(part("on"))} disabled={busy}
                 onchange={(next) => send("on", next)} />
  {/if}

{:else if kind === "lock"}
  {@const locked = String(part("state") ?? "") === "locked"}
  <span class="group">
    <span class="reading">{formatValue(resolved.value, options)}</span>
    {#if !readOnly}
      <ActionButton
        label={locked ? "Unlock" : "Lock"}
        state={stateOf("lock")}
        disabled={busy}
        onpress={() => send("state", locked ? "unlocked" : "locked")}
      />
    {/if}
  </span>

{:else if kind === "buttons"}
  {#if readOnly}
    <span class="reading">{formatValue(resolved.value, options)}</span>
  {:else if buttonChoices.length === 0}
    <ActionButton label="Run" state={stateOf("")} disabled={busy}
                  onpress={() => send("value", true)} />
  {:else}
    <span class="group wrap">
      {#each buttonChoices as button (button.value)}
        <ActionButton
          label={button.label}
          state={stateOf(button.value)}
          disabled={busy}
          onpress={() => send("value", button.value)}
        />
      {/each}
    </span>
  {/if}

{:else if kind === "number"}
  {#if readOnly}
    <span class="reading">{formatValue(resolved.value, options)}</span>
  {:else if textOption(options, "display") === "box"}
    <span class="group">
      <input
        class="box"
        type="number"
        disabled={busy}
        min={numberOption(options, "min", 0)}
        max={numberOption(options, "max", 100)}
        step={numberOption(options, "step", 1)}
        value={num("value", numberOption(options, "min", 0))}
        onchange={(e) => send("value", Number(e.currentTarget.value))}
      />
      {#if unit}<span class="reading">{unit}</span>{/if}
    </span>
  {:else}
    <SliderInput
      value={num("value", numberOption(options, "min", 0))}
      min={numberOption(options, "min", 0)}
      max={numberOption(options, "max", 100)}
      step={numberOption(options, "step", 1)}
      unit={unit ? ` ${unit}` : ""}
      disabled={busy}
      oncommit={(next) => send("value", next)}
    />
  {/if}

{:else if kind === "select"}
  {#if readOnly}
    <span class="reading">{formatValue(resolved.value, options)}</span>
  {:else}
    <PickerInput
      value={String(part("value") ?? "")}
      options={choices(options, "options")}
      display={textOption(options, "display")}
      disabled={busy}
      onpick={(next) => send("value", next)}
    />
  {/if}

{:else if kind === "light"}
  <span class="group" class:stack={!compact}>
    {#if readOnly}
      <span class="reading">{formatValue(truthy(part("on")), options)}</span>
    {:else}
      <SwitchInput checked={truthy(part("on"))} disabled={busy}
                   onchange={(next) => send("on", next)} />
    {/if}
    {#if has("brightness")}
      <SliderInput
        value={num("brightness", 0)}
        min={0}
        max={100}
        step={1}
        unit="%"
        label={compact ? "" : "Brightness"}
        disabled={busy || readOnly}
        oncommit={(next) => send("brightness", next)}
      />
    {/if}
    {#if has("color") && (!compact || !has("brightness"))}
      <ColorInput value={String(part("color") ?? "")} disabled={busy || readOnly}
                  oncommit={(next) => send("color", next)} />
    {/if}
    {#if has("color_temp") && !compact}
      <SliderInput
        value={num("color_temp", numberOption(options, "min_kelvin", 2000))}
        min={numberOption(options, "min_kelvin", 2000)}
        max={numberOption(options, "max_kelvin", 6500)}
        step={50}
        unit="K"
        label="Warmth"
        disabled={busy || readOnly}
        oncommit={(next) => send("color_temp", next)}
      />
    {/if}
    {#if has("effect") && !compact}
      <PickerInput
        value={String(part("effect") ?? "")}
        options={choices(options, "effect_list")}
        disabled={busy || readOnly}
        onpick={(next) => send("effect", next)}
      />
    {/if}
  </span>

{:else if kind === "cover"}
  {@const state = String(part("state") ?? "")}
  <span class="group" class:stack={!compact}>
    <span class="group">
      <ActionButton label="Open" active={state === "open"} disabled={busy || readOnly}
                    onpress={() => send("state", "open")} />
      {#if hasFeature(options, "stop")}
        <ActionButton label="Stop" disabled={busy || readOnly}
                      onpress={() => send("state", "stopped")} />
      {/if}
      <ActionButton label="Close" active={state === "closed"} disabled={busy || readOnly}
                    onpress={() => send("state", "closed")} />
    </span>
    {#if has("position")}
      <SliderInput
        value={num("position", 0)}
        min={0}
        max={100}
        step={1}
        unit="%"
        label={compact ? "" : "Position"}
        disabled={busy || readOnly}
        oncommit={(next) => send("position", next)}
      />
    {/if}
    {#if has("tilt") && !compact}
      <SliderInput
        value={num("tilt", 0)}
        min={0}
        max={100}
        step={1}
        unit="%"
        label="Tilt"
        disabled={busy || readOnly}
        oncommit={(next) => send("tilt", next)}
      />
    {/if}
  </span>

{:else if kind === "climate"}
  <span class="group" class:stack={!compact}>
    <span class="reading">
      {formatValue(part("current"), { unit: textOption(options, "unit"), precision: 1 })}
      {#if part("action")}<span class="dim">· {part("action")}</span>{/if}
    </span>
    {#if has("target_low")}
      <!-- A range thermostat aims between two setpoints; a single target has
           no meaning to it, so the sliders replace the target rather than
           joining it. A row has no room for two sliders and draws the band. -->
      {#if compact}
        <span class="reading">
          {formatValue(part("target_low"), { unit: textOption(options, "unit") })}
          –
          {formatValue(part("target_high"), { unit: textOption(options, "unit") })}
        </span>
      {:else}
        <SliderInput
          value={num("target_low", numberOption(options, "min_temp", 7))}
          min={numberOption(options, "min_temp", 7)}
          max={numberOption(options, "max_temp", 35)}
          step={numberOption(options, "step", 0.5)}
          unit={textOption(options, "unit")}
          label="Low"
          disabled={busy || readOnly}
          oncommit={(next) => send("target_low", next)}
        />
        <SliderInput
          value={num("target_high", numberOption(options, "min_temp", 7))}
          min={numberOption(options, "min_temp", 7)}
          max={numberOption(options, "max_temp", 35)}
          step={numberOption(options, "step", 0.5)}
          unit={textOption(options, "unit")}
          label="High"
          disabled={busy || readOnly}
          oncommit={(next) => send("target_high", next)}
        />
      {/if}
    {:else}
      <SliderInput
        value={num("target", numberOption(options, "min_temp", 7))}
        min={numberOption(options, "min_temp", 7)}
        max={numberOption(options, "max_temp", 35)}
        step={numberOption(options, "step", 0.5)}
        unit={textOption(options, "unit")}
        label={compact ? "" : "Target"}
        disabled={busy || readOnly}
        oncommit={(next) => send("target", next)}
      />
    {/if}
    {#if !compact}
      <PickerInput
        value={String(part("mode") ?? "")}
        options={choices(options, "modes")}
        disabled={busy || readOnly}
        onpick={(next) => send("mode", next)}
      />
      {#if has("fan_mode")}
        <PickerInput
          value={String(part("fan_mode") ?? "")}
          options={choices(options, "fan_modes")}
          disabled={busy || readOnly}
          onpick={(next) => send("fan_mode", next)}
        />
      {/if}
    {/if}
  </span>

{:else if kind === "fan"}
  <span class="group" class:stack={!compact}>
    {#if readOnly}
      <span class="reading">{formatValue(truthy(part("on")), options)}</span>
    {:else}
      <SwitchInput checked={truthy(part("on"))} disabled={busy}
                   onchange={(next) => send("on", next)} />
    {/if}
    {#if has("percentage")}
      <SliderInput
        value={num("percentage", 0)}
        min={0}
        max={100}
        step={numberOption(options, "speed_step", 1)}
        unit="%"
        label={compact ? "" : "Speed"}
        disabled={busy || readOnly}
        oncommit={(next) => send("percentage", next)}
      />
    {/if}
    {#if has("preset") && !compact}
      <PickerInput
        value={String(part("preset") ?? "")}
        options={choices(options, "preset_list")}
        disabled={busy || readOnly}
        onpick={(next) => send("preset", next)}
      />
    {/if}
    {#if has("oscillating") && !compact}
      <span class="group">
        <span class="dim">Oscillate</span>
        <SwitchInput checked={truthy(part("oscillating"))} disabled={busy || readOnly}
                     onchange={(next) => send("oscillating", next)} />
      </span>
    {/if}
    {#if has("direction") && !compact}
      <PickerInput
        value={String(part("direction") ?? "")}
        options={[
          { value: "forward", label: "Forward" },
          { value: "reverse", label: "Reverse" },
        ]}
        display="segmented"
        disabled={busy || readOnly}
        onpick={(next) => send("direction", next)}
      />
    {/if}
  </span>

{:else if kind === "datetime"}
  {@const mode = textOption(options, "mode") || "datetime"}
  {#if readOnly}
    <span class="reading">{formatValue(resolved.value, options)}</span>
  {:else}
    <input
      class="box datetime"
      type={mode === "date" ? "date" : mode === "time" ? "time" : "datetime-local"}
      disabled={busy}
      value={datetimeShown(mode, String(part("value") ?? ""))}
      onchange={(e) => send("value", datetimeStored(mode, e.currentTarget.value))}
    />
  {/if}

{:else if kind === "location"}
  <LocationFace
    value={resolved.value}
    {options}
    {readOnly}
    {busy}
    {compact}
    onpick={(next) => send("state", next)}
  />

{:else if kind === "media"}
  <MediaFace
    value={resolved.value}
    {options}
    {readOnly}
    {busy}
    {compact}
    {send}
  />

{:else if readOnly}
  <span class="reading">{formatValue(resolved.value, options)}</span>

{:else}
  <TextInput
    value={String(part("value") ?? "")}
    multiline={options.multiline === true && !compact}
    disabled={busy}
    oncommit={(next) => send("value", next)}
  />
{/if}
{/if}

<style>
  .group {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    min-width: 0;
  }

  /* A tile has the height a card row does not, so a light there stacks its
     attributes rather than squeezing them onto one line. */
  .group.stack {
    flex-direction: column;
    align-items: stretch;
    gap: 0.35rem;
  }

  .group.wrap {
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .reading {
    font-size: 0.9rem;
    color: var(--theme-subtext0, var(--theme-text-muted, #a6adc8));
    white-space: nowrap;
  }

  .dim {
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .unavailable {
    font-style: italic;
    opacity: 0.6;
  }

  .box.datetime {
    width: auto;
  }

  .box {
    width: 5rem;
    padding: 0.15rem 0.35rem;
    border: 1px solid var(--theme-border, #45475a);
    border-radius: 4px;
    background: var(--theme-surface0, #313244);
    color: var(--theme-text, #cdd6f4);
    font-family: inherit;
    font-size: 0.8rem;
  }
</style>
