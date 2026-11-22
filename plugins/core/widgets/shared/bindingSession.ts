// A binding session is one widget's live link to its source: the first read,
// the topic that keeps it current, and the request a press becomes. Entity
// Control and Button draw the same binding differently, so they share this
// and keep only their drawing and press feedback.

import {
  BLANK_RESOLVED,
  applyDeviceMessage,
  commandRequest,
  haService,
  loadControlKinds,
  resolveControl,
  resolveDevice,
  resolveHA,
  triggerPresses,
  type EntitySource,
  type Resolved,
  type TriggerPress,
} from "./entitySource";

const bridge = () => (window as any).__karakuri;

/** SessionEvents are what a session reports as its source answers. */
export interface SessionEvents {
  /** resolved is the source's current reading. */
  resolved: (r: Resolved) => void;
  /** error is a failure to read, or an empty string once the source recovers. */
  error: (message: string) => void;
  /** stale is when a restored device value was last reported, or empty once it is confirmed. */
  stale: (since: string) => void;
  /** catalog fires once the control kind catalog has loaded. */
  catalog?: () => void;
}

/**
 * triggerFace draws a flow-trigger binding as the buttons control it is: a
 * set of actions with no state behind them, which is what a trigger is.
 */
export function triggerFace(cfg: EntitySource): Resolved {
  const presses = triggerPresses(cfg);
  return {
    kind: "buttons",
    options: {
      buttons: presses.map((p) => ({ value: p.id, label: p.label || p.trigger || "Run" })),
    },
    readOnly: false,
    name: cfg.name || presses[0]?.label || presses[0]?.trigger || "Run",
    value: null,
  };
}

function list(res: any): any[] {
  return Array.isArray(res) ? res : (res?.data ?? []);
}

/**
 * bindSource reads a binding and follows it, reporting through events. The
 * returned function releases every subscription, including one made after it
 * is called by a read still in flight.
 */
export function bindSource(cfg: EntitySource, events: SessionEvents): () => void {
  let released = false;
  const offs: Array<() => void> = [];
  const track = (off: (() => void) | undefined) => {
    if (!off) return;
    if (released) off();
    else offs.push(off);
  };

  void (async () => {
    events.resolved(BLANK_RESOLVED);
    events.error("");
    events.stale("");
    // The kind catalog decides what the face draws, so it is fetched before
    // anything is resolved. One request serves every widget on the page.
    await loadControlKinds();
    if (released) return;
    events.catalog?.();

    switch (cfg.source) {
      case "control": {
        if (!cfg.controlId) return;
        let controls: any[];
        try {
          controls = list(await bridge().api.get("/controls"));
        } catch (e: any) {
          if (!released) events.error(e?.message || "cannot read controls");
          return;
        }
        if (released) return;
        const control = controls.find((c) => c.id === cfg.controlId);
        if (!control) {
          events.error("control not found");
          return;
        }
        events.resolved(resolveControl(control, cfg));
        track(
          bridge()?.subscribeTopic(`control:${cfg.controlId}`, (data: any) => {
            // A deletion is announced on the control's own topic; the widget
            // says the control is gone rather than drawing its last value.
            if (data?.deleted) {
              events.error("control deleted");
              events.resolved(BLANK_RESOLVED);
              return;
            }
            if (data?.control) events.resolved(resolveControl(data.control, cfg));
          }),
        );
        return;
      }
      case "ha": {
        if (!cfg.connectionId || !cfg.entityId) return;
        let entities: any[];
        try {
          entities = list(await bridge().api.get(`/connections/${cfg.connectionId}/ha/states`));
        } catch (e: any) {
          if (!released) events.error(e?.message || "cannot read states");
          return;
        }
        if (released) return;
        const entity = entities.find((s) => s.entity_id === cfg.entityId);
        if (entity) events.resolved(resolveHA(entity, cfg));
        else events.error("entity not found");
        track(
          bridge()?.subscribeTopic(`ha:${cfg.connectionId}:state:${cfg.entityId}`, (data: any) => {
            if (data?.new_state) events.resolved(resolveHA(data.new_state, cfg));
          }),
        );
        return;
      }
      case "mqtt": {
        if (!cfg.deviceId) {
          events.error("no device selected");
          return;
        }
        let devices: any[];
        try {
          devices = list(await bridge().api.get("/mqtt_devices"));
        } catch (e: any) {
          if (!released) events.error(e?.message || "cannot read devices");
          return;
        }
        if (released) return;
        const device = devices.find((d) => d.id === cfg.deviceId);
        if (!device) {
          events.error("device not found");
          return;
        }
        events.resolved(resolveDevice(device, device.state, cfg));
        events.stale(device.stale ? String(device.reported_at ?? "") || "unknown" : "");
        track(
          bridge()?.subscribeTopic(`mqttdevice:${cfg.deviceId}`, (data: any) => {
            if (data?.error) {
              events.error(String(data.error));
              return;
            }
            // A good state clears a standing decode error; leaving it up made
            // a recovered device look broken until the page reloaded.
            events.error("");
            if (applyDeviceMessage(device, data)) events.stale("");
            events.resolved(resolveDevice(device, device.state, cfg));
          }),
        );
        return;
      }
      case "flow-trigger":
        events.resolved(triggerFace(cfg));
        return;
    }
  })();

  return () => {
    released = true;
    for (const off of offs) off();
    offs.length = 0;
  };
}

/**
 * sendCommand sends one attribute change to a control, Home Assistant, or
 * MQTT binding. It throws with the reason when the source refuses.
 */
export async function sendCommand(
  cfg: EntitySource,
  resolved: Resolved,
  attribute: string,
  value: unknown,
): Promise<void> {
  switch (cfg.source) {
    case "control":
      await bridge().api.post(`/controls/${cfg.controlId}/set`, { attribute, value });
      return;
    case "ha": {
      const call = haService(cfg, resolved, attribute, value);
      if (!call) throw new Error("nothing to call");
      await bridge().api.post(`/connections/${cfg.connectionId}/ha/service`, {
        domain: call.domain,
        service: call.service,
        target: { entity_id: cfg.entityId },
        service_data: call.data,
      });
      return;
    }
    case "mqtt":
      if (!cfg.deviceId) throw new Error("no device selected");
      await bridge().api.post(
        `/mqtt_devices/${cfg.deviceId}/command`,
        commandRequest(resolved, attribute, value, resolved.value),
      );
      return;
  }
  throw new Error("this binding takes no commands");
}

/**
 * firePress starts the flow a trigger press names with its parameters. It
 * throws when the press names nothing yet or the flow refuses.
 */
export async function firePress(press: TriggerPress): Promise<void> {
  if (!press.flowId || !press.trigger) {
    // A button still being configured names nothing to start. Saying so is
    // clearer than the server's generic refusal for an empty flow id.
    throw new Error("this button names no trigger yet");
  }
  const body: Record<string, unknown> = { name: press.trigger };
  const params = await triggerPayload(press);
  if (params !== undefined) body.payload = params;
  await bridge().api.post(`/widget_triggers/${encodeURIComponent(press.flowId)}/fire`, body);
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
