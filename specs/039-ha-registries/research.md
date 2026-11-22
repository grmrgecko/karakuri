# Research: Home Assistant areas and devices

## Where Home Assistant lives today

- `pkg/integrations/homeassistant/driver.go` is a hand-rolled WebSocket
  client: id-correlated commands, shared event subscriptions with fan-out,
  reconnect with resubscribe, a 30 s states cache for pickers, and the
  reconnect catch-up of feature 004 in `catchup.go`. `hatest` is the fake
  server the tests use; it answers `get_states`, `call_service`,
  `subscribe_events`, history, statistics, and to-do commands.
- Pickers: `GET /api/connections/{id}/ha/entities` (`pkg/api/ha_handler.go`)
  returns `entity_id`, `friendly_name`, `state`, used by the flow editor's
  `entity` field (`ConfigForm.svelte`) and the widgets' `EntityPicker.svelte`.
  MCP `get_ha_states` filters by ID or substring.
- `ha-call-service` already has `area_id`, `device_id`, and `label_id` as
  free-text comma-separated fields; there is no `floor_id`, and nothing helps
  the author find an ID.
- The domain-to-kind mapping exists only in TypeScript
  (`plugins/core/widgets/shared/entitySource.ts`: `HA_DOMAIN_KINDS`,
  `haOptions`, `haValue`, `haService`).
- Entity devices: `pkg/entitydevices` turns a source's entities into
  `models.MQTTDevice` rows with a transport (`esphome`, `frigate`);
  `mqttrelay` drives them through `EntityTransport` (`WatchEntity`,
  `CommandEntity`) and `EntityAvailability`; the `Tracker` marks devices
  missing when their entity leaves the list. MCP `discover_entities` and
  `add_entities` and the settings page go through `entitydevices.SourceFor`.

## Registry commands

- Decision: read with `config/floor_registry/list`, `config/area_registry/list`,
  `config/device_registry/list`, `config/entity_registry/list_for_display`, and
  `config/label_registry/list` after each authentication, and follow
  `floor_registry_updated`, `area_registry_updated`, `device_registry_updated`,
  `entity_registry_updated`, and `label_registry_updated`.
- Rationale: the list commands are read-only and not restricted to
  administrators in Home Assistant; the five update events are on the
  subscription allow list for every user. `list_for_display` is the compact
  form the Home Assistant frontend loads (short keys: `ei` entity, `di`
  device, `ai` area, `lb` labels, `en` name, `hb` hidden, `ec` category) and
  omits disabled entities, which is what pickers want; it is a fraction of the
  size of `config/entity_registry/list` on a large installation.
- Floors and labels arrived in Home Assistant 2024.4; an `unknown_command`
  reply leaves that registry empty and is reported in the connection status
  rather than failing the connection.
- An update event carries an action and IDs, not the record. Decision: reread
  the changed registry, debounced by two seconds so a burst (a device with
  twenty entities added) costs one read.
- Events during an outage are lost, so every reconnect rereads all five, as
  the state catch-up does.

## Names and areas

- Display name: the state's `friendly_name` attribute, which Home Assistant
  already composes from device and entity name; the registry `en` (entity
  name, null when the entity is named after its device) is used only for the
  short name under a device heading.
- Area: the entity's `ai`, else its device's `area_id` (FR-002). Floor: the
  area's `floor_id`; floors order by `level`, then name.

## Resolving targets

- Service calls pass `area_id`, `floor_id`, `device_id`, and `label_id`
  straight to Home Assistant, which resolves them; karakuri resolves the same
  target from its registry copy only to refuse a call that matches nothing
  ("no entities in Kitchen"), and only when the registry is loaded.
- Triggers resolve membership per event: an incoming `state_changed` is
  matched against the registry copy at that moment, so a moved or added
  entity counts from the next event with no resubscription (SC-002). The
  trigger keeps its one `state_changed` subscription.
- Matching: area by entity or inherited device area; floor through the
  area; device by `di`; label by the entity's labels or its device's labels,
  as Home Assistant's target resolution does; then the optional domain and
  `device_class` attribute.

## One mapping for faces and devices

- Decision: port the mapping to Go in `pkg/integrations/homeassistant/mapping.go`
  (kind, options, writable, value, and command for each domain) and keep the
  TypeScript copy for the browser, with both tested against one shared
  fixture, `pkg/integrations/homeassistant/testdata/mapping.json`: each case
  is a state and the expected kind, options, value, and the service call for
  listed writes. The vitest suite and the Go tests read the same file, so a
  change to either mapping fails until both agree (FR-006, SC-004).
- Alternatives: serving the mapping from the server would make every card
  row wait on a round trip and break the widgets' offline-tolerant rendering;
  compiling one copy to both languages is more machinery than a shared
  fixture.

## Home Assistant devices

- Transport `homeassistant`; `EntityKey` is the entity ID and `EntityType`
  the domain. The driver implements `EntityTransport`: `WatchEntity` serves
  the cached state then each `state_changed` for that entity mapped through
  `Values`; `CommandEntity` maps the write with `Command` and calls the
  service. `EntityAvailability` reports an entity whose state is
  `unavailable` or `unknown`.
- Writable domains are those the mapping commands; `mqttrelay.Commandable`
  checks the transport's own set instead of one shared list, since ESPHome's
  `mediaplayer` and Home Assistant's `media_player` name the same thing
  differently.
- A renamed entity arrives as `entity_registry_updated` with `action:
  update` and `old_entity_id`; the device's `EntityKey` moves to the new ID.
  A removed entity leaves the list and the `Tracker` marks the device missing.

## Pickers

- A new field type `ha-target` with `control` `area`, `floor`, `device`, or
  `label` renders a searchable multi-select of that registry and stores the
  same comma-separated IDs the fields hold today, so existing flows keep
  their values and `{{ }}` expressions still work when typed.
- `GET /ha/entities` gains area, floor, device, labels, hidden, and category
  per entity; pickers group by them. `GET /ha/registry` returns floors,
  areas, devices, and labels for the target pickers and the card's area
  fill.
