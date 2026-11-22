import { writable, get } from 'svelte/store';
import { api } from '../api/client';

// An MQTT device is a device described by hand, because MQTT has no discovery:
// the topics it speaks on and the script that reads them. The settings panel
// defines them and dashboard rows bind to the values their decoders produce.
export interface MQTTDevice {
  id: string;
  name: string;
  connection_id: string;
  topics: string[];
  /** Reads one message into the values it carried. */
  state_script: string;
  /** Control kind the device is drawn with, from the same catalog a control picks from. */
  control: string;
  /** Settings of that kind: a slider's bounds, a remote's buttons, a light's features. */
  options: Record<string, unknown>;
  /** Turns one control change into {topic, payload}. */
  payload_script: string;
  description: string;
  /** Whether the device has a payload script, which is the whole of whether it can be commanded. */
  commandable?: boolean;
  // state is what the decoder has accumulated, absent when the device is not
  // running — a connection that is down, or a server that just started.
  state?: Record<string, unknown>;
  /** Why the last message failed to decode, when it did. */
  state_error?: string;
  /** Whether the device is subscribed at all, which an empty state cannot say. */
  running?: boolean;
  /** When the last message arrived on its topics, ignored messages included. */
  last_message?: string;
  /** Whether the state was restored from before a restart and not yet confirmed by the device. */
  stale?: boolean;
  /** When the device last reported a value. */
  reported_at?: string;
  /** Topic carrying online and offline payloads; empty for none. */
  availability_topic?: string;
  availability_online?: string;
  availability_offline?: string;
  /** Offline after this many seconds with no message; 0 is off. */
  expect_every_s?: number;
  /** Whether commands are sent while the device is offline. */
  offline_commands?: boolean;
  /** QoS of the state and availability subscriptions, and what the broker granted when lower. */
  state_qos?: number;
  state_qos_granted?: number;
  /** QoS and retain flag commands are published with. */
  command_qos?: number;
  command_retain?: boolean;
  /** Script time limits in milliseconds; 0 or absent is the default. */
  state_timeout_ms?: number;
  payload_timeout_ms?: number;
  /** online, offline, or unknown, with what decided it and when it changed. */
  availability?: string;
  availability_source?: string;
  availability_at?: string;
  /** Last availability payload that matched neither setting. */
  availability_note?: string;
  /** The Zigbee2MQTT device this was added from. */
  z2m_ieee?: string;
  z2m_base?: string;
  /** Whether the device is absent from its source's device or entity list. */
  missing?: boolean;
  /** Whether the connection's auto import created the device. */
  auto_imported?: boolean;
  /** When the device's source last reported it. */
  last_reported_at?: string;
  /** Home Assistant MQTT discovery identity the device was added from. */
  discovery_key?: string;
  /** mqtt, or esphome for an entity of an ESPHome node. */
  transport?: string;
  /** ESPHome object ID and entity type, for esphome devices. */
  entity_key?: string;
  entity_type?: string;
}

interface MQTTDevicesState {
  loaded: boolean;
  devices: MQTTDevice[];
}

function createMQTTDeviceStore() {
  const store = writable<MQTTDevicesState>({ loaded: false, devices: [] });
  let loadPromise: Promise<void> | null = null;

  async function refresh(): Promise<MQTTDevice[]> {
    const list = (await api.mqttDevices.list<MQTTDevice[]>()) || [];
    store.update((s) => ({ ...s, loaded: true, devices: list }));
    return list;
  }

  return {
    subscribe: store.subscribe,

    // load fetches the devices once; concurrent calls share one request.
    async load() {
      if (loadPromise) return loadPromise;
      loadPromise = refresh()
        .then(() => undefined)
        .catch((err) => {
          loadPromise = null;
          throw err;
        });
      return loadPromise;
    },

    refresh,

    /** byId returns a cached device, for a picker resolving a stored ID. */
    byId(id: string): MQTTDevice | undefined {
      return get(store).devices.find((d) => d.id === id);
    },

    // applyState folds a live decode from the broker into the cached list. The
    // message's arrival is itself the last-heard time, and a clean decode
    // clears a standing error the way the runner does.
    applyState(id: string, state: Record<string, unknown>) {
      store.update((s) => ({
        ...s,
        devices: s.devices.map((d) =>
          d.id === id
            ? {
                ...d,
                state,
                state_error: undefined,
                running: true,
                stale: false,
                last_message: new Date().toISOString(),
                reported_at: new Date().toISOString(),
              }
            : d,
        ),
      }));
    },

    /** applyAvailability folds a live availability change into the cached list. */
    applyAvailability(id: string, availability: string, source: string | undefined, at: string | undefined) {
      store.update((s) => ({
        ...s,
        devices: s.devices.map((d) =>
          d.id === id
            ? { ...d, availability, availability_source: source ?? d.availability_source, availability_at: at ?? d.availability_at }
            : d,
        ),
      }));
    },

    /** applyError records a live decode failure the same way. */
    applyError(id: string, error: string) {
      store.update((s) => ({
        ...s,
        devices: s.devices.map((d) =>
          d.id === id
            ? { ...d, state_error: error, running: true, last_message: new Date().toISOString() }
            : d,
        ),
      }));
    },

    async create(body: Omit<MQTTDevice, 'id' | 'state'>): Promise<MQTTDevice> {
      const created = await api.mqttDevices.create<MQTTDevice>(
        body as unknown as Record<string, unknown>,
      );
      await refresh();
      return created;
    },

    async update(id: string, body: Omit<MQTTDevice, 'id' | 'state'>): Promise<MQTTDevice> {
      const updated = await api.mqttDevices.update<MQTTDevice>(
        id,
        body as unknown as Record<string, unknown>,
      );
      await refresh();
      return updated;
    },

    async remove(id: string): Promise<void> {
      await api.mqttDevices.remove(id);
      await refresh();
    },

    /** test runs one of a device's scripts against a sample. */
    test(body: Record<string, unknown>) {
      return api.mqttDevices.test<{
        value?: unknown;
        output?: string[];
        error?: string;
      }>(body);
    },
  };
}

export const mqttDevicesStore = createMQTTDeviceStore();
