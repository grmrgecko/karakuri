# Data model: Frigate NVR integration

## Connection config (`frigate` kind)

| Field | Type | Secret | Meaning |
| --- | --- | --- | --- |
| `url` | string | no | Base URL, such as `http://10.0.10.5:5000` or `https://nvr.local:8971`. Required. |
| `username` | string | no | Frigate user, when the URL needs login. |
| `password` | string | yes | Frigate password. |
| `tls_skip_verify` | boolean | no | Accept any certificate. |
| `ca_cert` | string | no | PEM certificate to trust instead of the system roots. |

## Camera (driver memory)

`name`, `zones` (names), `objects` (tracked labels), `receiving` (camera fps
above zero at the last stats poll), `switches` (detect, record, snapshots,
motion from `camera_activity`).

## Event (as relayed and as trigger payload)

| Field | Source |
| --- | --- |
| `id`, `camera`, `label`, `sub_label`, `zones`, `score`, `start_time`, `end_time`, `has_clip`, `has_snapshot` | Frigate event (`data.score` or `top_score`) |
| `stage` | `start` for Frigate `new`, `update`, or `end` |
| `entered_zones` | zones in `after` not in `before` |
| `snapshot`, `thumbnail`, `clip` | relay paths under `/api/connections/<id>/nvr/events/<event>/` |

## Review item

`id`, `camera`, `severity` (`alert` or `detection`), `objects`, `zones`,
`start_time`, `end_time`, `stage`.

## Binding (`nvr` source)

```json
{ "source": "nvr", "connectionId": "<uuid>", "camera": "doorbell", "displayOnly": true }
```

`camera: "*"` binds every camera of the connection.

## Devices

`mqtt_devices` rows with `transport: frigate`, `entity_key: <camera>/<feature>`,
`entity_type: switch`, kind `boolean`. Availability follows the connection
and, through `EntityAvailability`, whether the camera receives frames.
