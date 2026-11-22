import { loadControlKinds, resolveDevice, type EntitySource } from "../../shared/entitySource";

// conditionText reduces a source's value to the word a condition key is
// matched against. A composite value has no single word, so it matches none.
function conditionText(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return "";
}

function list(res: any): any[] {
  return Array.isArray(res) ? res : Array.isArray(res?.data) ? res.data : [];
}

/**
 * watchWeather follows the current value of a weather binding and calls apply
 * with it as text: a Home Assistant entity's raw state such as "rainy", or a
 * control's or device's value. It reads the source once and then its live
 * topic, the same way the entity widgets do. The returned function stops it;
 * a read that lands after that is ignored.
 */
export function watchWeather(src: EntitySource | undefined, apply: (value: string) => void): () => void {
  const bridge = (window as any).__karakuri;
  if (!bridge || !src) return () => {};

  let closed = false;
  let off: (() => void) | undefined;
  const set = (value: unknown): void => {
    if (!closed) apply(conditionText(value));
  };

  void (async () => {
    try {
      switch (src.source) {
        case "ha": {
          if (!src.connectionId || !src.entityId) return;
          const states = list(await bridge.api.get(`/connections/${src.connectionId}/ha/states`));
          if (closed) return;
          set(states.find((s) => s?.entity_id === src.entityId)?.state);
          off = bridge.subscribeTopic(`ha:${src.connectionId}:state:${src.entityId}`, (data: any) => {
            if (data?.new_state) set(data.new_state.state);
          });
          break;
        }
        case "control": {
          if (!src.controlId) return;
          const controls = list(await bridge.api.get("/controls"));
          if (closed) return;
          set(controls.find((c) => c?.id === src.controlId)?.value);
          off = bridge.subscribeTopic(`control:${src.controlId}`, (data: any) => {
            if (data?.deleted) set("");
            else if (data?.control) set(data.control.value);
          });
          break;
        }
        case "mqtt": {
          if (!src.deviceId) return;
          // A device's value is shaped by its control kind, so the catalog is
          // read before the first value is.
          await loadControlKinds();
          const devices = list(await bridge.api.get("/mqtt_devices"));
          if (closed) return;
          const device = devices.find((d) => d?.id === src.deviceId);
          if (!device) return;
          set(resolveDevice(device, device.state, src).value);
          off = bridge.subscribeTopic(`mqttdevice:${src.deviceId}`, (data: any) => {
            if (data?.state) set(resolveDevice(device, data.state, src).value);
          });
          break;
        }
      }
    } catch {
      // An unreadable source selects no condition; the other lists still show.
    }
    if (closed) off?.();
  })();

  return () => {
    closed = true;
    off?.();
  };
}
