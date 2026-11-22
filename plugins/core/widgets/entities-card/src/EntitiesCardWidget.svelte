<svelte:options customElement="entities-card-widget" />

<script lang="ts">
  import { onDestroy } from "svelte";
  import {
    BLANK_RESOLVED,
    commandRequest,
    displayOnly,
    haService,
    loadControlKinds,
    parseConfig,
    pressable,
    resolveControl,
    resolveDevice,
    applyDeviceMessage,
    resolveHA,
    triggerPresses,
    type Resolved,
    type Row,
    type TriggerPress,
  } from "./rows";
  import ControlFace from "../../shared/faces/ControlFace.svelte";
  import { iconFontStack, iconGlyph, loadIcons } from "../../shared/icons";
  import { matchState, safeColor, stateLabel } from "../../shared/appearance";

  let { config: configAttr = "{}" }: { config: string | Record<string, unknown> } = $props();

  let cfg = $derived(parseConfig(configAttr));

  // RowState is what a row currently shows: what its source turned out to be,
  // and whatever went wrong reaching it. Everything about how it draws comes
  // from `resolved`, because the source declared it.
  interface RowState {
    resolved: Resolved;
    error: string;
    busy: boolean;
    /** armed is the first tap of a confirm-first press, keyed by the button's value. */
    armed: string;
    /** flash reports the result of a press for a moment after it settles. */
    flash: "" | "ok" | "error";
    flashed: string;
    /** staleSince is when a restored device value was last reported, empty once the device confirms it. */
    staleSince: string;
  }

  const BLANK: RowState = {
    resolved: BLANK_RESOLVED,
    error: "",
    busy: false,
    armed: "",
    flash: "",
    flashed: "",
    staleSince: "",
  };

  let states = $state<Record<string, RowState>>({});
  // catalogVersion re-renders the faces once the kind catalog lands, since a
  // light drawn before it arrives has no attributes to draw.
  let catalogVersion = $state(0);
  let unsubscribes: Array<() => void> = [];
  // bindGeneration tells a bind that outlived its cleanup apart from the live
  // one. A widget navigated away from while it is still loading would
  // otherwise finish binding into a component that is gone, and those topic
  // subscriptions would never be released.
  let bindGeneration = 0;
  function track(generation: number, off: (() => void) | undefined): void {
    if (!off) return;
    if (generation !== bindGeneration) {
      off();
      return;
    }
    unsubscribes.push(off);
  }
  let flashTimers: Record<string, ReturnType<typeof setTimeout>> = {};

  const bridge = () => (window as any).__karakuri;

  // The host's icon pack, read once: a row draws from the same set the rest of
  // the UI does.
  const icons = loadIcons();
  const fontStack = iconFontStack();

  // Re-bind whenever the rows change. The signature is the row list itself, so
  // editing a row's device re-subscribes and renaming the card does not.
  let rowsKey = $derived(JSON.stringify(cfg.rows));
  $effect(() => {
    const key = rowsKey;
    void key;
    void bind();
    return () => {
      bindGeneration += 1;
      for (const off of unsubscribes) off();
      unsubscribes = [];
    };
  });

  onDestroy(() => {
    bindGeneration += 1;
    for (const off of unsubscribes) off();
    unsubscribes = [];
    for (const timer of Object.values(flashTimers)) clearTimeout(timer);
  });

  // staleTitle explains a restored value: what the device last said, and that
  // it has not said anything since the server started.
  function staleTitle(since: string): string {
    const at = new Date(since);
    const when = Number.isNaN(at.getTime()) ? "before the server restarted" : at.toLocaleString();
    return `Last reported ${when}; not confirmed since the server restarted.`;
  }

  function patch(rowId: string, next: Partial<RowState>): void {
    states[rowId] = { ...(states[rowId] ?? BLANK), ...next };
  }

  function resolve(rowId: string, resolved: Resolved): void {
    patch(rowId, { resolved, error: "" });
  }

  // bind loads each row's current value and subscribes it to live updates.
  // Sources are gathered per connection rather than per row, so a card of ten
  // Home Assistant entities makes one request rather than ten.
  async function bind(): Promise<void> {
    const generation = ++bindGeneration;
    const rows = cfg.rows;
    const next: Record<string, RowState> = {};
    for (const row of rows) next[row.id] = { ...BLANK };
    states = next;

    // The kind catalog decides what every face draws, so it is fetched before
    // anything is resolved. One request serves every widget on the page.
    await loadControlKinds();
    if (generation !== bindGeneration) return;
    catalogVersion += 1;

    const controlRows = rows.filter((r) => r.source === "control" && r.controlId);
    const haRows = rows.filter((r) => r.source === "ha" && r.connectionId && r.entityId);
    const deviceRows = rows.filter((r) => r.source === "mqtt" && r.deviceId);

    if (controlRows.length > 0) await bindControls(controlRows, generation);
    if (haRows.length > 0) await bindHA(haRows, generation);
    if (deviceRows.length > 0) await bindDevices(deviceRows, generation);

    // A flow trigger has nothing to read: its buttons are declared on the row,
    // so it resolves without asking the host anything.
    for (const row of rows.filter((r) => r.source === "flow-trigger")) {
      resolve(row.id, triggerFace(row));
    }
    for (const row of rows.filter((r) => r.source === "mqtt" && !r.deviceId)) {
      patch(row.id, { error: "no device selected" });
    }
  }

  // triggerFace draws a flow-trigger row as the buttons control it is: a set of
  // actions with no state behind them, which is exactly what a trigger is.
  function triggerFace(row: Row): Resolved {
    const presses = triggerPresses(row);
    return {
      kind: "buttons",
      options: {
        buttons: presses.map((p) => ({ value: p.id, label: p.label || p.trigger || "Run" })),
      },
      readOnly: false,
      name: row.name || presses[0]?.label || presses[0]?.trigger || "Run",
      value: null,
    };
  }

  async function bindControls(rows: Row[], generation: number): Promise<void> {
    let list: any[] = [];
    try {
      const res = await bridge().api.get("/controls");
      list = Array.isArray(res) ? res : (res?.data ?? []);
    } catch (e: any) {
      for (const row of rows) patch(row.id, { error: e?.message || "cannot read controls" });
      return;
    }

    for (const row of rows) {
      const control = list.find((c) => c.id === row.controlId);
      if (!control) {
        patch(row.id, { error: "control not found" });
        continue;
      }
      resolve(row.id, resolveControl(control, row));
      const off = bridge()?.subscribeTopic(`control:${row.controlId}`, (data: any) => {
        // A deletion is announced on the control's own topic; the row says the
        // control is gone rather than drawing its last value forever.
        if (data?.deleted) {
          patch(row.id, { error: "control deleted", resolved: BLANK_RESOLVED });
          return;
        }
        if (data?.control) resolve(row.id, resolveControl(data.control, row));
      });
      track(generation, off);
    }
  }

  async function bindHA(rows: Row[], generation: number): Promise<void> {
    const connections = [...new Set(rows.map((r) => r.connectionId!))];
    for (const conn of connections) {
      const mine = rows.filter((r) => r.connectionId === conn);
      let list: any[] = [];
      try {
        const res = await bridge().api.get(`/connections/${conn}/ha/states`);
        list = Array.isArray(res) ? res : (res?.data ?? []);
      } catch (e: any) {
        for (const row of mine) patch(row.id, { error: e?.message || "cannot read states" });
        continue;
      }

      for (const row of mine) {
        const entity = list.find((s) => s.entity_id === row.entityId);
        if (entity) resolve(row.id, resolveHA(entity, row));
        else patch(row.id, { error: "entity not found" });

        const off = bridge()?.subscribeTopic(`ha:${conn}:state:${row.entityId}`, (data: any) => {
          if (data?.new_state) resolve(row.id, resolveHA(data.new_state, row));
        });
        track(generation, off);
      }
    }
  }

  // A device row reads a value the server already decoded. The device holds its
  // state whether or not anyone is watching, so the current value is fetched
  // once and the topic carries every change after that, which is what makes a
  // device that only reports what changed readable from a dashboard opened
  // halfway through the day.
  async function bindDevices(rows: Row[], generation: number): Promise<void> {
    let list: any[] = [];
    try {
      const res = await bridge().api.get("/mqtt_devices");
      list = Array.isArray(res) ? res : (res?.data ?? []);
    } catch (e: any) {
      for (const row of rows) patch(row.id, { error: e?.message || "cannot read devices" });
      return;
    }

    for (const row of rows) {
      const device = list.find((d) => d.id === row.deviceId);
      if (!device) {
        patch(row.id, { error: "device not found" });
        continue;
      }
      resolve(row.id, resolveDevice(device, device.state, row));
      // A value restored from before a server restart is shown, but marked,
      // until the device reports again.
      patch(row.id, { staleSince: device.stale ? String(device.reported_at ?? "") || "unknown" : "" });
      const off = bridge()?.subscribeTopic(`mqttdevice:${row.deviceId}`, (data: any) => {
        if (data?.error) {
          patch(row.id, { error: String(data.error) });
          return;
        }
        if (applyDeviceMessage(device, data)) patch(row.id, { staleSince: "" });
        resolve(row.id, resolveDevice(device, device.state, row));
      });
      track(generation, off);
    }
  }

  // ---- Interaction ----

  function flash(row: Row, result: "ok" | "error", key: string): void {
    patch(row.id, { flash: result, flashed: key, busy: false });
    clearTimeout(flashTimers[row.id]);
    flashTimers[row.id] = setTimeout(() => patch(row.id, { flash: "", flashed: "" }), 2000);
  }

  async function act(row: Row, run: () => Promise<void>): Promise<void> {
    if (states[row.id]?.busy) return;
    patch(row.id, { busy: true });
    try {
      await run();
      patch(row.id, { busy: false, error: "" });
    } catch (e: any) {
      patch(row.id, { busy: false, error: e?.message || "failed" });
      flash(row, "error", "");
    }
  }

  // command is the one path an interaction takes. The face says which attribute
  // moved and what to; where that goes is the source's business.
  function command(row: Row, attribute: string, value: unknown): Promise<void> {
    const st = states[row.id] ?? BLANK;

    if (row.source === "flow-trigger") {
      return fireTrigger(row, String(value));
    }

    return act(row, async () => {
      switch (row.source) {
        case "control":
          await bridge().api.post(`/controls/${row.controlId}/set`, { attribute, value });
          return;
        case "ha": {
          const call = haService(row, st.resolved, attribute, value);
          if (!call) throw new Error("nothing to call");
          await bridge().api.post(`/connections/${row.connectionId}/ha/service`, {
            domain: call.domain,
            service: call.service,
            target: { entity_id: row.entityId },
            service_data: call.data,
          });
          return;
        }
        case "mqtt": {
          if (!row.deviceId) throw new Error("row has no device");
          const previous = st.resolved.value;
          await bridge().api.post(
            `/mqtt_devices/${row.deviceId}/command`,
            commandRequest(st.resolved, attribute, value, previous),
          );
          return;
        }
      }
    });
  }

  // A confirm-first press arms on the first tap and fires on the second, so a
  // wall tablet cannot start something destructive on a stray touch.
  async function fireTrigger(row: Row, pressId: string): Promise<void> {
    const press = triggerPresses(row).find((p) => p.id === pressId);
    if (!press) return;
    if (!press.flowId || !press.trigger) {
      // A button still being configured names nothing to start. Saying so is
      // clearer than the server's generic refusal for an empty flow id.
      patch(row.id, { error: "this button names no trigger yet" });
      return;
    }

    if (press.confirmFirst && states[row.id]?.armed !== pressId) {
      patch(row.id, { armed: pressId });
      setTimeout(() => {
        if (states[row.id]?.armed === pressId) patch(row.id, { armed: "" });
      }, 3000);
      return;
    }
    patch(row.id, { armed: "" });

    await act(row, async () => {
      const body: Record<string, unknown> = { name: press.trigger };
      const params = await triggerPayload(press);
      if (params !== undefined) body.payload = params;
      await bridge().api.post(
        `/widget_triggers/${encodeURIComponent(press.flowId || "")}/fire`,
        body,
      );
      flash(row, "ok", pressId);
    });
  }

  // Parameters are stored as the document the author wrote, so they are read
  // back through the host in the format it was written in.
  async function triggerPayload(press: TriggerPress): Promise<Record<string, unknown> | undefined> {
    if (!press.payload || !press.payload.trim()) return undefined;
    const parsed = await bridge().parseDocument(press.payload, press.payloadFormat || "json");
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error("parameters must be an object");
    }
    return parsed as Record<string, unknown>;
  }

  // ---- Rendering helpers ----

  function rowLabel(row: Row): string {
    return states[row.id]?.resolved.name || row.name || "—";
  }

  // rowLook is the first of a row's states its reading matches; none matches
  // before the source answers or while it reports an error.
  function rowLook(row: Row, st: RowState) {
    if (st.error) return null;
    return matchState(row.states, st.resolved, st.resolved !== BLANK_RESOLVED);
  }

  function lookStyle(look: ReturnType<typeof rowLook>): string | null {
    const out = [
      safeColor(look?.background) ? `background:${safeColor(look?.background)}` : "",
      safeColor(look?.color) ? `color:${safeColor(look?.color)}` : "",
    ].filter(Boolean);
    return out.length ? out.join(";") : null;
  }

  // The empty string means nothing is armed and nothing has flashed, so it must
  // never match a button: the lone unnamed action carries an empty value.
  function buttonState(row: Row) {
    return (value: string): "" | "armed" | "ok" | "error" => {
      const st = states[row.id] ?? BLANK;
      if (st.armed && st.armed === value) return "armed";
      if (st.flash && st.flashed && st.flashed === value) return st.flash;
      return "";
    };
  }
</script>

<div class="card">
  {#if cfg.title}
    <div class="card-title">{cfg.title}</div>
  {/if}

  {#if cfg.rows.length === 0}
    <div class="empty">No rows yet. Add one in the widget settings.</div>
  {:else}
    <div class="rows">
      {#each cfg.rows as row (row.id)}
        {@const st = states[row.id] ?? BLANK}
        <!-- The pack's classes do not cross into a shadow root, but its
             @font-face does, so the glyph is drawn as a character. A value
             that is not an icon name is drawn as the text it is. -->
        {@const look = rowLook(row, st)}
        {@const icon = iconGlyph(look?.icon || row.icon || "", icons)}
        {@const name = look?.label ? stateLabel(look.label, st.resolved, rowLabel(row)) : rowLabel(row)}
        <div class="row" class:busy={st.busy} class:tinted={!!safeColor(look?.background)} style={lookStyle(look)}>
          <span class="row-icon" class:glyph={icon.glyph}
                style:font-family={icon.glyph ? fontStack : null}
                style:color={safeColor(look?.iconColor) || null}>{icon.text}</span>
          <span class="row-name" title={name}>{name}</span>
          {#if st.staleSince}
            <span class="row-stale" title={staleTitle(st.staleSince)}>last known</span>
          {/if}

          <span class="row-control">
            <!-- The error rides beside the control rather than replacing it:
                 a failed toggle that removed the switch also removed the way
                 to try again. Only a row with nothing resolved yet shows the
                 error alone. -->
            {#if st.error && st.resolved === BLANK_RESOLVED}
              <span class="row-error" title={st.error}>{st.error}</span>
            {:else}
              {#if st.error}
                <span class="row-error" title={st.error}>{st.error}</span>
              {/if}
              {#key catalogVersion}
                <ControlFace
                  resolved={st.resolved}
                  readOnly={!pressable(row, st.resolved) || displayOnly(row)}
                  busy={st.busy}
                  compact={true}
                  buttonState={buttonState(row)}
                  command={(attribute, value) => command(row, attribute, value)}
                />
              {/key}
            {/if}
          </span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  :host {
    display: block;
    width: 100%;
    height: 100%;
    font-family: var(--theme-font-family, "Inter", sans-serif);
    box-sizing: border-box;
  }

  .card {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    padding: 0.75rem 0.9rem;
    box-sizing: border-box;
    border: 1px solid var(--tile-border, var(--theme-border, var(--theme-surface1, #45475a)));
    border-radius: var(--theme-radius-md, 8px);
    background: var(--theme-widget-bg, transparent);
    color: var(--theme-text, #cdd6f4);
    overflow: hidden;
  }

  .card-title {
    font-size: 1.1rem;
    font-weight: 600;
    margin-bottom: 0.5rem;
  }

  .rows {
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    flex: 1;
    min-height: 0;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.35rem 0;
    min-height: 2rem;
  }

  .row.busy {
    opacity: 0.6;
  }

  /* A state's background reads as a band, so the row keeps its text off the
     band's edge. */
  .row.tinted {
    margin: 0 -0.4rem;
    padding-left: 0.4rem;
    padding-right: 0.4rem;
    border-radius: var(--theme-radius-sm, 4px);
  }

  .row-icon {
    width: 1.4rem;
    flex-shrink: 0;
    font-size: 1.15rem;
    color: var(--theme-accent, #89b4fa);
    text-align: center;
  }

  /* A pack glyph is a character in an icon font: no synthetic bold, or the
     brand face — which ships only one weight — comes out smeared. */
  .row-icon.glyph {
    font-weight: 900;
    font-synthesis: none;
  }

  .row-name {
    flex: 1;
    min-width: 0;
    font-size: 0.9rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .row-control {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.4rem;
  }

  .row-stale {
    font-size: 0.7rem;
    opacity: 0.55;
    white-space: nowrap;
  }

  .row-error {
    font-size: 0.75rem;
    color: var(--theme-error, #f38ba8);
    max-width: 9rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .empty {
    font-size: 0.85rem;
    opacity: 0.6;
    padding: 0.5rem 0;
  }

</style>
