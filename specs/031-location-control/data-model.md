# Data model: Location control kind

## Location value

Stored as JSON in `controls.state` for a control and in the device state
held in `mqtt_device_states` for an MQTT device.

| Key | Type | Rule |
| --- | --- | --- |
| `state` | string | Primary. Not empty. One of `zones` when `restrict_zones` is on. |
| `latitude` | number | -90 to 90. |
| `longitude` | number | -180 to 180. |
| `accuracy` | number | Metres, zero or more. |
| `source` | string | Free text: `gps`, `router`, `bluetooth`, a tracker entity ID. |
| `since` | string | RFC 3339 UTC. Read-only. Set by the writer when `state` changes. |

Every key is optional. A new control holds `{}`.

Example:

```json
{
  "state": "home",
  "latitude": 41.8781,
  "longitude": -87.6298,
  "accuracy": 25,
  "source": "gps",
  "since": "2026-10-31T14:02:11Z"
}
```

## Options

| Option | Type | Default | Meaning |
| --- | --- | --- | --- |
| `zones` | choices | `[{value: home, label: Home}, {value: not_home, label: Away}]` | Labels shown for state values, in picker order. |
| `restrict_zones` | boolean | false | Refuse states that are not listed in `zones`. |
| `manual` | boolean | false | Draw the zones as a picker on dashboards so a viewer can set `state`. |
| `show_coordinates` | boolean | false | Show coordinates and accuracy under the state. |

`NormalizeOptions` refuses `restrict_zones` with an empty `zones` list, since
such a control could never be set.

## `since` transitions

| Write | Before | After |
| --- | --- | --- |
| `{state: home}` | `{}` | `since` = now |
| `{latitude: 41.9}` | `{state: home, since: T}` | `since` = T |
| `{state: home, accuracy: 10}` | `{state: home, since: T}` | `since` = T |
| `{state: work}` | `{state: home, since: T}` | `since` = now |
| `{since: X}` | any | refused |

For an MQTT device the same rules apply to the decoded values merged into the
device state; a `since` the script returns is discarded.

## Home Assistant entity to location value

| Location key | `person` | `device_tracker` |
| --- | --- | --- |
| `state` | `state` | `state` |
| `latitude` | `attributes.latitude` | `attributes.latitude` |
| `longitude` | `attributes.longitude` | `attributes.longitude` |
| `accuracy` | `attributes.gps_accuracy` | `attributes.gps_accuracy` |
| `source` | `attributes.source` | `attributes.source_type` |
| `since` | `last_changed` | `last_changed` |

`unavailable` and `unknown` states draw as unavailable.
