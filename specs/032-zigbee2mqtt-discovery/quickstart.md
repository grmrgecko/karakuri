# Quickstart: Zigbee2MQTT discovery and device availability

Prerequisites: `make dev`, `systemctl --user restart karakuri-dev.service`, the
MCP token, and the development connection "Lab MQTT (10.0.10.5)" (broker
`mqtt://10.0.10.5:1883`, user `iot`). The broker carries a real Zigbee2MQTT on
base topic `zigbee2mqtt` whose plugs power network equipment: **never command
a device under `zigbee2mqtt`**. Commands and availability use synthetic
devices under base topic `karakuri-test`, published with `publish_mqtt` and
cleared with empty retained messages at the end.

## 1. Discover the real bridge (read only)

1. `discover_zigbee2mqtt_devices` on the lab connection with base
   `zigbee2mqtt`.
2. Expect 13 devices (the coordinator left out): plugs as Toggle with power,
   energy, and link quality fields; the Aqara buttons as a text reading of
   `action` with battery; the air quality sensor as a temperature reading with
   humidity, CO2, VOC, formaldehyde, and PM2.5 fields.
3. Discover with base `nothing-here` and expect the not-found error after
   about 5 seconds.

## 2. Add synthetic devices

1. Publish, retained, a `karakuri-test/bridge/devices` list holding a colour
   light, a cover, a lock, a fan, a thermostat, and a contact sensor (the
   fixture in `pkg/zigbee2mqtt/testdata/synthetic.json`).
2. Discover base `karakuri-test`, add all six.
3. Publish a state message for each on `karakuri-test/<name>`; expect each
   device's state in its kind's keys.
4. Command the light from a dashboard and with `command_mqtt_device`; capture
   `karakuri-test/+/set` and expect the IEEE topic and Zigbee2MQTT payloads.

## 3. Availability

1. Publish `{"state":"offline"}` retained on
   `karakuri-test/<light>/availability`. Expect offline in the device list,
   the dashboard row unavailable, and an `mqtt-device-availability` test flow
   run with `previous: online`.
2. Command the light and expect the refusal; turn on `offline_commands` and
   expect the command published.
3. Publish `online` (plain form) and expect online.
4. On a hand-written device, set `expect_every_s: 60`, send one message, wait
   75 seconds, and expect offline by `interval`; send another and expect
   online.
5. Disable the lab connection; expect every device on it unknown; enable it
   and expect the retained availability back.

## 4. Restart

1. With the light offline, restart the service. Expect the light offline and
   stale in the list, then confirmed by the retained message.

## 5. Rename and removal

1. Republish the synthetic list with the contact sensor renamed. Expect its
   topics and availability topic on the new name, name and bindings
   unchanged, and its next state message read.
2. Republish without the lock. Expect it missing and unavailable, not deleted.
   Republish with it and expect missing cleared.

## 6. Browser

1. Open Settings, MQTT devices, discover the lab bridge, and check the list,
   the added marks, and the disabled entries; add nothing from `zigbee2mqtt`.
2. Open a synthetic device and check the availability settings and badges.
3. Take settled screenshots and check the console.

## Cleanup

Delete the synthetic devices and test flows, publish empty retained payloads
on every `karakuri-test/#` topic used, and report what was created. Keep the
lab connection for later features unless asked to remove it.
