# Tasks: Device auto import

**Feature**: `specs/049-device-auto-import` | [spec.md](spec.md) | [plan.md](plan.md)

## Phase 1: Foundation

- [x] T001 Add `auto_imported`, `discovery_key`, `last_reported_at` to `models.MQTTDevice` and the `AutoImportTombstone` model in `pkg/models`, registered for migration.
- [x] T002 Extend `mqttrelay.Origin` with discovery key and auto flag; enforce `(connection_id, discovery_key)` uniqueness in `SaveDefinition`; expose the new fields in device views.
- [x] T003 Add shared auto import config fields and `ValidateAutoImport` in `pkg/integrations/autoimport.go`; add them to the MQTT, ESPHome, Frigate, Home Assistant, and openHAB kinds; call validation from `connops.validateConfig`.

## Phase 2: Home Assistant MQTT discovery (US1)

- [x] T004 `pkg/hadiscovery`: topic parsing, abbreviation expansion, device-based configs.
- [x] T005 `pkg/hadiscovery`: Jinja subset to JavaScript translator with tests.
- [x] T006 `pkg/hadiscovery`: component mapping to definitions with generated scripts, with tests over recorded configs.
- [x] T007 Discovery picker: read retained configs on demand, REST and MCP list and add.

## Phase 3: Importer (US1, US2, US3)

- [x] T008 `entitydevices`: entity places for filters, auto add path.
- [x] T009 `zigbee2mqtt`: add from a parsed list with auto origin.
- [x] T010 `pkg/autoimport`: settings, entity followers with filters, MQTT follower (discovery, Zigbee2MQTT, activity), tombstones.
- [x] T011 `pkg/autoimport`: sweep, last reported flush, removal with events, re-import of deleted devices, status.
- [x] T012 Wire the importer in `serve`; REST and MCP import status.

## Phase 4: Interface (US4)

- [x] T013 Connection form shows import status; Devices page hides pickers for auto import connections, adds "Add from MQTT discovery", warns on deleting an auto-imported device, and marks auto-imported devices.

## Phase 5: Guides and verification

- [x] T014 Update guides: `docs/mqtt-device-state-script.md` or a new MQTT discovery section, `docs/esphome.md`, `docs/frigate.md`, `docs/homeassistant.md`, `docs/openhab.md`, `docs/mcp.md`.
- [x] T015 `make test`, `make dev`, restart the service, live MQTT check, browser check.
