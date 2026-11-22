# Contract: Zigbee2MQTT and availability interfaces

## REST

All need `api:connections:write`, like creating a device.

- `GET /api/mqtt_devices/zigbee2mqtt?connection_id=<uuid>&base_topic=zigbee2mqtt`
  returns `{devices: [DiscoveredDevice]}` as in [data-model.md](../data-model.md),
  or 404 `Zigbee2MQTT not found on zigbee2mqtt/bridge/devices` after 5 seconds.
- `POST /api/mqtt_devices/zigbee2mqtt` with
  `{connection_id, base_topic, ieee_addresses: [..]}` adds those devices and
  returns `{added: [MQTTDevice], failed: [{ieee_address, error}]}`.
- `POST /api/mqtt_devices/{id}/regenerate` rebuilds the kind, options, and
  scripts of a device added from Zigbee2MQTT from the current list. Refused
  for other devices.
- `GET /api/mqtt_devices` and `GET /api/mqtt_devices/{id}` add
  `availability`, `availability_source`, `availability_at`,
  `availability_note`, `missing`, `z2m_ieee`, `z2m_base`, and the
  availability settings. The dashboard viewer's reduced list adds
  `availability` and `availability_at` only.
- `POST`/`PUT /api/mqtt_devices` accept `availability_topic`,
  `availability_online`, `availability_offline`, `expect_every_s`,
  `offline_commands`. `expect_every_s` between 1 and 59 is refused.
- `POST /api/mqtt_devices/{id}/command` to an offline device without
  `offline_commands` returns 409 with
  `device "Hallway motion" is offline since 2026-10-31T14:02:11Z; allow commands while offline on the device to send anyway`.

## MCP

- `discover_zigbee2mqtt_devices {connection_id, base_topic?}`
- `add_zigbee2mqtt_devices {connection_id, base_topic?, ieee_addresses}`
- `regenerate_mqtt_device {id}`
- `save_mqtt_device` and `list_mqtt_devices` carry the same availability
  fields as REST.

## Broker

`mqttdevice:<id>` messages of type `mqtt_device_state` add
`availability`, `availability_at`. A new type `mqtt_device_availability`
carries `{device_id, availability, previous, source, at}` when only
availability changed.

## Nodes

`mqtt-device-availability` (trigger, folder MQTT):

| Field | Meaning |
| --- | --- |
| `device` | Device to watch, or empty for every device. |
| `to` | `online`, `offline`, `unknown`, or empty for any. |
| `test_params` | Seed payload for test runs. |

Output payload:

```json
{ "device_id": "…", "name": "Hallway motion", "availability": "offline",
  "previous": "online", "source": "topic", "at": "2026-10-31T14:02:11Z" }
```

`mqtt-device-get` and `mqtt-device-changed` add `meta.availability`.
`mqtt-device-set` fails with the refusal above.

## Generated scripts

State script for a light added from `zigbee2mqtt` (abridged):

```javascript
let d;
try { d = JSON.parse(payload); } catch { return null; }
if (!d || typeof d !== "object" || Array.isArray(d)) return null;
const out = {};
if ("state" in d) out.on = d.state === "ON";
if ("brightness" in d) out.brightness = Math.round((d.brightness / 254) * 100);
if ("color_temp" in d) out.color_temp = Math.round(1000000 / d.color_temp);
if (d.color && "x" in d.color) out.color = xyToHex(d.color.x, d.color.y, d.brightness);
for (const k of Object.keys(d)) {
  if (!["state", "brightness", "color_temp", "color"].includes(k)) out[k] = d[k];
}
return out;
```

Payload script:

```javascript
const setTopic = "zigbee2mqtt/0x282c02bfffe6c5fe/set";
switch (field) {
  case "on": return { topic: setTopic, payload: JSON.stringify({ state: on ? "ON" : "OFF" }) };
  case "brightness": return { topic: setTopic, payload: JSON.stringify({ brightness: Math.round(value * 2.54) }) };
  case "color_temp": return { topic: setTopic, payload: JSON.stringify({ color_temp: Math.round(1000000 / value) }) };
  case "color": return { topic: setTopic, payload: JSON.stringify({ color: { hex: value } }) };
}
return {};
```
