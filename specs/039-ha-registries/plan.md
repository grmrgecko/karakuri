# Design plan: Home Assistant areas and devices

**Feature**: `specs/039-ha-registries` | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

## Summary

Each Home Assistant driver reads the floor, area, device, entity, and label
registries after authenticating and keeps an in-memory copy current from the
registries' update events, rereading everything after a reconnect. The copy
answers three questions: how to show an entity (name, area, floor, device),
which entities a group target names, and whether a call would reach anything.

Pickers get grouping from richer `/ha/entities` rows and a new `/ha/registry`
route; a new `ha-target` field type turns the call service node's existing
free-text area, device, and label fields, plus a new floor field, into
pickers without changing what is stored. State triggers and get state accept
the same targets and resolve membership per event from the copy, so a lamp
moved into the living room counts from its next change.

Home Assistant entities become devices with a `homeassistant` transport. The
domain mapping the dashboard faces use is ported to Go and held to the
TypeScript copy by one shared fixture both test suites read, so a device and
a card row bound to the same entity cannot disagree. [research.md](research.md)
records the commands and decisions.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new dependencies.
- Components:
  - `pkg/integrations/homeassistant/registry.go`: `Registry` (floors, areas,
    devices, entities, labels, per-registry status), load after auth and
    after reconnect, debounced reread on update events, `Resolve(target)`,
    `Describe(entityID)`; driver accessors `Registry()`, `WatchRegistry`.
  - `pkg/integrations/homeassistant/mapping.go` and
    `testdata/mapping.json`: kind, options, writable, value, command per
    domain; `driver.go` gains `WatchEntity`, `CommandEntity`,
    `WatchEntityAvailability`.
  - `pkg/integrations/homeassistant/hatest`: registry list commands, registry
    update events, entity rename.
  - `pkg/flow/meta.go` and `validate.go`: field type `ha-target`.
  - `pkg/nodes/hanodes/ha.go`: group targets on `ha-state` and
    `ha-get-state`; `floor_id` and the empty-target refusal on
    `ha-call-service`.
  - `pkg/api/ha_handler.go`, `connection_handler.go`: richer entities,
    `/ha/registry`.
  - `pkg/mcpserver`: `get_ha_states` filters, `get_ha_registry`.
  - `pkg/entitydevices/sources.go`: Home Assistant source;
    `pkg/mqttrelay/transport.go`: per-transport writable set and the
    `homeassistant` transport; rename handling in the tracker.
  - Frontend: `ConfigForm.svelte` (`ha-target` picker, grouped entity
    suggestions), `EntityPicker.svelte` (grouping, hidden toggle),
    entities card config (**Add an area**), devices settings (**Add from Home
    Assistant**), `entitySource.ts` tested against the shared fixture.
  - Docs: `docs/flow-authoring.md` (targets, triggers), new
    `docs/homeassistant.md` (registries, devices, status), dashboard
    authoring (picker, area fill), `docs/mcp.md`.
- Storage: no schema change; Home Assistant devices are `mqtt_devices` rows.

## Constitution check

- **Failures**: a registry that cannot be read leaves the connection working
  and says why in its status; an empty group target fails the call service
  node with a reason; a refused device write fails before the service call.
- **Drafts and deployment**: group targets are resolved at run time, so a
  deployed flow follows registry changes with no redeploy; stored field
  values keep their current form.
- **Permissions**: registry routes need connection read; dashboard service
  calls keep refusing group targets; Home Assistant devices use the existing
  device permissions and dashboard device capability.
- **Existing state**: existing flows, dashboards, and devices are unchanged;
  `entity_id` on `ha-state` becomes optional only when a group target is set.
- **Verification**: `make dev`, `make test`, registry and resolution tests on
  `hatest`, the shared mapping fixture in Go and vitest, device command tests
  on `hatest`, and a read-only live check on the lab Home Assistant.

## Design and contracts

### Registry lifecycle

```text
authenticate -> resubscribe -> load registries (5 reads, unknown_command = empty + status)
             -> subscribe *_registry_updated -> debounce 2 s -> reread that registry -> notify watchers
reconnect    -> load all again (events during the outage are lost)
```

### Trigger match

```text
state_changed(entity) -> registry.Describe(entity) -> matches any of entity/area/floor/device/label
                      -> domain and device_class filters -> from/to/attribute filters as today
```

### Device command

```text
Device Command -> controls.CoerceAttribute (kind) -> mapping.Command(state, attribute, value)
               -> CallService(domain, service, data, target{entity_id}) -> value changes on state_changed
```

[contracts/interfaces.md](contracts/interfaces.md) lists the routes, fields,
nodes, MCP tools, and widget changes.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Registry load and derived area on `hatest`; `/ha/entities` fields; picker grouping in the browser on the lab Home Assistant |
| US2 | Trigger on area, floor, device, label with domain and class; membership change by update event without re-arming; empty target refusal; get state group payload |
| US3 | Shared mapping fixture in Go and vitest; add, watch, command, refuse, rename, and missing on `hatest`; a read-only lab sensor added as a device |
| US4 | Area fill in the browser on the lab Home Assistant, discarded |

## Risks and open decisions

- `list_for_display` is the Home Assistant frontend's own command and its
  short keys may change; the reader tolerates missing keys and the fixture
  pins the current shape.
- Two mapping copies can still drift in behavior the fixture does not cover;
  every domain and write in the mapping gets a fixture case.
- Very large registries reread on every update event burst; the debounce
  bounds it to one read per registry per two seconds.
