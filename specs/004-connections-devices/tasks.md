# Tasks: Home Assistant reconnect catch-up, control handoff, and MQTT delivery

**Feature**: `specs/004-connections-devices` (user stories 4 to 6, FR-023 to
FR-029, and the revised FR-010 and FR-013)
**Input**: [spec.md](spec.md), [plan.md](plan.md) (the reconnect catch-up,
control handoff, and MQTT delivery extensions), [quickstart.md](quickstart.md).

The rest of feature 004 is an implemented baseline; these tasks cover only the
three extensions. The lab Home Assistant is read only: the live check disables and
enables the connection and makes no service calls.

## Phase 1: User story 4, Home Assistant catch-up (P1)

**Goal**: after a reconnect, each entity that changed during the outage is
reported once, caught up, to triggers and dashboards.
**Independent check**: quickstart, reconnect catch-up.

- [X] T001 [US4] `pkg/integrations/homeassistant/driver.go`: a known-state map (entity to raw state with `last_updated`) updated by every `state_changed` event under the dispatcher's lock; a `StateChange{EntityID, Old, New, CaughtUp}` type and `SubscribeStateChangesDetailed(func(StateChange)) (func(), error)`, with `SubscribeStateChanges` wrapping it
- [X] T002 [US4] Catch-up in the driver: the first session seeds the map from `get_states` and reports nothing; every later session, once `state_changed` is re-subscribed, reads `get_states` and reports changed (state or attributes differ and fresh `last_updated` is newer), removed (`New` null), and added (`Old` null) entities with `CaughtUp` true; a failed read leaves the catch-up pending for the next session; log the count at info
- [X] T003 [US4] `cmd/karakuri/serve.go`: subscribe with the detailed handler and add `caught_up` to the `ha:<conn>:state:<entity>` broker message
- [X] T004 [US4] `pkg/nodes/hanodes/ha.go`: `ha-state` field **Ignore changes caught up after a reconnect** (`ignore_caught_up`, default false); `caught_up` in the message payload and meta
- [X] T005 [US4] Tests against `hatest`: seed without reports; drop the socket, change one entity three times, add one, remove one, leave one alone, reconnect, and expect three caught-up reports with the latest states; a live event arriving during the read is not reported backwards; a failed read reports nothing and catches up on the next session; `ha-state` fires once with `caught_up` and not at all with the ignore option (SC-005)

## Phase 2: User story 5, control handoff (P1)

**Goal**: one flow's control write starts another flow; loops stop at depth 8.
**Independent check**: quickstart, control handoff.

- [X] T006 [US5] `pkg/controls/store.go`: `Writer{Source, FlowID, RunID, Depth, Chain}` on `Change`; `Set` and `SetAttribute` take a `Writer`; update the API, MCP, dashboard, and node callers, with dashboard, API, and MCP writers carrying no flow
- [X] T007 [US5] Engine: `ExecContext.RunID()` and `ExecContext.ControlChain() (int, []string)` from the trigger message's `meta.control_depth` and `meta.control_chain` at run start, zero and empty for any other start; test doubles that embed `ExecContext` keep compiling
- [X] T008 [US5] `pkg/nodes/core/controls.go`: Set Control passes the run's writer, extending the chain with `<flow name> set <control name>`; Control Changed replaces `ignore_flow_writes` with `ignore_writes` (`own_flow` default, `any_flow`, `none`; saved `true` reads as `own_flow`, `false` as `none`) and `loop_limit` (default 8, 1 to 32); it sets `meta.control_depth` and `meta.control_chain` for a flow's write, records a skipped start with `control loop: <chain>` and logs at warn when the depth would exceed the limit; the message payload carries `flow_id` and `run_id` of a flow writer
- [X] T009 [US5] Tests: writers from each source; own-flow, any-flow, and none filtering; saved `ignore_flow_writes` mapping; a two-flow loop through the engine stopping at the limit with one skipped start naming the chain (SC-007); a write from a timer-started run having depth 0

## Final verification and documentation

- [X] T010 Update `docs/flow-authoring.md` (Control Changed's option and the loop limit; `ha-state` caught-up changes) and `docs/dashboard-authoring.md` if it describes Home Assistant values after reconnects; mark these tasks done
- [X] T011 Run a full `make dev`, `make test`, restart `karakuri-dev.service`, then the quickstart: the lab Home Assistant read only, test flows and controls through MCP, a dashboard checked in the browser after the reconnect with a console check; remove the test objects and report them

Live checks on 2026-11-05, with the lab Home Assistant read only: disabling the
connection for 90 seconds and enabling it caught up 88 changed entities; a test
flow watching `sensor.kiki_power`, `sensor.office_temperature`, and `sun.sun`
ran once, caught up, for the one that changed (593.8 to 581.8 W) and not for the
others; an open dashboard showed the sensor's current value after a second
outage without a reload. A test flow setting a text control started a second
flow once with the writer's flow and run; two flows writing each other's number
controls ran nine times and refused the tenth, recorded as a canceled run naming
the chain.

## Phase 3: User story 6, MQTT device delivery (P2)

**Goal**: devices choose QoS, retain commands, see retained messages, and set
their script time limits.
**Independent check**: quickstart, MQTT delivery. Live checks use only the
`karakuri-test` base topic on the lab broker, never `zigbee2mqtt/`.

- [X] T012 [US6] `pkg/models/mqtt_device.go`: columns `state_qos` (int, default 0), `command_qos` (int, default 0), `command_retain` (bool, default false), `state_timeout_ms` (int, default 0 meaning 250), `payload_timeout_ms` (int, default 0 meaning 2000), and helpers returning the effective limits; `pkg/mqttrelay` `Definition` and `View` carry them, `ValidateDefinition` refuses QoS outside 0 to 2, a state limit outside 50 to 5000, and a payload limit outside 100 to 10000 for MQTT transport devices, naming the range
- [X] T013 [P] [US6] `pkg/integrations/mqtt/driver.go`: `Message{Topic, Payload, Retained, QoS}` from paho's `PublishReceived` packet; `SubscribeMessages(filter, qos, func(Message))` with `Subscribe` wrapping it; delivery carries the message; record the granted QoS per filter from SUBACK reasons below 0x80 and expose `GrantedQoS(filter) (byte, bool)`
- [X] T014 [US6] `pkg/mqttrelay/device.go` and `availability.go`: subscribe state and availability topics at `state_qos` with `SubscribeMessages`; pass `retained` into `decode` and the preamble `const {binary, state, retained} = meta;`; run the state script under the device's limit; `TestStateScript` takes `retained` and a limit; `View.StateQoSGranted` when the broker granted less than `state_qos`
- [X] T015 [US6] `pkg/mqttrelay/command.go` and `publish.go`: `RunPayloadScript` under the device's payload limit; `ReadPayloadResult` reads optional `qos` (0 to 2) and `retain` (boolean), refusing other values with the reason; `PublishCommand` publishes with them, else the device's `command_qos` and `command_retain`; `CommandResult` and the device's event log entry carry the QoS and retain used
- [X] T016 [US6] REST (`pkg/api/mqtt_device_handler.go`), MCP (`save_mqtt_device`, `list_mqtt_devices`, `test_mqtt_device_script` with `retained`), and the script test route accept and return the new fields
- [X] T017 [US6] `frontend/src/routes/settings/MQTTDevices.svelte`: a **Delivery** section for MQTT devices with state QoS and command QoS selects (0 at most once, 1 at least once, 2 exactly once), a retain commands switch, and the two time limits in milliseconds with 250 and 2000 as placeholders; the state script test sample carries `retained`; the editor notes a granted QoS lower than asked
- [X] T018 [US6] Tests: driver against the embedded broker, a retained message delivered on subscribe with `Retained` true and a live one false, and the granted QoS recorded; relay, device defaults and per-message `qos` and `retain` reaching the publish, invalid values refused, the state script reading `retained`, a busy script succeeding under a raised limit and failing under the default, and out-of-range limits refused on save
- [X] T019 [US6] Update `docs/mqtt-device-state-script.md` (`retained`, the time limit) and `docs/mqtt-device-payload-script.md` (`qos`, `retain`, clearing a retained command, the time limit); full `make dev` and `make test`; restart `karakuri-dev.service`; live quickstart under `karakuri-test/deliverytest` with a browser check of the Delivery section and a console check; clear the retained topics, remove the test device, and report what was created and removed

Live checks on 2026-11-06 on the lab broker under `karakuri-test/deliverytest`:
a device with state QoS 1, command QoS 1, retain on, and a 1000 ms state limit
read a state published retained before it subscribed as replayed and the next
live one as live; a command was held on the broker as a retained message, and a
command whose payload script returned `retain: false` was not; the broker
granted the asked QoS, so no lower grant was shown. The editor's Delivery
section showed the saved values; the console's only entry was the session
refresh. Writing the tests found that the script test, payload script, and
publish template paths never stopped a slow script, since the JavaScript
runtime watches only its context's deadline; they now set one. The test device
was deleted and the retained topics cleared.

## Dependencies

- Phase 1 and Phase 2 are independent and can be built in either order.
- In Phase 3, T012 and T013 come first and in parallel; T014 and T015 build on
  both; T016 and T017 follow T014 and T015.
- T001 precedes T002 to T005; T006 and T007 precede T008 and T009.
- Commit each phase on its own.
