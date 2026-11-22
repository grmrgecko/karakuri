// A binding names a source; the source says what it is. A karakuri control and
// an MQTT device both declare a control kind where they are defined, a Home
// Assistant entity is derived into one here, and a flow trigger is a set of
// buttons the row itself declares. Nothing asks the author to pick a face,
// because three of the four already know and the fourth is configured where it
// is used.
//
// This directory is shared source, not a widget package: it has no
// package.json, so the build never treats it as one, and every widget package
// in this plugin depends on it.

import {
  attributesOf,
  attributeValue,
  choices,
  findKind,
  hasFeature,
  isComposite,
  numberOption,
  primaryAttribute,
  textOption,
  truthy,
  type Choice,
} from "./controlKinds";

import type { ValueFormat } from "./formats";
import type { StateLook } from "./appearance";

export { formatValue, loadControlKinds, attributesOf, attributeValue, truthy } from "./controlKinds";
export type { ValueFormat } from "./formats";
export type { Choice, ControlAttribute, ControlKindDef } from "./controlKinds";

export type SourceKind = "control" | "ha" | "mqtt" | "flow-trigger";

// TriggerPress is one button of a flow-trigger binding: which flow's named
// entry point it starts and what it sends. A row of them is On and Off side by
// side, which is the only place a widget declares its own buttons, because a
// trigger has no state anywhere else to declare them in.
export interface TriggerPress {
  id: string;
  label: string;
  icon?: string;
  flowId?: string;
  trigger?: string;
  payload?: string;
  payloadFormat?: "json" | "yaml";
  confirmFirst?: boolean;
}

// EntitySource is one binding: which source it reads, how it is labelled, and
// the fields that source needs. A widget adds whatever identity it needs of its
// own; a card's rows carry an ID, a single-control widget does not.
export interface EntitySource {
  source: SourceKind;
  /** Display name; empty falls back to the source's own name. */
  name?: string;
  /**
   * Icon shown before the name: a class name from the host's pack, e.g.
   * "la-home". Anything else is drawn as the text it is, which is what keeps
   * the emoji written before the picker existed working (see ./icons.ts).
   */
  icon?: string;
  /**
   * displayOnly is the author saying this shows a reading and nothing more.
   * It is the one thing about presentation the source cannot answer, and the
   * host reads the same flag to decide what a dashboard's viewers may press.
   */
  displayOnly?: boolean;

  // Control source.
  controlId?: string;

  // Home Assistant source.
  connectionId?: string;
  entityId?: string;

  // MQTT source. A binding is a link to a device and nothing else, exactly as
  // a control binding is a link to a control. The device owns the topics, how
  // its messages are read, what control it is drawn with, and how a command is
  // built, because those are properties of the device and a row that had to be
  // told them would be told differently on every dashboard.
  deviceId?: string;
  /**
   * deviceField shows one value the device reports beside its control rather
   * than the control itself: a battery level, a link quality. It is a reading,
   * so a row bound this way never presses anything.
   */
  deviceField?: string;
  /**
   * showCoordinates shows a location row's coordinates and accuracy even when
   * the source's own setting is off. It is the only switch a Home Assistant
   * person has, since an entity carries no kind options.
   */
  showCoordinates?: boolean;
  /** Display settings for a device field, which has no kind of its own to carry them. */
  fieldUnit?: string;
  fieldPrecision?: number;
  /**
   * format draws a numeric reading as a duration, a size, a rate, a percent,
   * or a number, wherever the binding's value is shown as text.
   */
  format?: ValueFormat;
  /** precision overrides the decimals the source reports a reading with. */
  precision?: number;
  /** states change the binding's icon, colors, and label by its reading; the first match applies. */
  states?: StateLook[];

  // Flow trigger source. A row may press several.
  triggers?: TriggerPress[];

  /**
   * control was the widget face an author picked before kinds were declared at
   * the source. Only "value" is still read, because that was how display-only
   * used to be spelled and a deployed snapshot cannot be rewritten.
   */
  control?: string;

  // Fields a row saved before triggers were a list. They are read back into one
  // press so an existing button keeps working.
  flowId?: string;
  trigger?: string;
  payload?: string;
  payloadFormat?: "json" | "yaml";
  confirmFirst?: boolean;
}

/**
 * withRowOptions folds the row's own display settings into the source's kind
 * options. A Home Assistant entity has no kind options to set, so a location
 * row's coordinates switch lives on the row.
 */
function withRowOptions(kind: string, options: Record<string, unknown>, src: EntitySource): Record<string, unknown> {
  let out = options;
  if (kind === "location" && src.showCoordinates) out = { ...out, show_coordinates: true };
  if (src.format) out = { ...out, format: src.format };
  if (typeof src.precision === "number") out = { ...out, precision: src.precision };
  return out;
}

/**
 * drawsLocation reports whether a binding resolves to a location, which is
 * when a config panel offers the row's coordinates switch. Variables are left
 * to the resolver: a reference names no source until a viewer picks one.
 */
export function drawsLocation(
  src: EntitySource,
  controls: { id: string; kind?: string }[],
  devices: { id: string; control?: string }[],
): boolean {
  switch (src.source) {
    case "ha":
      return HA_DOMAIN_KINDS[haDomain(src.entityId || "")] === "location";
    case "control":
      return controls.some((c) => c.id === src.controlId && c.kind === "location");
    case "mqtt":
      return !src.deviceField && devices.some((d) => d.id === src.deviceId && d.control === "location");
  }
  return false;
}

/** displayOnly reads the author's declaration, old spelling included. */
export function displayOnly(src: EntitySource): boolean {
  return src.displayOnly === true || src.control === "value";
}

/**
 * triggerPresses is what a flow-trigger row draws. A row saved before the list
 * existed reads back as the single press it was, so nothing has to be migrated
 * out of a frozen dashboard version.
 *
 * A press with nothing filled in yet is kept, because that is what a button
 * looks like the moment it is added and dropping it would empty the panel the
 * author is typing into. Firing one is what fails, not drawing it.
 */
export function triggerPresses(src: EntitySource): TriggerPress[] {
  const list = (src.triggers ?? []).filter((t) => t && typeof t === "object" && t.id);
  if (list.length > 0) return list;
  if (!src.trigger) return [];
  return [
    {
      id: "legacy",
      label: src.name || src.trigger,
      flowId: src.flowId,
      trigger: src.trigger,
      payload: src.payload,
      payloadFormat: src.payloadFormat,
      confirmFirst: src.confirmFirst,
    },
  ];
}

// Resolved is what a source turned out to be: the kind it declares, that kind's
// settings, and whether anything can be pressed. Every face is drawn from one
// of these, whichever source it came from.
export interface Resolved {
  kind: string;
  options: Record<string, unknown>;
  /** readOnly is the source's own answer, before the author's display-only flag. */
  readOnly: boolean;
  name: string;
  value: unknown;
  /** state is the raw Home Assistant state string, kept for the wording a lock uses. */
  state?: string;
  /**
   * unavailable says the source cannot answer right now: an entity reporting
   * "unavailable" or "unknown". The kind and options still resolve so the face
   * keeps its shape, but a value drawn from it would be a lie and a press
   * would go nowhere.
   */
  unavailable?: boolean;
}

export const BLANK_RESOLVED: Resolved = {
  kind: "text",
  options: {},
  readOnly: true,
  name: "",
  value: null,
};

/** configuredPresses are the buttons that name a trigger and can therefore fire. */
export function configuredPresses(src: EntitySource): TriggerPress[] {
  return triggerPresses(src).filter((p) => p.flowId && p.trigger);
}

/**
 * hostOperates reports whether the dashboard lets this viewer operate it. A
 * host that does not say, such as an older one, is taken to allow it.
 */
export function hostOperates(): boolean {
  return (globalThis as any).__karakuri?.canOperate?.() !== false;
}

/** pressable reports whether a binding draws something that acts. */
export function pressable(src: EntitySource, resolved: Resolved): boolean {
  if (!hostOperates()) return false;
  if (src.source === "flow-trigger") return !displayOnly(src);
  if (src.deviceField) return false;
  return !displayOnly(src) && !resolved.readOnly && !resolved.unavailable;
}

// ---- Karakuri controls ----

/** resolveControl reads a control the way the API returns it. */
export function resolveControl(control: any, src: EntitySource): Resolved {
  const kind = String(control?.kind ?? "text");
  return {
    kind,
    options: withRowOptions(kind, (control?.options ?? {}) as Record<string, unknown>, src),
    readOnly: false,
    name: src.name || String(control?.name ?? ""),
    value: control?.value,
  };
}

// ---- MQTT devices ----

/**
 * resolveDevice reads a device and the state its script has accumulated. A
 * device is writable exactly when it has a payload script: there is no separate
 * read-only kind, because "takes no commands" is already what an empty payload
 * script means.
 */
export function resolveDevice(device: any, state: any, src: EntitySource): Resolved {
  const values = state && typeof state === "object" ? (state as Record<string, unknown>) : {};
  const kind = String(device?.control ?? "text");
  const options = (device?.options ?? {}) as Record<string, unknown>;
  const field = (src.deviceField || "").trim();

  // A row bound to one of the device's other values shows that value and
  // nothing else: it is a reading the device happens to report, not the control
  // the device is.
  if (field) {
    const fieldOptions: Record<string, unknown> = {};
    if (src.fieldUnit) fieldOptions.unit = src.fieldUnit;
    if (typeof src.fieldPrecision === "number") fieldOptions.precision = src.fieldPrecision;
    if (typeof src.precision === "number") fieldOptions.precision = src.precision;
    if (src.format) fieldOptions.format = src.format;
    return {
      kind: "text",
      options: fieldOptions,
      readOnly: true,
      name: src.name || `${device?.name ?? "Device"} ${field}`,
      value: values[field],
      unavailable: deviceUnavailable(device),
    };
  }

  return {
    kind,
    options: withRowOptions(kind, options, src),
    readOnly: device?.commandable === false,
    name: src.name || String(device?.name ?? ""),
    value: deviceValue(kind, values),
    unavailable: deviceUnavailable(device),
  };
}

/**
 * deviceUnavailable reports a device that cannot answer: offline, unknown
 * because its broker is down or its availability topic has not spoken, or
 * gone from Zigbee2MQTT. A device listed before availability existed has none
 * and counts as available.
 */
export function deviceUnavailable(device: any): boolean {
  const availability = device?.availability;
  return availability === "offline" || availability === "unknown" || device?.missing === true;
}

/**
 * applyDeviceMessage folds one live device message into the device record a
 * widget holds. A state message carries the values and availability; an
 * availability message carries availability alone and must not blank the
 * values. Returns whether the message carried values.
 */
export function applyDeviceMessage(device: any, data: any): boolean {
  if (!device || !data) return false;
  if (typeof data.missing === "boolean") device.missing = data.missing;
  if (typeof data.availability === "string") {
    device.availability = data.availability;
    if (data.at) device.availability_at = data.at;
    if (data.availability_at) device.availability_at = data.availability_at;
  }
  if (data.state && typeof data.state === "object") {
    device.state = data.state;
    device.stale = false;
    return true;
  }
  return false;
}

/**
 * deviceValue shapes a device's accumulated state into what the kind holds. The
 * kind's attribute names are the keys a state script returns, so a device and a
 * widget agree without the dashboard row being told anything.
 */
function deviceValue(kind: string, values: Record<string, unknown>): unknown {
  if (isComposite(kind)) return values;
  const key = primaryAttribute(kind);
  if (key in values) return values[key];
  // A device written before the keys were fixed may report a single unnamed
  // value, which is still the reading it meant.
  const keys = Object.keys(values);
  return keys.length === 1 ? values[keys[0]] : undefined;
}

// ---- Home Assistant ----

/** haDomain returns the domain half of an entity ID. */
export function haDomain(entityId: string): string {
  return (entityId || "").split(".")[0] || "";
}

// HA_DOMAIN_KINDS maps an entity's domain onto the kind it is drawn with. A
// domain that is not listed is a reading: showing a switch beside something
// that cannot be switched is worse than showing its value.
const HA_DOMAIN_KINDS: Record<string, string> = {
  light: "light",
  switch: "boolean",
  siren: "boolean",
  humidifier: "boolean",
  input_boolean: "boolean",
  fan: "fan",
  cover: "cover",
  climate: "climate",
  water_heater: "climate",
  lock: "lock",
  scene: "buttons",
  script: "buttons",
  automation: "buttons",
  button: "buttons",
  input_button: "buttons",
  number: "number",
  input_number: "number",
  select: "select",
  input_select: "select",
  text: "text",
  input_text: "text",
  person: "location",
  device_tracker: "location",
  media_player: "media",
};

// HA_READ_ONLY_DOMAINS are drawn with a kind but take no commands from here:
// where someone is comes from their phone, not from a dashboard.
const HA_READ_ONLY_DOMAINS = new Set(["person", "device_tracker"]);

// DEFAULT_ZONES are the labels for the two states every Home Assistant
// tracker reports; any other state is a zone's own name and shows as itself.
const DEFAULT_ZONES: Choice[] = [
  { value: "home", label: "Home" },
  { value: "not_home", label: "Away" },
];

/**
 * resolveHA derives everything a face needs from one entity: which kind it is,
 * what that kind's settings are for this particular device, and its value in
 * the shape the kind holds. Home Assistant already reports what a light
 * supports, so an author is never asked.
 */
export function resolveHA(entity: any, src: EntitySource): Resolved {
  const entityId = String(entity?.entity_id ?? src.entityId ?? "");
  const domain = haDomain(entityId);
  const attributes = (entity?.attributes ?? {}) as Record<string, any>;
  const state = String(entity?.state ?? "");
  const kind = HA_DOMAIN_KINDS[domain] ?? "text";
  const name = src.name || String(attributes.friendly_name ?? entityId);

  return {
    kind,
    options: withRowOptions(kind, haOptions(kind, domain, attributes), src),
    readOnly: !(domain in HA_DOMAIN_KINDS) || HA_READ_ONLY_DOMAINS.has(domain),
    name,
    value: haValue(kind, state, attributes, entity?.last_changed),
    state,
    // "unavailable" and "unknown" are Home Assistant's own words for an
    // entity that cannot answer; rendering them as values put a NaN on a
    // slider and an off switch on a dead light.
    unavailable: state === "unavailable" || state === "unknown",
  };
}

/** haOptions turns an entity's attributes into the kind's settings. */
function haOptions(kind: string, domain: string, attr: Record<string, any>): Record<string, unknown> {
  const modes: string[] = Array.isArray(attr.supported_color_modes) ? attr.supported_color_modes : [];
  switch (kind) {
    case "light":
      return {
        brightness: modes.some((m) => m !== "onoff"),
        color: modes.some((m) => ["hs", "rgb", "rgbw", "rgbww", "xy"].includes(m)),
        color_temp: modes.includes("color_temp"),
        min_kelvin: Number(attr.min_color_temp_kelvin ?? 2000),
        max_kelvin: Number(attr.max_color_temp_kelvin ?? 6500),
        effects: Array.isArray(attr.effect_list) && attr.effect_list.length > 0,
        effect_list: asChoices(attr.effect_list),
      };
    case "fan":
      return {
        speed: attr.percentage !== undefined || attr.percentage_step !== undefined,
        speed_step: Number(attr.percentage_step ?? 1),
        presets: Array.isArray(attr.preset_modes) && attr.preset_modes.length > 0,
        preset_list: asChoices(attr.preset_modes),
        oscillate: attr.oscillating !== undefined,
        direction: attr.direction !== undefined,
      };
    case "cover":
      return {
        position: attr.current_position !== undefined,
        tilt: attr.current_tilt_position !== undefined,
        stop: true,
      };
    case "climate":
      // A water heater is drawn as a thermostat but speaks its own dialect:
      // its modes are operation_list, not hvac_modes.
      return {
        modes: asChoices(attr.hvac_modes ?? attr.operation_list),
        min_temp: Number(attr.min_temp ?? 7),
        max_temp: Number(attr.max_temp ?? 35),
        step: Number(attr.target_temp_step ?? 0.5),
        unit: String(attr.unit_of_measurement ?? "°"),
        range: attr.target_temp_low !== undefined || attr.target_temp_high !== undefined,
        fan: Array.isArray(attr.fan_modes) && attr.fan_modes.length > 0,
        fan_modes: asChoices(attr.fan_modes),
      };
    case "number":
      return {
        min: Number(attr.min ?? 0),
        max: Number(attr.max ?? 100),
        step: Number(attr.step ?? 1),
        unit: String(attr.unit_of_measurement ?? ""),
        display: attr.mode === "box" ? "box" : "slider",
      };
    case "select":
      return { options: asChoices(attr.options), display: "dropdown" };
    case "location":
      return { zones: DEFAULT_ZONES };
    case "media":
      return mediaOptions(Number(attr.supported_features ?? 0));
    default:
      // A reading takes its unit and its precision from the entity, which is
      // the only place a sensor's formatting is declared at all.
      return {
        unit: String(attr.unit_of_measurement ?? ""),
        precision: attr.suggested_display_precision,
      };
  }
}

// Home Assistant's media_player feature bits.
const MEDIA_SEEK = 2;
const MEDIA_VOLUME_SET = 4;
const MEDIA_VOLUME_MUTE = 8;
const MEDIA_PREVIOUS_TRACK = 16;
const MEDIA_NEXT_TRACK = 32;
const MEDIA_TURN_ON = 128;
const MEDIA_TURN_OFF = 256;
const MEDIA_SELECT_SOURCE = 2048;
const MEDIA_STOP = 4096;

/**
 * mediaOptions reads which controls a player has from supported_features, so
 * a speaker without volume control draws no slider and one that cannot skip
 * draws no skip.
 */
export function mediaOptions(features: number): Record<string, unknown> {
  const has = (bit: number) => (features & bit) !== 0;
  return {
    volume: has(MEDIA_VOLUME_SET),
    mute: has(MEDIA_VOLUME_MUTE),
    skip: has(MEDIA_NEXT_TRACK) || has(MEDIA_PREVIOUS_TRACK),
    seek: has(MEDIA_SEEK),
    source: has(MEDIA_SELECT_SOURCE),
    power: has(MEDIA_TURN_ON) || has(MEDIA_TURN_OFF),
    stop: has(MEDIA_STOP),
  };
}

/** asChoices renders a Home Assistant string list as the catalog's choice shape. */
function asChoices(list: unknown): Choice[] {
  if (!Array.isArray(list)) return [];
  return list.map((item) => ({ value: String(item), label: String(item) }));
}

/** haValue shapes an entity's state and attributes into what the kind holds. */
function haValue(kind: string, state: string, attr: Record<string, any>, lastChanged?: unknown): unknown {
  switch (kind) {
    case "location":
      // A person names the tracker it follows in source; a tracker says what
      // kind of tracker it is in source_type.
      return {
        state,
        latitude: typeof attr.latitude === "number" ? attr.latitude : undefined,
        longitude: typeof attr.longitude === "number" ? attr.longitude : undefined,
        accuracy: typeof attr.gps_accuracy === "number" ? attr.gps_accuracy : undefined,
        source: attr.source ?? attr.source_type,
        since: typeof lastChanged === "string" ? lastChanged : undefined,
      };
    case "light":
      return {
        on: state === "on",
        // Home Assistant counts brightness to 255; every light on a dashboard
        // reads as a percentage, so the conversion happens once, here.
        brightness:
          attr.brightness === undefined ? undefined : Math.round((Number(attr.brightness) / 255) * 100),
        color: rgbToHex(attr.rgb_color),
        color_temp: attr.color_temp_kelvin === undefined ? undefined : Number(attr.color_temp_kelvin),
        effect: attr.effect,
      };
    case "fan":
      return {
        on: state === "on",
        percentage: attr.percentage === undefined ? undefined : Number(attr.percentage),
        preset: attr.preset_mode,
        oscillating: attr.oscillating,
        direction: attr.direction,
      };
    case "cover":
      return {
        state,
        position: attr.current_position === undefined ? undefined : Number(attr.current_position),
        tilt: attr.current_tilt_position === undefined ? undefined : Number(attr.current_tilt_position),
      };
    case "climate":
      return {
        mode: state,
        target: attr.temperature === undefined ? undefined : Number(attr.temperature),
        target_low: attr.target_temp_low === undefined ? undefined : Number(attr.target_temp_low),
        target_high: attr.target_temp_high === undefined ? undefined : Number(attr.target_temp_high),
        current:
          attr.current_temperature === undefined ? undefined : Number(attr.current_temperature),
        fan_mode: attr.fan_mode,
        action: attr.hvac_action,
      };
    case "media":
      // standby is Home Assistant's other word for off.
      return {
        state: state === "standby" ? "off" : state,
        volume:
          attr.volume_level === undefined ? undefined : Math.round(Number(attr.volume_level) * 100),
        muted: attr.is_volume_muted,
        title: attr.media_title,
        artist: attr.media_artist,
        album: attr.media_album_name,
        art: attr.entity_picture,
        position: attr.media_position === undefined ? undefined : Number(attr.media_position),
        duration: attr.media_duration === undefined ? undefined : Number(attr.media_duration),
        position_at: attr.media_position_updated_at,
        source: attr.source,
        sources: Array.isArray(attr.source_list) ? attr.source_list.map(String) : undefined,
      };
    case "boolean":
      return state === "on";
    case "lock":
      return state;
    case "number":
      return Number(state);
    default:
      return state;
  }
}

/**
 * haService resolves the call one interaction makes. Returning null means the
 * binding cannot be operated, which is the answer for every domain the table
 * above does not name.
 */
export function haService(
  src: EntitySource,
  resolved: Resolved,
  attribute: string,
  value: unknown,
): { domain: string; service: string; data?: Record<string, unknown> } | null {
  const domain = haDomain(src.entityId || "");
  if (!domain) return null;
  const attr = attribute || primaryAttribute(resolved.kind);

  switch (resolved.kind) {
    case "boolean":
      return { domain, service: truthy(value) ? "turn_on" : "turn_off" };

    case "lock":
      return { domain: "lock", service: value === "locked" ? "lock" : "unlock" };

    case "buttons":
      if (domain === "automation") return { domain, service: "trigger" };
      if (domain === "button" || domain === "input_button") return { domain, service: "press" };
      return { domain, service: "turn_on" };

    case "number":
      return { domain, service: "set_value", data: { value: Number(value) } };

    case "select":
      return { domain, service: "select_option", data: { option: String(value) } };

    case "text":
      return { domain, service: "set_value", data: { value: String(value) } };

    case "light":
      if (attr === "on") {
        return { domain: "light", service: truthy(value) ? "turn_on" : "turn_off" };
      }
      if (attr === "brightness") {
        return { domain: "light", service: "turn_on", data: { brightness_pct: Number(value) } };
      }
      if (attr === "color") {
        return { domain: "light", service: "turn_on", data: { rgb_color: hexToRgb(String(value)) } };
      }
      if (attr === "color_temp") {
        return { domain: "light", service: "turn_on", data: { color_temp_kelvin: Number(value) } };
      }
      if (attr === "effect") {
        return { domain: "light", service: "turn_on", data: { effect: String(value) } };
      }
      return null;

    case "fan":
      if (attr === "on") return { domain: "fan", service: truthy(value) ? "turn_on" : "turn_off" };
      if (attr === "percentage") {
        return { domain: "fan", service: "set_percentage", data: { percentage: Number(value) } };
      }
      if (attr === "preset") {
        return { domain: "fan", service: "set_preset_mode", data: { preset_mode: String(value) } };
      }
      if (attr === "oscillating") {
        return { domain: "fan", service: "oscillate", data: { oscillating: truthy(value) } };
      }
      if (attr === "direction") {
        return { domain: "fan", service: "set_direction", data: { direction: String(value) } };
      }
      return null;

    case "cover":
      if (attr === "position") {
        return { domain: "cover", service: "set_cover_position", data: { position: Number(value) } };
      }
      if (attr === "tilt") {
        return {
          domain: "cover",
          service: "set_cover_tilt_position",
          data: { tilt_position: Number(value) },
        };
      }
      if (value === "open") return { domain: "cover", service: "open_cover" };
      if (value === "closed") return { domain: "cover", service: "close_cover" };
      return { domain: "cover", service: "stop_cover" };

    case "climate":
      if (attr === "mode") {
        if (domain === "water_heater") {
          return { domain, service: "set_operation_mode", data: { operation_mode: String(value) } };
        }
        return { domain, service: "set_hvac_mode", data: { hvac_mode: String(value) } };
      }
      if (attr === "fan_mode") {
        return { domain, service: "set_fan_mode", data: { fan_mode: String(value) } };
      }
      if (attr === "target_low" || attr === "target_high") {
        const current = (resolved.value ?? {}) as Record<string, unknown>;
        return {
          domain,
          service: "set_temperature",
          data: {
            target_temp_low: Number(attr === "target_low" ? value : current.target_low),
            target_temp_high: Number(attr === "target_high" ? value : current.target_high),
          },
        };
      }
      return { domain, service: "set_temperature", data: { temperature: Number(value) } };

    case "media":
      return mediaService(attr, value);
  }
  return null;
}

// MEDIA_STATE_SERVICES are the media_player services that put a player in
// each state the kind can be told.
const MEDIA_STATE_SERVICES: Record<string, string> = {
  playing: "media_play",
  paused: "media_pause",
  idle: "media_stop",
  on: "turn_on",
  off: "turn_off",
};

/** mediaService resolves one media write into its media_player service. */
function mediaService(
  attr: string,
  value: unknown,
): { domain: string; service: string; data?: Record<string, unknown> } | null {
  const domain = "media_player";
  switch (attr) {
    case "state": {
      const service = MEDIA_STATE_SERVICES[String(value)];
      return service ? { domain, service } : null;
    }
    case "volume":
      // Home Assistant takes volume as a fraction; the kind holds a percentage.
      return { domain, service: "volume_set", data: { volume_level: Number(value) / 100 } };
    case "muted":
      return { domain, service: "volume_mute", data: { is_volume_muted: truthy(value) } };
    case "source":
      return { domain, service: "select_source", data: { source: String(value) } };
    case "position":
      return { domain, service: "media_seek", data: { seek_position: Number(value) } };
    case "command":
      if (value === "next") return { domain, service: "media_next_track" };
      if (value === "previous") return { domain, service: "media_previous_track" };
      return null;
  }
  return null;
}

// ---- MQTT commands ----
//
// A widget sends the interaction, never the payload: which attribute is being
// set and what to. The device owns how that becomes bytes, so the same press
// produces the same message wherever it is pressed from, and a viewer who may
// not write a script can still press it.

/** CommandRequest is the body of one interaction with a device. */
export interface CommandRequest {
  field: string;
  value: unknown;
  on?: boolean;
  previous?: unknown;
}

/** commandRequest builds what one interaction sends. */
export function commandRequest(
  resolved: Resolved,
  attribute: string,
  next: unknown,
  previous?: unknown,
): CommandRequest {
  const field = attribute || primaryAttribute(resolved.kind);
  const out: CommandRequest = {
    field,
    value: next,
    previous: previous ?? null,
  };
  if (typeof next === "boolean") out.on = next;
  return out;
}

// ---- Colour ----

/** rgbToHex renders Home Assistant's rgb_color triple as the hex a face draws. */
export function rgbToHex(rgb: unknown): string | undefined {
  if (!Array.isArray(rgb) || rgb.length < 3) return undefined;
  const hex = rgb
    .slice(0, 3)
    .map((n) => Math.max(0, Math.min(255, Math.round(Number(n) || 0))).toString(16).padStart(2, "0"))
    .join("");
  return `#${hex}`;
}

/** hexToRgb is the reverse, for the service call that takes a triple. */
export function hexToRgb(hex: string): number[] {
  const clean = (hex || "").replace("#", "");
  const full = clean.length === 3 ? clean.replace(/(.)/g, "$1$1") : clean;
  if (full.length !== 6) return [255, 255, 255];
  return [
    parseInt(full.slice(0, 2), 16),
    parseInt(full.slice(2, 4), 16),
    parseInt(full.slice(4, 6), 16),
  ];
}

// ---- Face helpers ----
//
// The faces need the same handful of answers about a kind's settings, and a
// widget should not reach past them into the catalog.

export { choices, findKind, hasFeature, isComposite, numberOption, primaryAttribute, textOption };
