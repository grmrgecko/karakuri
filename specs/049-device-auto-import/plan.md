# Design plan: Device auto import

**Feature**: `specs/049-device-auto-import` | **Date**: 2026-11-22 | **Spec**: [spec.md](spec.md)

## Summary

A connection of kind MQTT, ESPHome, Frigate, Home Assistant, or openHAB gains
auto import settings in its config. A new server component, the importer,
follows every running connection with auto import on: it adds devices its
source reports, records when each auto-imported device was last reported, and
removes the ones not reported within "Remove after". MQTT connections gain a
Home Assistant MQTT discovery reader that turns a discovery config into an
ordinary MQTT device definition with generated state and payload scripts, the
same shape Zigbee2MQTT discovery (feature 032) produces. The existing pickers
stay; MQTT gains a picker over discovery configs.

## Technical context

- Language and dependencies: Go server, Svelte frontend; no new dependencies.
- Components:
  - `pkg/integrations`: shared auto import config fields and validation
    (`autoimport.go`), added to each device-capable kind's `ConfigSpec`.
  - `pkg/hadiscovery`: discovery topic parsing, abbreviation expansion, the
    Jinja subset translator, and component-to-definition mapping.
  - `pkg/autoimport`: the importer (settings, MQTT and entity followers,
    sweep, removal, tombstones, status).
  - `pkg/entitydevices`, `pkg/zigbee2mqtt`, `pkg/mqttrelay`: add paths take an
    auto origin; entities carry filter places.
  - `pkg/api`, `pkg/mcpserver`: import status and the discovery picker.
  - `frontend/src/routes/settings`: connection import status, picker
    visibility, delete warning, MQTT discovery picker.
- Storage: `mqtt_devices` gains `auto_imported`, `discovery_key`, and
  `last_reported_at`; new table `auto_import_tombstones`. Settings live in the
  connection's encrypted config. Candidate lists, live activity, and import
  status are memory only.
- Limits: sweep every minute; "Remove after" at least one hour; picker read
  of retained discovery configs waits 3 seconds; skipped list keeps the
  latest 200 entries per connection.

## Constitution check

- Failures: an import or removal that fails is logged and reported in the
  import status; the next report or sweep retries. Removal deletes only
  auto-imported devices of connections whose settings ask for it.
- Shared rules: REST, MCP, and the interface read the same status and use the
  same discovery add path through `mqttrelay.SaveDefinition`.
- Permissions: settings are connection config, so editing them needs Edit on
  the connection. The importer acts as the server (`events.System()`), like
  the existing Zigbee2MQTT sync and entity tracker.
- Stored state: greenfield; new columns and table are created by
  `AutoMigrate`. No encrypted material changes.
- Verification: `make test TARGET=server`, `make test TARGET=frontend`,
  `make dev`, restart `karakuri-dev.service`, live MQTT check against a local
  broker.

## Design and contracts

### Settings

Config keys, all optional (see [contracts/interfaces.md](contracts/interfaces.md)):
`auto_import` (bool), `remove_after` (duration such as `12h` or `7d`, empty
for never), MQTT `discovery_prefix` (default `homeassistant`) and
`zigbee2mqtt_base` (default `zigbee2mqtt`, `off` to turn it off), Home Assistant
`import_areas` and `import_domains`, openHAB `import_locations` and
`import_types`. Filters are comma-separated and case-insensitive; an area or
location matches its ID, name, or floor. `integrations.ValidateAutoImport`
runs from `connops.validateConfig` for every kind.

### Importer

`autoimport.Manager` is built in `serve` and called from
`connMgr.OnDriverStart`. `Attach(id, driver)` reads the connection's
settings and replaces any earlier follower for it. A connection update
restarts its driver, so settings changes take effect through `Attach`.

- Entity sources (ESPHome, Frigate, Home Assistant, openHAB): watch the
  entity list through `entitydevices.Source.WatchEntityList`; each list adds
  the entities that pass the filters and have no device, through
  `entitydevices.AddAuto`.
- MQTT: `SubscribeMessages` on `<prefix>/#` reads discovery configs and on
  `<base>/bridge/devices`, `<base>/+`, and `<base>/+/availability` reads
  Zigbee2MQTT. Each discovery config's state, attribute, and availability
  topics are subscribed for activity. A config with an empty payload marks
  its device missing. Configs from Zigbee2MQTT (`device.identifiers` starting
  `zigbee2mqtt_`) are skipped while a Zigbee2MQTT base topic is set.
- Live activity is a message the broker did not replay from its retained
  store: a discovery config, a state message, or an availability message
  that matches the online payload. Retained replays import unknown devices
  but neither refresh the clock nor clear a tombstone.
- The sweep (one minute) tracks when each connection last connected,
  refreshes `last_reported_at` for entity devices present while connected
  and for MQTT devices with newer live activity, re-imports deleted devices
  their source still reports, and removes expired devices: the reference
  time is the later of `last_reported_at` (or creation) and the connection's
  last connect, except ESPHome, which uses `last_reported_at` alone.
  Non-ESPHome connections that are down are skipped.
- Removal deletes through `mqttrelay.DeleteDevice` and the history cleanup
  hook, records a device event, and for MQTT sources writes a tombstone.

### Home Assistant discovery mapping

`hadiscovery.Parse(prefix, topic, payload)` returns zero or more `Config`
values (device-based configs yield one per component). `Map(cfg)` returns an
`mqttrelay.Definition` or a skip reason. Templates translate to JavaScript
from a Jinja subset: `value`, `value_json` paths with dots and brackets,
literals, arithmetic, comparisons, `and`/`or`/`not`, inline `if`/`else`, and
the filters `float`, `int`, `round`, `default`, `lower`, `upper`, `string`,
`abs`, `trim`. Anything else, including `{% %}` blocks, is a skip reason. The
generated state script dispatches on topic; the payload script dispatches on
field. Component coverage and key mapping are in
[contracts/interfaces.md](contracts/interfaces.md).

### Interfaces

- REST `GET /api/connections/{id}/auto_import` returns import status (Use
  access). `GET /api/mqtt_devices/mqtt_discovery?connection_id=` lists
  discovery configs; `POST /api/mqtt_devices/mqtt_discovery` adds chosen
  ones (connections write plus Use, as the other pickers).
- MCP `get_auto_import_status`, `discover_mqtt_discovery_devices`,
  `add_mqtt_discovery_devices`.
- Device views gain `auto_imported` and `last_reported_at`.

## State and data model

See [data-model.md](data-model.md). Device identity for discovery devices is
`(connection_id, discovery_key)`, checked in `SaveDefinition` like the
Zigbee2MQTT and entity keys.

## Verification

- Unit: discovery parsing and abbreviations, template translation, mapping
  per component against recorded configs, settings validation, filter
  matching, removal reference times and tombstones with a test clock.
- Live: publish discovery configs to a broker, confirm devices appear and
  command; delete one and confirm it returns; set `remove_after` with a test
  clock in unit tests rather than waiting an hour.
- Browser: connection form fields and status, picker visibility, delete
  warning, MQTT discovery picker; console clean.

## Risks and open decisions

- The Jinja subset covers the templates Tasmota, ESPHome, Shelly, and
  Zigbee2MQTT publish in common configs; others are skipped, not guessed.
- Removal of a hub entity device follows list absence only; an entity Home
  Assistant still lists but marks unavailable is kept.
