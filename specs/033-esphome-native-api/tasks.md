# Tasks: ESPHome native API

**Feature**: `specs/033-esphome-native-api`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

No ESPHome node announces itself on the development subnet. Live scenarios
run against a real node only when the user provides one; otherwise the
simulated node in tests is the evidence and the report says so.

## Phase 1: Shared preparation

- [X] T001 Add `github.com/mycontroller-org/esphome_api` v1.4.0 and `github.com/flynn/noise` to `go.mod` through the Makefile build, and confirm the server binary stays pure Go (`CGO_ENABLED=0` build passes)
- [X] T002 Simulated node in `pkg/integrations/esphome/esphometest/node.go`: a TCP listener speaking plaintext framing and the Noise `NNpsk0_25519_ChaChaPoly_SHA256` responder handshake (with `flynn/noise`), answering Hello, Connect (password), DeviceInfo, ListEntities (configurable entity set covering switch, light, cover, fan, lock, climate, number, select, text, button, sensor, binary_sensor, text_sensor, datetime, event, and one media player), SubscribeStates (pushing current states), SubscribeHomeassistantServices, Ping, and recording command requests; with hooks to push a state, send a node action call, go silent, and drop the connection

## Phase 2: Foundation, connection and device transport

Every user story phase depends on this phase.

- [X] T003 `pkg/integrations/esphome/kind.go`: kind `esphome` with `host` (required), `port` (default 6053), `encryption_key` and `password` (secrets); validation that `encryption_key` decodes as 32 bytes of base64; probe returning `<name> (<board>), ESPHome <version>, MAC <mac>, <n> entities`
- [X] T004 `pkg/integrations/esphome/driver.go`: session on the library's `connection` and `client` packages: connect, Hello, Connect with password when set, DeviceInfo, list entities (at most 500), SubscribeStates, SubscribeHomeassistantServices; entity table by key and object ID; state cache; watchers per object ID and for node action calls; ping every 20 seconds, drop after 60 seconds silent; reconnect with backoff 1 second doubling to 60; status through the existing connection statuses with the node's reason
- [X] T005 [P] `pkg/integrations/esphome/mdns.go`: browse `_esphomelib._tcp.local` for 3 seconds over `x/net/dns/dnsmessage` and `x/net/ipv4`, reading SRV, A, and TXT (`mac`, `version`, `board`, `friendly_name`) into discovered nodes; resolve one instance name; the driver re-resolves the node's name when its host stops answering
- [X] T006 Add `Transport` (default `mqtt`), `EntityKey`, `EntityType` to `models.MQTTDevice`; skip topic and script validation for `esphome` devices in `mqttrelay.ValidateDefinition`; refuse a second device with the same connection and `entity_key`; add the fields to the view
- [X] T007 `mqttrelay` transport branch: an `EntityTransport` interface (`WatchEntity(objectID string, fn func(values map[string]any)) (func(), error)`, `Command(ctx, objectID, field string, value any) error`, `Entity(objectID string) (EntityInfo, bool)`); `start` and `Rebind` watch the entity for `esphome` devices and merge values through the same path `receive` uses after decoding (merge, stale clear, publish, persist, fan out, availability)
- [X] T008 `mqttrelay.Command(ctx, device, field, value, on, previous)`: kind check, offline refusal, then the MQTT payload-script path or the ESPHome transport; move the REST handler, the MCP `command_mqtt_device` tool, and `mqtt-device-set` onto it; `Commandable` is a payload script for MQTT and a writable entity type for ESPHome
- [X] T009 Tests: driver against the simulated node over plaintext and Noise (handshake, wrong key, password), entity listing, state cache, ping, silence drop and reconnect; `mqttrelay.Command` for both transports; existing MQTT device tests still pass unchanged

## Phase 3: User story 1, connect a node (P1)

**Goal**: an author finds a node and connects with its key.
**Independent check**: quickstart scenario 1.

- [X] T010 [US1] Plugin `pkg/nodes/esphomenodes/plugin.go` registering the kind; wire it into the built-in plugin list in `cmd/karakuri/serve.go`
- [X] T011 [US1] `GET /api/connections/esphome/nodes` and MCP `discover_esphome_nodes`, needing `api:connections:write`
- [X] T012 [US1] Connection form host picker: a field control `esphome-host` on `host` in the kind schema, rendered in the connection form as a text input with a "Find nodes" list filled from the browse route, filling host and port
- [X] T013 [US1] mDNS parse tests from recorded answer packets; kind validation and probe tests through the simulated node; masking of key and password through REST and MCP

## Phase 4: User story 2, entities as devices (P1)

**Goal**: a node's entities become working devices.
**Independent check**: quickstart scenario 2.

- [X] T014 [US2] `pkg/integrations/esphome/entities.go`: entity type to kind and options per the research table (light colour modes to features, kelvin from mireds, fan speed count to percentage, climate modes and ranges, number bounds, select options, sensor unit and accuracy, binary sensor labels by device class); state responses to the kind's values; the kind's field and value to the type's command request; media player, alarm panel, siren, valve, update, water heater, infrared, radio frequency, and camera not addable
- [X] T015 [US2] `pkg/esphomedevices`: list a connected node's entities against devices (`object_id`, `name`, `type`, `kind`, `commandable`, `can_add`, `reason`, `device_id`); add through `mqttrelay.SaveDefinition` with name `<node> <entity>` and a numeric suffix when taken; on each entity list after a connect, set `missing` on devices whose object ID is gone and clear it on return
- [X] T016 [US2] REST `GET`/`POST /api/mqtt_devices/esphome` and MCP `discover_esphome_entities`, `add_esphome_entities`
- [X] T017 [US2] `frontend/src/routes/settings/ESPHomeDiscovery.svelte` opened from the device settings, all addable entities ticked; the settings page and nav say Devices, with a transport column and the ESPHome devices' entity type shown
- [X] T018 [US2] Tests: mapping per entity type with state and command round trips through the simulated node; device start, merge, availability unknown while the node is disconnected; missing and return; add with name suffixes

## Phase 5: User story 3, flows (P1)

**Goal**: flows read, command, call node actions, and react to node action calls.
**Independent check**: quickstart scenario 3.

- [X] T019 [US3] `pkg/integrations/esphome/actions.go` and nodes in `pkg/nodes/esphomenodes`: `esphome-action` (connection, action picker, JSON args with `{{ }}`, argument type check for bool, int, float, string, and arrays) and `esphome-node-action` trigger (connection, optional action filter, payload per contract); REST `POST /api/connections/{id}/esphome/actions/{name}` and MCP `call_esphome_action` needing `api:connections:invoke`
- [X] T020 [US3] Relabel the device nodes in `pkg/nodes/mqttnodes/devices.go` to Device Value, Command Device, Device Changed, and Device Availability with descriptions naming MQTT and ESPHome devices; type IDs unchanged
- [X] T021 [US3] Node tests against the simulated node: action call with checked arguments, a wrong argument type refused, the trigger on a node action call

## Phase 6: User story 4, node down (P2)

**Goal**: a dead node is unavailable everywhere and recovers alone.
**Independent check**: quickstart scenario 4.

- [X] T022 [US4] Test the full path with the simulated node going silent: connection error within 60 seconds, devices unknown, the availability trigger firing, reconnection after the node returns, states fresh, devices online; and the stale restore of an ESPHome device's state after a runner restart

## Final verification and documentation

- [X] T023 Write `docs/esphome.md` (connecting, keys, discovery, entity mapping, devices, actions, offline behaviour, limits), serve it as an MCP guide and through the guide route, point to it from `docs/mqtt-device-state-script.md`, and update `docs/mcp.md` and `docs/flow-authoring.md`; set the spec status to Implemented
- [X] T024 Run a full `make dev` (widgets included), `make test TARGET=server`, and `make test TARGET=frontend`; restart `karakuri-dev.service`; browser check of the connection form and the discovery dialog against the simulated node or a real one, with settled screenshots and a console check
- [X] T025 Live quickstart against a real node if the user provided one; otherwise record that the simulated node is the evidence. (No node was provided: the simulated node in tests, plaintext and Noise, is the evidence; the live server was checked only against an unreachable node for its error paths.) Remove test objects, confirm the service is active and http://localhost:8080 answers, and report what was created and removed

## Dependencies and execution order

- T001 precedes everything; T002 precedes the tests of every phase.
- Phase 2 blocks the stories: T003 and T004 precede T005 to T009; T006
  precedes T007 and T008.
- US1 and US2 can proceed in parallel after Phase 2; US3 needs US2's devices
  only for its live check; US4 needs US2.
- Commit each verified phase on its own; run a full `make dev` before each
  commit that touches shared widget code.

## Implementation strategy

The connection and transport (Phase 2) with entities as devices (US2) are the
first deliverable: a node's entities on dashboards. Discovery by mDNS (US1),
node actions (US3), and the offline checks (US4) follow.
