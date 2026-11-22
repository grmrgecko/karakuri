# Research: Zigbee2MQTT discovery and device availability

The lab broker at 10.0.10.5 carries a Zigbee2MQTT 2.14.2 bridge with 14
devices: a coordinator, ten Third Reality plugs (one plain, nine metering),
two Aqara buttons, and a Tuya air quality sensor. Its retained
`zigbee2mqtt/bridge/devices` (about 69 KB) is the fixture these decisions were
checked against; the bridge's own availability feature is off and its state
is `offline`. Several plugs power network equipment, so nothing in this
feature's checks commands a real device.

## Availability is generic

- **Decision**: one mechanism on every MQTT device: availability topic,
  online and offline payloads, optional expected interval, and the
  connection's status. Zigbee2MQTT devices get their topic filled in when
  added; the payload match accepts the configured strings or a JSON object
  whose `state` equals them, which is Zigbee2MQTT's current form, while the
  plain form matches the strings directly.
- **Rationale**: Tasmota, ESPHome, Shelly, and most firmware publish a
  last-will message on a topic of their own. A Zigbee-only path would leave
  them out and duplicate the logic.
- **Alternatives**: an availability script; the payloads in the field cover
  every known form, and a script adds a second sandbox run per message.

## Unknown versus online

- **Decision**: unknown while the connection is down, and for a device with
  an availability topic until its first availability message after the
  connection comes up. Devices with no settings are online while connected.
- **Rationale**: the broker delivers retained availability on subscribe, so
  unknown lasts a moment for devices that publish retained availability, and
  existing devices keep today's behaviour.

## Connection status

- **Decision**: the relay takes an interface
  `WatchStatus(id) (Status, bool, <-chan Status, func())`, satisfied by
  `connmgr.Manager`, and runs one watcher per connection that has running
  devices.
- **Rationale**: a connection going down is the event that matters; polling
  the driver every sweep would delay it by up to 15 seconds.

## Expected interval

- **Decision**: one sweep every 15 seconds over running devices with
  `expect_every_s`; minimum 60 seconds.
- **Rationale**: battery sensors report every few minutes to an hour, so a
  15-second sweep is well inside the precision anyone sets. A timer per
  device would cost more for no visible gain.

## Refusing commands

- **Decision**: `PublishCommand` refuses for offline devices unless
  `offline_commands` is on; unknown is not refused. The REST command handler
  moves onto `PublishCommand`.
- **Rationale**: unknown mostly means the broker is down, where publishing
  fails anyway with a clearer error; refusing on unknown would also block
  commands during the moment after a restart. One choke point keeps the three
  command paths in agreement.

## Mapping exposes to kinds

The main expose is the first one without a `category` (Zigbee2MQTT marks
settings `config` and readings such as battery and link quality
`diagnostic`). Specific types win over generic ones:

| Main expose | Kind | Options and keys |
| --- | --- | --- |
| `light` | light | `brightness`, `color` (when `color_xy` or `color_hs`), `color_temp` with kelvin bounds from the mired range, `effects` from an `effect` enum |
| `switch` | boolean | `state` ON/OFF to `on` |
| `cover` | cover | `state`, `position`, `tilt` |
| `lock` | lock | `state` LOCK/UNLOCK to locked/unlocked |
| `fan` | fan | `state` to `on`, `fan_mode` presets |
| `climate` | climate | `system_mode` modes, `occupied_heating_setpoint` or `current_heating_setpoint` as `target`, `local_temperature` as `current`, `running_state` as `action`, `fan_mode` |
| binary `contact`, `occupancy`, `water_leak`, `smoke`, `tamper`, `vibration`, `presence` | boolean, read-only | `on`, with value labels (contact true is Closed) |
| enum `action` | text, read-only | `value` is the last action; flows read it from `mqtt-device-changed` |
| numeric | number, read-only | `value`, `unit`, bounds from the expose |
| anything else | text, read-only | `value` from the first property |

Every other expose property is kept under its own name as a state field, so
battery, power, energy, and link quality become rows of their own. Values are
converted at the edge: brightness 0 to 254 to a percentage, colour
temperature mireds to kelvin, colour `{x,y}` or `{hex}` to hex.

## Generated scripts

- **Decision**: Go templates write a plain script per device with the
  property names in it, readable and editable. The state script parses JSON,
  sets the kind's keys from the mapped properties, copies every other
  property, and returns nothing for a payload that is not a JSON object. The
  payload script switches on `field` and publishes to `<base>/<ieee>/set`.
- **Rationale**: Zigbee2MQTT accepts the IEEE address in place of the friendly
  name on `/set`, so a rename never touches a script. Generated scripts are
  ordinary scripts, so the author can change anything and regenerate later.
- **Alternatives**: one shared runtime script reading a mapping table; harder
  to read and edit, which the spec requires.

## Renames and removals

- **Decision**: `pkg/zigbee2mqtt.Sync` holds one subscription to
  `<base>/bridge/devices` per connection and base that added devices use.
  On each list it rewrites state and availability topics that still name the
  old friendly name, and sets or clears `missing`.
- **Rationale**: Zigbee2MQTT republishes the retained list after any rename,
  removal, or interview, so the list alone is enough; `bridge/event` would
  add a second source of the same truth.

## Discovery read

- **Decision**: subscribe, wait up to 5 seconds for the retained list, then
  drop the subscription. The MQTT driver delivers retained messages to new
  subscriptions.
- **Rationale**: the list is retained, so it arrives at once when Zigbee2MQTT
  has ever run on that base topic, even with the bridge offline, as on the
  lab broker.

## Testing without real devices

- **Decision**: unit tests use the recorded 2.14.2 list plus a synthetic list
  with a colour light, a cover, a lock, a fan, a thermostat, and a contact
  sensor. Live checks publish that synthetic list and device messages under a
  test base topic, `karakuri-test`, on the lab broker, and use the real list
  for discovery only.
- **Rationale**: the household's plugs power its network; a test that
  toggles one could take the lab offline.
