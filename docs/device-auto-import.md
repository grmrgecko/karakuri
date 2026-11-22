# Device auto import

A connection with auto import on adds every device it reports as a karakuri
device, including ones that appear later, and can remove the ones it stops
reporting. MQTT, ESPHome, Frigate NVR, Home Assistant, and openHAB connections
offer it. Connections without it keep the **Add from** buttons under
**Settings, Devices**, where devices are picked one at a time.

## Turning it on

Edit the connection under **Connections** and turn on **Auto import devices**.
Saving reconnects the connection, and every device it reports is added
straight away.

| Field | Kinds | Meaning |
| --- | --- | --- |
| Auto import devices | all | Add every device the connection reports. |
| Remove after | all | Remove an auto-imported device its source has not reported for this long, such as `12h` or `7d`. Empty never removes. At least `1h`. |
| Discovery prefix | MQTT | The topic prefix devices publish Home Assistant MQTT discovery under. Default `homeassistant`. |
| Zigbee2MQTT base topic | MQTT | The Zigbee2MQTT instance to import from. Default `zigbee2mqtt`; `off` turns Zigbee2MQTT import off. |
| Only areas, Only domains | Home Assistant | Comma-separated area IDs, area names, or floors, and entity domains such as `light, switch`. |
| Only locations, Only item types | openHAB | Comma-separated location item names, labels, or floors, and item types. `Number` matches dimensioned numbers too. |

An empty filter matches everything, and an entity must match every filter
that is set. A Home Assistant install has hundreds of entities, most of them
diagnostics, so set at least one filter there.

Editing the connection shows what auto import has done since the server
started: how many devices it imported, which reported devices it skipped and
why, and when it last imported or removed one. `get_auto_import_status` over
MCP reports the same.

## What each connection imports

| Connection | Imports |
| --- | --- |
| MQTT | Every device announcing itself with Home Assistant MQTT discovery under the prefix, and every Zigbee2MQTT device that finished its interview. |
| ESPHome | Every entity of the node. Entities a reflash adds arrive when the node reconnects. |
| Frigate NVR | Every camera's switches. A camera added to Frigate arrives at the next stats read. |
| Home Assistant | Every entity matching the filters, including ones added later. |
| openHAB | Every item matching the filters, including ones added later. |

Imported devices are the same devices the **Add from** buttons make, and work
on dashboards and in flows the same way. They are marked **auto** in the
device list. Editing an imported device's name, kind, or scripts is kept; a
later discovery change moves only its topics and availability topic.

## Home Assistant MQTT discovery

Tasmota, Shelly, ESPHome flashed for MQTT, Zigbee2MQTT, and many other
firmwares announce their devices with retained configs on
`<prefix>/<component>/<object_id>/config`. Home Assistant does not need to
run. Karakuri reads each config, including abbreviated keys, the `~` base
topic, and device-based configs that list several components, and writes the
device's state and payload scripts from it.

| Component | Device kind |
| --- | --- |
| switch | Toggle |
| binary_sensor | Toggle, read only, labelled by device class (door: Open and Closed, motion: Detected and Clear) |
| sensor | Number with its unit when it has a unit or state class, otherwise text |
| light | Light, default and JSON schemas, with brightness, colour, colour temperature, and effects |
| cover | Cover, with position and tilt |
| lock | Lock |
| fan | Fan, with speed, presets, oscillation, and direction |
| climate | Thermostat, with modes, target or range, current temperature, action, and fan modes |
| number | Number with its bounds, step, and unit |
| select | Dropdown |
| button | Buttons, one button |
| text | Text |

Value and command templates are translated to the scripts' JavaScript.
Translation covers `value` and `value_json` paths, arithmetic, comparisons,
`and`, `or`, `not`, inline `if` and `else`, and the filters `float`, `int`,
`round`, `default`, `lower`, `upper`, `string`, `trim`, and `abs`. A config
using `{% %}` blocks or anything else is skipped, and the import status says
why. Other components, such as device triggers, cameras, and alarm panels, are
skipped the same way.

A config published again with an empty payload marks its device missing.
Zigbee2MQTT publishes discovery for its own devices too; while a Zigbee2MQTT
base topic is set, those come from its bridge list instead, once.

**Settings, Devices, Add from MQTT discovery** (or
`discover_mqtt_discovery_devices` and `add_mqtt_discovery_devices`) picks
discovery devices by hand on a connection without auto import.

## Removal

With **Remove after** set, an auto-imported device is removed, with its stored
state, once its source has not reported it for that long. The event log
records each removal with the time the device was last reported. Devices
added by hand or through a picker are never removed this way.

A report is:

- On MQTT, a live message: a discovery config, a state message, or an
  availability message saying online. Retained messages the broker replays on
  reconnect do not count.
- On ESPHome, Frigate, Home Assistant, and openHAB, the device's entity being
  in the connection's list while it is connected.

The broker, Frigate, Home Assistant, or openHAB being unreachable says nothing
about a device, so the clock for their devices starts again when the
connection reconnects. An ESPHome connection is the node itself, so the time
it is unreachable counts.

A device removed for not reporting comes back on its next live report, not
from a retained config replayed on reconnect.

## Deleting an imported device

Deleting an auto-imported device while its connection still imports it is
temporary: the device is added again the next time its source reports it. The
delete confirmation says so. To keep a device out, narrow the connection's
filters or turn auto import off. Turning auto import off keeps every device it
added as an ordinary device.

## Notes

- Deleting a connection leaves its devices, as with picked devices.
- Several Zigbee2MQTT instances on one broker need one MQTT connection each
  for auto import, since a connection imports from one base topic.
- Karakuri does not publish Home Assistant's birth message and never clears
  retained discovery configs on the broker.
- New ESPHome nodes are not found automatically; each node still needs its own
  connection and key.
