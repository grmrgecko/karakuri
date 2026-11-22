# Quickstart: Frigate NVR integration

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`, the
MCP token. The lab Frigate at `http://10.0.10.5:5000` is read only: never set
a camera's switches there unless the user has said so.

## 1. Connect

1. Save a `frigate` connection to `http://10.0.10.5:5000`; `test_connection`
   reports `Frigate 0.18.2, 5 cameras: doorbell, house_back, house_front,
   kitchen, living_room`.
2. `list_nvr_cameras` shows each camera receiving frames.

## 2. Cameras on a dashboard

1. A test dashboard with a camera widget on `doorbell` live and one on
   `house_front` as snapshots every 10 seconds with boxes.
2. Open it, see both, check the console.
3. As a viewer, request `kitchen`'s snapshot through the relay and expect 403.

## 3. Detections

1. A test flow with `nvr-event` on all cameras, stages start and end, into a
   debug node; deploy and enable.
2. Wait for a real event on the lab (the lab sees cars and pets during the
   day); expect a start run and an end run with relay links. Fetch the
   snapshot link and expect a JPEG.
3. `nvr-image` in a test run for `doorbell` returns a JPEG.

## 4. Event list

1. An `nvr-events` widget on `*`, limit 10; see thumbnails; tap one and play
   its clip.

## 5. Switches

1. Against the simulated server in tests only: add a camera's switches, toggle
   detect, see the reported state. On the lab, list the switches with
   `discover_entities` without adding or toggling.

## Cleanup

Delete the test dashboard, flow, and connection; report what was created.
