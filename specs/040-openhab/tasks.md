# Tasks: openHAB connection

**Feature**: `specs/040-openhab`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

All live checks use the disposable openHAB from `test/openhab/compose.yaml`;
no other openHAB is touched. Test objects are named `openHAB test`.

## Phase 1: Shared preparation

- [X] T001 `test/openhab/compose.yaml` (`openhab/openhab:5.2.3` on `19080`, `conf/items` mounted read only, data in an anonymous volume), `test/openhab/README.md`, and Makefile targets `openhab-up` (compose up, wait for `/rest/`, create user `admin` with password `karakuri-test` and an API token through the Karaf console on stdin, write it to `build/openhab-token`), `openhab-down` (compose down with volumes), and `test-openhab` (`TARGET=openhab`, `go test -tags karakuri_openhab ./test/openhab/...`)
- [X] T002 [P] `pkg/integrations/openhab/openhabtest`: a fake openHAB on httptest serving `GET /rest/` (version), `GET /rest/items` (with `metadata=semantics`), `POST /rest/items/{name}` (records commands, `400` for a wrong type when told, `401` for a wrong token), `GET /rest/items/{name}/state`, and `GET /rest/events` as a server-sent stream the test pushes `statechanged`, `added`, `removed`, and `updated` events and `alive` keepalives into, with `Drop()` to cut the stream

## Phase 2: Foundation, connection and item copy

Every user story phase depends on this phase.

- [X] T003 `pkg/integrations/openhab/kind.go`: kind `openhab`, config `base_url` (required, `http` or `https`, trailing `/rest` or slash trimmed) and `token` (secret, optional), `Validate`, and `Probe` answering `openHAB <version>, <n> items, authenticated|anonymous`, or `unauthorized: check the API token`, `not found: is this an openHAB server?`, or the network error
- [X] T004 `pkg/integrations/openhab/items.go`: `Item` (name, label, type with dimension, group type, state, state description with minimum, maximum, step, options, read only, pattern; tags; group names; semantics value and relations) decoded from the REST list and event payloads; `Locate` giving location and floor per research.md (`hasLocation`, else the equipment's through `isPointOf`, else the nearest `Location_*` ancestor group; floor from `isPartOf` when it is a `Location_*_Floor_*`)
- [X] T005 `pkg/integrations/openhab/driver.go`: HTTP client with the bearer token when set; on start and every reconnect open `/rest/events` for `statechanged`, `added`, `removed`, `updated`, then read `/rest/items?recursive=false&metadata=semantics`; an SSE reader (`event:`/`data:` lines, JSON payload strings); 30 s without any event is a dead stream; status connecting, connected, error with the reason; backoff 1 s doubling to 30 s; the item copy updated from events; `Items()`, `Item(name)`, `WatchItems(fn)`, `SubscribeChanges(fn(ItemChange{Name, Old, New, Item, CaughtUp}))`, `SendCommand(ctx, name, command)` reporting `401`/`403` as authorization and other statuses with openHAB's message
- [X] T006 Catch-up in the driver: a known-state memory per connection and URL, seeded by the first read without reports; every later read reports each item whose state differs, plus added and removed items, once with `CaughtUp`; a failed read leaves it for the next connect
- [X] T007 `pkg/connops/drivers.go` `OpenHABDriver(id)`; register the kind in `cmd/karakuri/serve.go`
- [X] T008 Tests on `openhabtest`: connect and version; items with locations for tagged, equipment, and untagged items; event updates to the copy; a wrong token unauthorized; a dropped stream reconnecting; a stream silent for 30 s treated as dead (with a shortened interval); catch-up reporting changed, added, and removed items once and nothing for unchanged ones; the first connect reporting nothing

## Phase 3: User story 1, connect and see items (P1)

**Goal**: the connection and its item list by location.
**Independent check**: quickstart scenario 1.

- [X] T009 [US1] `pkg/api`: `GET /api/connections/{id}/openhab/items` (connection read) with label, type, state, location and floor labels, read only, sorted by floor, location, label, no location last
- [X] T010 [P] [US1] `pkg/mcpserver`: `list_openhab_items` (connection read; `location` by name or ID, `type`, `filter`) and `send_openhab_command` (connection invoke)
- [X] T011 [US1] Tests: route and MCP listing order and filters on the fake; permission checks

## Phase 4: User story 2, items as devices (P1)

**Goal**: openHAB items drawn and commanded as devices.
**Independent check**: quickstart scenario 2.

- [X] T012 [US2] `pkg/integrations/openhab/mapping.go`: per research.md, item type to kind and options (`Switch` boolean; `Contact` boolean read only with Open and Closed labels; `Dimmer` light with brightness; `Color` light with brightness and colour; `Rollershutter` cover with position; `Number` number with minimum, maximum, step, and the unit from a dimensioned state, read only when the description says; `String` select with options, else text; `DateTime` datetime; `Player` media with skip, without volume, mute, stop, seek, source, power; `Location` location; `Group` with a base type as that type; others unsupported with the reason), `Values(item)` (NULL and UNDEF give no value; position = 100 − state; HSB to hex and brightness; `21.5 °C` to 21.5), and `Command(item, attribute, value)` (ON, OFF, a percent, `h,s,b`, UP, DOWN, STOP, 100 − position, a number, a string, RFC 3339, PLAY, PAUSE, NEXT, PREVIOUS) refusing values outside the state description's minimum, maximum, and options before anything is sent
- [X] T013 [US2] Driver `WatchEntity` (current state, then changes, mapped through `Values`), `CommandEntity` (mapping `Command`, then `SendCommand`), and `WatchEntityAvailability` (unknown while NULL or UNDEF, false when the item is gone)
- [X] T014 [US2] `pkg/mqttrelay` transport `openhab` (entity transport, label `openHAB`, writable by the mapping, schema enum); `pkg/entitydevices/sources.go` openHAB source with location groups and `SourceFor`; `cmd/karakuri/serve.go` rebinding devices and attaching the tracker on driver start
- [X] T015 [US2] `frontend/src/routes/settings`: **Add from openHAB** beside the other sources, entities grouped by location
- [X] T016 [US2] Tests: the mapping table both ways for every type; devices added, commanded, following state, NULL as unknown, a removed item marking its device missing, a forbidden value refused with nothing sent, all on the fake

## Phase 5: User story 3, flows on items (P2)

**Goal**: item triggers, commands, and reads in flows.
**Independent check**: quickstart scenario 3.

- [X] T017 [US3] `pkg/nodes/openhabnodes`: plugin with folder openHAB and an `openhab-item` field control completing against the item list; `openhab-item-changed` (items, groups at any depth, location, item type, from, to, ignore caught-up changes; payload `{item, type, old, new, label, location, caught_up?}`; membership read at each change), `openhab-send-command` (item or group, templated command; a refusal fails the node with the reason), `openhab-get-item` (pure value); registered in serve
- [X] T018 [US3] `ConfigForm.svelte`: the `openhab-item` control completing item names with labels and locations, grouped by location
- [X] T019 [US3] Tests on the fake: triggers by item, group member, location, type, from and to; caught-up marking and the ignore option; send command and its refusal; get item

## Phase 6: User story 4, catch-up (P2)

**Goal**: changes during an outage reach triggers, devices, and dashboards once.
**Independent check**: quickstart scenario 4.

- [X] T020 [US4] Caught-up changes reach triggers and devices, and through devices dashboards; a separate broker topic was not needed, since no widget binds openHAB items except as devices. Tested: a reconnect after changes on the fake fires an armed trigger once per changed item, marked caught up

## Final verification and documentation

- [X] T021 `test/openhab` tagged suite against the container: connect with the token from `build/openhab-token`, list items with locations, command a switch, dimmer, blind, and number and read openHAB's states back, see a state change arrive within a second, and a forbidden volume refused
- [X] T022 Write `docs/openhab.md` (connection and token, locations, the mapping table, devices, nodes, catch-up, testing with `make openhab-up`), serve it as a guide and MCP resource, and update `docs/flow-authoring.md`, `docs/dashboard-authoring.md`, and `docs/mcp.md`; set the spec status to Implemented
- [X] T023 Full `make dev`, `make test`, `make openhab-up`, `make test TARGET=openhab`; restart `karakuri-dev.service`; live quickstart through MCP and the browser (card with item devices, screenshot, console check); remove the test connection, devices, flow, and dashboard; `make openhab-down`; report what was created and removed

Live checks on 2026-11-06 against openHAB 5.2.3 from `make openhab-up`: the
connection tested as `openHAB 5.2.3, 17 items, authenticated` with the token
masked, and a wrong token as unauthorized; `list_openhab_items` for Kitchen
listed its blind, switch, and temperature on the ground floor; five items
added as devices took brightness 40 (openHAB 40), blind position 30 (openHAB
70), and refused a volume of 70 against the item's maximum of 50 before
sending; a temperature set in openHAB reached its device. A flow on the
kitchen location for switches turning on ran once for the kitchen switch and
not for the living-room dimmer; with the connection disabled, the switch
turned on in openHAB, and enabling it ran the flow once, caught up, and
updated the devices. A card drew a light, a cover, a toggle, a reading, and a
slider, a press of the switch reached openHAB, and the console held only the
session refresh. The tagged suite passed against the container. A temperature
item whose state was NULL when added first came without a unit; openHAB 4
and later report `unitSymbol` on the item, which the mapping now reads. Test objects were deleted and the container removed
with `make openhab-down`.

## Dependencies and execution order

- T001 and T002 first, in parallel; Phase 2 precedes every story; T004 before
  T005, T005 before T006.
- US2 needs the item copy (T005) and the mapping (T012) before devices
  (T013, T014).
- US3 needs Phase 2; US4 needs Phase 2 and the devices of US2 for its broker
  topic check.
- Commit each verified phase on its own; run a full `make dev` before
  committing frontend code.

## Parallel opportunities

- T001 beside T002; T010 beside T009; T012 beside T009 to T011; T018 beside
  T017.

## Implementation strategy

The connection with its item copy and catch-up is the base; items as devices
(US2) is the first deliverable that draws on a card, then the flow nodes and
the catch-up broker topic.
