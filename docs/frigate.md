# Frigate NVR

A Frigate NVR connection talks to a Frigate server over Frigate's HTTP API and
websocket. Frigate keeps doing the recording, detection, and storage.
Karakuri shows its cameras on dashboards, starts flows on its detections, and
switches its camera features. Nothing needs configuring on the Frigate side,
and no MQTT broker is involved. Karakuri stores no video. It fetches every
image, stream, and clip from Frigate when someone asks for it.

## Connecting

Add a connection of kind **Frigate NVR** under **Connections**:

| Field | Meaning |
| --- | --- |
| URL | Frigate's base address, such as `http://frigate.local:5000`. Port 5000 is Frigate's internal port and needs no login. Port 8971 needs one. |
| Username, Password | A Frigate user, for a URL that needs a login. The password is stored encrypted and never shown again. |
| Skip TLS verification | Accept any certificate, such as Frigate's self-signed one on port 8971. |
| CA certificate | A PEM certificate to trust instead of the system's. |

**Test** reports the version and cameras, such as `Frigate 0.18.2, 5 cameras:
doorbell, house_back, house_front, kitchen, living_room`, or why it failed:
`login refused`, `frigate requires a login`, or the network error.

The connection stays open on Frigate's websocket. Every 15 seconds it reads
Frigate's stats to learn which cameras are receiving frames, and shows a
camera at 0 fps as unavailable. A camera added to Frigate's config appears at
the next stats read. When Frigate restarts or the network drops, the
connection goes to error and reconnects on its own, waiting 1 second and
doubling up to a minute. With a login, the connection renews an expired
session on the next request.

## Cameras on dashboards

The **Camera** widget takes **NVR camera (Frigate)** as its source. Pick the
connection and a camera, choose a live stream or snapshots every few seconds,
and optionally turn on **Draw boxes around tracked objects**. The widget asks
for a live stream at a height that fits it, in steps of 180, 360, 480, 720,
and 1080 pixels. All viewers of one camera at one step share a single stream
from Frigate, so a wall of panels showing the doorbell costs Frigate one
stream. That stream closes 10 seconds after its last viewer leaves.

The **NVR Events** widget lists recent events of one camera, or of every camera
with **Every camera**, with label, zones, score, time, and a thumbnail. It
refreshes when an event starts or ends. Tapping an event plays its clip.

A dashboard viewer reaches exactly the cameras its widgets show. A camera
widget grants that one camera's snapshots, stream, events, and event media. An
event list on every camera grants every camera of that connection. A viewer
asking for another camera gets 403. A camera receiving no frames answers 503,
and the widget says so.

## Camera switches as devices

**Settings, Devices, Add from Frigate** (or `discover_entities` and
`add_entities` with the Frigate connection) lists each camera's `detect`,
`record`, `snapshots`, and `motion` switches. Each switch added becomes a
toggle device named after the camera and feature, such as `doorbell detect`,
with object ID `doorbell/detect`. Turning it on or off sends the change to
Frigate. The device's value changes when Frigate reports the new state. With
auto import on the connection, every camera's switches are added without
picking them; the Device auto import guide under **Help** covers it.

A switch device is offline while its camera receives no frames and unknown
while the Frigate server is unreachable. A camera removed from Frigate's config
marks its switches missing until the camera returns.

Turning detection or recording off stops Frigate watching or recording that
camera. Put such a switch on a dashboard only where that is wanted.

## Nodes

Nodes are in the **NVR** folder.

**NVR Event** starts a run on a tracked object. It filters by:

| Field | Meaning |
| --- | --- |
| Cameras | Only these cameras. Empty is every camera. |
| Labels | Only these objects, such as `person`, `car`, `dog`. |
| Sub label contains | A recognised face or plate name, case-insensitive. |
| Zones | Only objects in any of these zones. Start and update fire when the object enters one. End fires when it had been in one. |
| Minimum score | Frigate's score from 0 to 1. |
| Stages | `start` when Frigate begins tracking, `end` when it stops, `update` on every change. Frigate sends updates several times a second for a moving object, so most flows want start, end, or a zone filter. |

The payload looks like this:

```json
{ "id": "1793910167.825535-bsyysr", "camera": "doorbell", "label": "person",
  "sub_label": null, "zones": ["driveway"], "entered_zones": ["driveway"],
  "score": 0.86, "stage": "start", "start_time": "2026-10-31T15:20:55Z",
  "end_time": null, "has_clip": true, "has_snapshot": true,
  "snapshot": "/api/connections/<id>/nvr/events/<event>/snapshot",
  "thumbnail": "/api/connections/<id>/nvr/events/<event>/thumbnail",
  "clip": "/api/connections/<id>/nvr/events/<event>/clip",
  "connection_id": "<id>" }
```

`zones` lists where the object is now, or, at the end, every zone it entered.
`entered_zones` lists the zones it entered with this message. The media paths
go through karakuri's relay, and anyone allowed to read the camera can read
them.

**NVR Review** starts a run on Frigate's review items, each an alert or a
detection that groups activity worth looking at. It filters by camera,
severity (`alert`, `detection`, or `any`), and stage. Its payload is `{id,
camera, severity, objects, zones, stage, start_time, end_time}`.

**NVR Image** reads a camera's latest frame, scaled to a height and with boxes
if asked, or an event's snapshot. After an NVR Event trigger, set **Event** to
`{{ payload.id }}`. The **Bytes** output carries the JPEG with
`meta.content_type`, ready for a notification attachment. The **Image** output
is a typed image for an AI agent's Images input. A camera receiving no frames
or an unknown event fails through the node's error handling and can be
retried.

Triggers fire only while connected. Karakuri does not replay events that
happen while Frigate is unreachable.

## MCP

- `list_nvr_cameras {connection_id}` lists cameras with zones, objects,
  switches, and whether each receives frames.
- `list_nvr_events {connection_id, cameras?, labels?, zones?, limit?}` lists
  recent events, newest first, with relay paths.
- `get_nvr_snapshot {connection_id, camera, height?, bbox?}` returns the latest
  frame as an image.
- `discover_entities` and `add_entities` add the camera switches as devices.

## Limits

| Limit | Value |
| --- | --- |
| Snapshot | 5 MB, 10 seconds |
| Thumbnail | 1 MB |
| Event list | 100 events |
| Clip | Relayed for as long as the viewer's request lasts. Frigate builds the clip from its recordings and sends it whole. |
| Live stream | 1 to 10 fps, default 5; one stream from Frigate per camera and height |
| Stats read | Every 15 seconds |
| Reconnect | 1 second, doubling to 60 |

Karakuri does not record, detect, transcode, or keep video, and does not serve
WebRTC or the go2rtc restream. Frigate keeps those.
