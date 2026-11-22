# Design plan: openHAB connection

**Feature**: `specs/040-openhab` | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

## Summary

A new `openhab` connection kind drives openHAB's REST API and its server-sent
event stream. The driver holds a copy of every item, kept current from item
events, follows state changes, and catches up after a reconnect the way the
Home Assistant driver does. Items become devices with a new `openhab`
transport through the entity device machinery features 033 and 039 built,
with a fixed item-type mapping (research.md) that also enforces the item's
state description, which openHAB itself does not. Three flow nodes cover
triggers by item, group, or location, raw commands, and reads.

Everything was probed against openHAB 5.2.3 in Docker; research.md records the
formats seen. Unit tests use an `openhabtest` fake; a tagged suite and the
quickstart run against the real container, started by new make targets.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new Go dependency: the
  event stream is read with `net/http` and `bufio`.
- Components:
  - `pkg/integrations/openhab`: `kind.go` (connection kind, config, probe),
    `driver.go` (HTTP client, SSE reader, reconnect, status, item copy,
    fan-out, catch-up memory), `items.go` (item model, semantic location),
    `mapping.go` (item type to kind, options, values, commands, checks),
    `openhabtest` (fake server).
  - `pkg/connops/drivers.go`: `OpenHABDriver(id)`.
  - `pkg/mqttrelay`: transport `openhab` (entity transport, label, writable
    by mapping).
  - `pkg/entitydevices/sources.go`: openHAB source and `SourceFor`.
  - `pkg/nodes/openhabnodes`: the three nodes, `ha-target`-style item picker
    (`openhab-item` control completing against the item list).
  - `pkg/api`: `GET /connections/{id}/openhab/items`; MCP
    `list_openhab_items`, `send_openhab_command`.
  - `cmd/karakuri/serve.go`: register the kind and plugin; on driver start,
    rebind devices and attach the tracker.
  - Frontend: **Add from openHAB** in Devices settings, the item picker in
    `ConfigForm.svelte`.
  - `test/openhab`: `compose.yaml`, `conf/items/karakuri.items`, the tagged
    suite, `README.md`; Makefile targets `openhab-up`, `openhab-down`,
    `test-openhab`.
  - Docs: `docs/openhab.md` served as a guide and MCP resource; flow and
    dashboard authoring mentions; `docs/mcp.md`.
- Storage: no schema change; the connection config is a new kind's JSON.

## Constitution check

- **Failures**: a refused command reports openHAB's status and reason; a
  command outside the state description is refused before sending; the stream
  reconnects with backoff and reports its error in the connection status.
- **Secrets**: the token is a declared secret, encrypted and masked, sent only
  as a bearer header.
- **Permissions**: items need connection read, commands connection invoke;
  devices use the existing device permissions and dashboard capability.
- **Existing state**: none changed.
- **Verification**: `make dev`, `make test`, the fake-backed unit tests, the
  tagged suite against Docker, and a live quickstart in the browser.

## Design

### Driver lifecycle

```text
start -> GET /rest/ (version) -> open SSE (statechanged, added, removed, updated)
      -> GET /rest/items?metadata=semantics -> compare with memory (catch-up) -> connected
SSE event -> update item copy -> fan out change (live)
no event for 30 s (alive every 10 s) or read error -> error status -> backoff 1 s..30 s -> start
```

The stream is opened before the item read so no change falls between them;
changes that arrive during the read are applied after it, by sequence.

### Item location

`hasLocation`, else the equipment's location through `isPointOf`, else the
nearest `Location_*` ancestor group; floor from the location's `isPartOf`.

### Device command

```text
Device Command -> controls.CoerceAttribute (kind) -> mapping.Command(item, attribute, value)
              -> state description check -> POST /rest/items/{name} text/plain
```

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Fake: connect, version, item list with locations, auth failure, stream drop and reconnect. Docker: the tagged suite connects and lists |
| US2 | Mapping table tests for every type both ways; device add, command, state follow, NULL as unknown, removal as missing on the fake; Docker: commands change openHAB's states |
| US3 | Node tests on the fake: trigger by item, group, location, type, from, to; send command; get item |
| US4 | Fake: drop the stream, change items, reconnect, one caught-up report each; Docker quickstart |

## Risks and open decisions

- openHAB's semantic tags are optional; houses without them get no locations,
  and pickers fall back to a flat list.
- `Number` items without a state description have no bounds; the device takes
  any number, as openHAB does.
- The Player type has no volume or track details; richer players are
  openHAB things with separate items, which can be added as separate devices.
