# Data model: Zigbee2MQTT discovery and device availability

## `mqtt_devices` columns added

| Column | Type | Default | Meaning |
| --- | --- | --- | --- |
| `availability_topic` | text | empty | Topic carrying online and offline payloads. |
| `availability_online` | text | `online` | Payload meaning online. |
| `availability_offline` | text | `offline` | Payload meaning offline. |
| `expect_every_s` | integer | 0 | Offline after this long with no message; 0 is off, otherwise at least 60. |
| `offline_commands` | boolean | false | Send commands while offline. |
| `z2m_ieee` | text | empty | IEEE address of the Zigbee2MQTT device this was added from. |
| `z2m_base` | text | empty | Zigbee2MQTT base topic it was added from. |
| `missing` | boolean | false | Set while the device is absent from Zigbee2MQTT's list. |

`(connection_id, z2m_base, z2m_ieee)` is unique when `z2m_ieee` is set,
checked on save.

## `mqtt_device_states` columns added

| Column | Type | Meaning |
| --- | --- | --- |
| `availability` | text | `online`, `offline`, or `unknown`. |
| `availability_source` | text | `connection`, `topic`, `interval`, or `default`. |
| `availability_at` | timestamp | When it last changed. |

Restored availability is marked stale with the values.

## Running device

`device` in `pkg/mqttrelay` gains `availability`, `availabilitySource`,
`availabilityAt`, and `availabilityNote` (the last unrecognised availability
payload). `DeviceStatus` carries them.

## Discovered device (not stored)

| Field | Source |
| --- | --- |
| `ieee_address`, `friendly_name`, `type`, `power_source`, `supported`, `interview_completed`, `disabled` | device list entry |
| `model`, `vendor`, `description` | `definition` |
| `kind` | mapping of the main expose |
| `fields` | other expose properties kept as state fields |
| `can_add` | supported, interviewed, not disabled, not the coordinator |
| `device_id` | karakuri device already added from it, if any |

## Availability transitions

| Event | Before | After |
| --- | --- | --- |
| connection leaves connected | any | unknown, `connection` |
| connection connected, no topic | unknown | online, `default` |
| connection connected, topic | unknown | unknown until a message |
| topic online payload | any | online, `topic` |
| topic offline payload | any | offline, `topic` |
| interval exceeded, online | online | offline, `interval` |
| any message, interval set, offline by `interval` | offline | online, `interval` |
