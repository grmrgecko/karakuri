# Tasks: Zigbee2MQTT discovery and device availability

**Feature**: `specs/032-zigbee2mqtt-discovery`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

Never publish under `zigbee2mqtt/` on the lab broker; live checks use the
`karakuri-test` base topic.

## Phase 1: Shared preparation

- [X] T001 Copy the recorded lab list `build/z2m-devices-2.14.json` to `pkg/zigbee2mqtt/testdata/bridge-devices-2.14.json` and write `pkg/zigbee2mqtt/testdata/synthetic.json`, a Zigbee2MQTT 2.x device list with a colour light (brightness, `color_xy`, `color_temp` with a mired range, `effect`), a cover (state, position, tilt), a lock, a fan with `fan_mode`, a thermostat (`system_mode`, `occupied_heating_setpoint`, `local_temperature`, `running_state`), a contact sensor with battery, an unsupported device, and an interviewing device

## Phase 2: Foundation, stored fields

Every user story phase depends on this phase.

- [X] T002 Add to `models.MQTTDevice` in `pkg/models/mqtt_device.go`: `AvailabilityTopic`, `AvailabilityOnline` (default `online`), `AvailabilityOffline` (default `offline`), `ExpectEveryS` ("0 is off, otherwise at least 60"), `OfflineCommands` (default false), `Z2MIEEE`, `Z2MBase`, `Missing` (default false); and to `models.MQTTDeviceState` in `pkg/models/mqtt_device_state.go`: `Availability` (`online`, `offline`, or `unknown`), `AvailabilitySource` (`connection`, `topic`, `interval`, or `default`), `AvailabilityAt`; confirm the GORM auto-migration adds the columns to the development database without touching existing rows
- [X] T003 Extend `Definition`, `ValidateDefinition`, and the save path in `pkg/mqttrelay/view.go` and `pkg/api/mqtt_device_handler.go`: the availability settings; refuse `expect_every_s` between 1 and 59; refuse a second device with the same connection, `z2m_base`, and `z2m_ieee`; and expose the new fields on device reads, with only `availability` and `availability_at` in the dashboard viewer's reduced list

## Phase 3: User story 2, availability (P1)

**Goal**: every MQTT device is online, offline, or unknown, drawn, refused, triggered, and restored accordingly.
**Independent check**: quickstart scenarios 3 and 4.

- [X] T004 [US2] Availability state in `pkg/mqttrelay/device.go`: fields on `device` and `DeviceStatus`, the transitions in data-model.md, a separate subscription to `availability_topic` that matches the configured strings or a JSON object whose `state` equals them and records any other payload as `availability_note`, availability watchers, and a `mqtt_device_availability` broker message plus `availability` and `availability_at` on `mqtt_device_state` messages
- [X] T005 [US2] Connection status in `pkg/mqttrelay`: a `StatusWatcher` interface (`WatchStatus(id) (integrations.Status, bool, <-chan integrations.Status, func())`) set by `cmd/karakuri/serve.go` from `connmgr.Manager`, one watch per connection with running devices, unknown while not connected, and the connected rules for devices with and without a topic
- [X] T006 [US2] Interval sweep in `pkg/mqttrelay`: every 15 seconds on an injectable clock, offline by `interval` for devices with `expect_every_s` past their last message, and online again on the next message
- [X] T007 [US2] Persist and restore availability with device state in `saveDevice` and `restoreState`, stale like the values, an offline restore staying offline
- [X] T008 [US2] Refuse commands in `PublishCommand` in `pkg/mqttrelay/command.go` for offline devices without `offline_commands`, with the contract's message, and move the REST command handler in `pkg/api/mqtt_device_handler.go` onto `PublishCommand`, mapping the refusal to 409
- [X] T009 [US2] Tests in `pkg/mqttrelay`: topic payloads in both forms, an unknown payload, the interval sweep with a fake clock, connection down and up with and without a topic, persistence and stale restore, the refusal and its override
- [X] T010 [US2] Nodes in `pkg/nodes/mqttnodes/devices.go`: the `mqtt-device-availability` trigger (`device`, `to`, `test_params`) with the contract's payload, `meta.availability` on `mqtt-device-get` and `mqtt-device-changed`; tests beside them
- [X] T011 [P] [US2] Record availability as an attribute sample (online 1, offline 0, unknown skipped) in `pkg/historyfwd/convert.go` with a test
- [X] T012 [P] [US2] Draw a device as unavailable while `availability` is offline or unknown in `resolveDevice` in `plugins/core/widgets/shared/entitySource.ts`, reading availability from the device list and live messages; frontend test
- [X] T013 [US2] Availability settings, an availability badge with its time and note, and a missing badge in `frontend/src/routes/settings/MQTTDevices.svelte`
- [X] T014 [US2] MCP: availability fields on `save_mqtt_device` and `list_mqtt_devices` in `pkg/mcpserver/tools_devices.go`
- [X] T015 [US2] Live: quickstart scenarios 3 and 4 on the lab broker under `karakuri-test`

## Phase 4: User story 1, discovery and adding (P1)

**Goal**: Zigbee2MQTT devices are listed and added as working MQTT devices.
**Independent check**: quickstart scenarios 1 and 2.

- [X] T016 [US1] `pkg/zigbee2mqtt/bridge.go`: types for the device list (`ieee_address`, `friendly_name`, `type`, `power_source`, `supported`, `interview_completed`, `disabled`, `definition` with `model`, `vendor`, `description`, `exposes` with `features`, `endpoint`, `category`, `access`, `value_on`, `value_off`, `value_min`, `value_max`, `values`, `unit`), tolerating a coordinator with no `definition`
- [X] T017 [US1] `pkg/zigbee2mqtt/exposes.go`: the main expose (first without `category`) and the kind table in research.md, kind options, and the list of other properties kept as fields
- [X] T018 [US1] `pkg/zigbee2mqtt/scripts.go`: generated state and payload scripts per kind as in the contract, with brightness, mired, and colour conversions, the payload script publishing to `<base>/<ieee>/set`, and no payload script for read-only kinds
- [X] T019 [US1] `pkg/zigbee2mqtt/discover.go` and `adopt.go`: read the retained list with a 5-second limit, build `DiscoveredDevice` entries with `can_add` and `device_id`, add devices through the existing save path with topics `[<base>/<friendly>]`, `availability_topic` `<base>/<friendly>/availability`, the origin fields, and a numeric suffix for a taken name; per-device failures reported, not rolled back; regenerate rebuilds kind, options, and scripts
- [X] T020 [US1] Tests in `pkg/zigbee2mqtt`: mapping of every device in both fixtures (13 lab devices; the six synthetic kinds; unsupported and interviewing not addable), generated state scripts through `mqttrelay.TestStateScript` on recorded messages, generated payload scripts through the payload runner, already-added detection, name suffixes
- [X] T021 [US1] REST routes in `pkg/api/mqtt_device_handler.go` and MCP tools `discover_zigbee2mqtt_devices`, `add_zigbee2mqtt_devices`, `regenerate_mqtt_device` in `pkg/mcpserver/tools_devices.go`, all requiring `api:connections:write`
- [X] T022 [US1] Discovery dialog `frontend/src/routes/settings/Zigbee2MQTTDiscovery.svelte`, opened from `MQTTDevices.svelte`: connection and base topic, the device table with proposed kind and fields, added and not-addable marks, multi-select add, results per device, and Regenerate on devices with an origin
- [X] T023 [US1] Live: quickstart scenarios 1 and 2 (real list read only; synthetic devices added and commanded)

## Phase 5: User story 3, renames and removals (P2)

**Goal**: added devices follow Zigbee2MQTT.
**Independent check**: quickstart scenario 5.

- [X] T024 [US3] `pkg/zigbee2mqtt/sync.go`: one subscription per connection and base in use, started from `cmd/karakuri/serve.go` and refreshed when a device with an origin is saved or deleted; rename rewrites only topics naming the old friendly name, through the save path; absence sets `missing`, presence clears it
- [X] T025 [US3] Sync tests: rename, removal, return, hand-edited scripts kept
- [X] T026 [US3] Live: quickstart scenario 5

## Phase 6: User story 4, any device (P2)

**Goal**: hand-written devices use availability topics.
**Independent check**: quickstart scenario 3 step 4 and a custom-payload device.

- [X] T027 [US4] Live: a hand-written device under `karakuri-test` with custom online and offline payloads driven by `publish_mqtt`

## Final verification and documentation

- [X] T028 Write the Zigbee2MQTT section (discovery, mapping, renames, enabling Zigbee2MQTT's availability) and an availability section in `docs/mqtt-device-state-script.md`, which MCP already serves as a guide, instead of a separate `docs/zigbee2mqtt.md`; the trigger in `docs/flow-authoring.md`, the tools in `docs/mcp.md`; set the spec status to Implemented
- [X] T029 Run `make dev`, `make test TARGET=server`, `make test TARGET=frontend`; restart `karakuri-dev.service`; browser check of quickstart scenario 6 with settled screenshots and a console check
- [X] T030 Cleanup: delete synthetic devices and test flows, publish empty retained payloads on the `karakuri-test` topics used, confirm the service is active and http://localhost:8080 answers, and report what was created and removed

## Dependencies and execution order

- T001 precedes the `pkg/zigbee2mqtt` tests; Phase 2 blocks every story.
- US2 comes first: discovery fills in availability topics, and its live
  checks need availability to show. T004 precedes T005 to T010. T011 and T012
  are independent of each other and of T008 to T010.
- US1 needs Phase 2. T016 to T018 precede T019; T020 follows them; T021 and
  T022 follow T019.
- US3 needs US1. US4 needs US2 only.
- Commit each verified phase on its own.

## Implementation strategy

Availability alone (Phase 2 and US2) is useful for every existing device.
Discovery (US1) then removes the hand-written scripts, and sync (US3) keeps
them working through renames.
