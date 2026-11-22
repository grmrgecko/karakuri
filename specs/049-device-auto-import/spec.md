# Feature specification: Device auto import

**Feature**: `specs/049-device-auto-import`
**Created**: 2026-11-22
**Status**: Implemented
**Input**: Connection device auto import. Each device-capable connection (MQTT,
ESPHome, Frigate, Home Assistant, openHAB) gains an "Auto import devices"
option. When on, devices the source announces are added as devices
automatically, like Home Assistant adds devices from MQTT discovery. MQTT
connections gain Home Assistant-style MQTT discovery
(`homeassistant/<component>/<object>/config`, configurable prefix) alongside
Zigbee2MQTT bridge discovery (configurable base topic). Home Assistant and
openHAB auto import can be narrowed by filters (Home Assistant areas and
domains, openHAB locations and item types). Each connection has a "Remove
after" duration: an auto-imported device that the source has not reported for
that long is removed automatically; empty never removes. A deleted
auto-imported device that is still being discovered is re-added on its next
discovery. The manual "Add from ..." pickers stay for connections without auto
import. Greenfield change: no migration of existing data required.

## User scenarios and testing

### User story 1: New MQTT devices appear on their own (Priority: P1)

A household runs Tasmota plugs, an ESPHome sensor flashed with MQTT, and
Zigbee2MQTT on one broker. The author turns on auto import on the MQTT
connection once. Every device announcing itself with Home Assistant MQTT
discovery, and every Zigbee device Zigbee2MQTT knows, shows up as a working
device. Next month a new plug is plugged in, announces itself, and is on the
device list a moment later without anyone opening Settings.

**Why this priority**: Adding devices by hand is the step that keeps a
household on Home Assistant; discovery-driven import removes it.
**Independent test**: Turn on auto import on an MQTT connection pointed at a
test broker, publish discovery configs for a switch, a light, a sensor, and a
binary sensor, and a Zigbee2MQTT device list, and check that each becomes a
device that reads and commands from a dashboard. Publish one more config and
check it appears without any action.

**Acceptance scenarios**:

1. **Given** an MQTT connection with auto import on and the default discovery
   prefix `homeassistant`, **When** a device publishes a discovery config for
   a supported component, **Then** a device is added with its kind, options,
   state reading, commands, and availability taken from the config.
2. **Given** the same connection with a Zigbee2MQTT base topic set, **When**
   Zigbee2MQTT publishes its device list, **Then** every device that finished
   its interview is added as feature 032 adds a picked device.
3. **Given** a device announced both by Zigbee2MQTT's bridge list and by
   Zigbee2MQTT's own Home Assistant discovery output, **When** both arrive,
   **Then** it is added once, from the Zigbee2MQTT bridge list.
4. **Given** a discovery config for a component karakuri does not support, or
   with a template it cannot evaluate, **When** it arrives, **Then** nothing
   is added and the connection's import status lists it as skipped with the
   reason.
5. **Given** a device already added, **When** its discovery config is
   published again with changes, **Then** the device keeps its karakuri name,
   bindings, and any edits the author made, and its topics follow the new
   config.
6. **Given** a discovery config published with an empty payload, **When** it
   arrives, **Then** the device is marked missing, as when Zigbee2MQTT drops a
   device.

### User story 2: Old devices go away on their own (Priority: P1)

The author sets "Remove after" to 7 days on the MQTT connection. A plug given
away last month has not said anything since. It is removed from the device
list, and the event log says why.

**Why this priority**: Auto import without cleanup fills the device list with
dead entries that dashboards and pickers keep offering.
**Independent test**: Set "Remove after" to a short duration, auto import a
device, stop all messages from it, and check that it is removed once the
duration passes and that a device that keeps reporting is kept.

**Acceptance scenarios**:

1. **Given** an auto-imported device and "Remove after" set, **When** its
   source has not reported it for that long, **Then** the device and its
   stored state are removed and the event log records the device, the
   connection, and the time it was last reported.
2. **Given** a device that reports again before the duration passes, **When**
   the check runs, **Then** the device is kept and its last reported time is
   the latest report.
3. **Given** "Remove after" empty, **When** a device stops reporting, **Then**
   it is never removed; it is marked missing where its source says it is gone.
4. **Given** a device the author added by hand or through a picker, **When**
   it stops reporting, **Then** "Remove after" does not remove it.
5. **Given** an MQTT broker, Home Assistant, openHAB, or Frigate connection
   that is down, **When** time passes, **Then** that time does not count
   toward removal for its devices.
6. **Given** a device removed for not reporting, **When** the source later
   reports it again with fresh activity, **Then** it is imported again as a
   new device.

### User story 3: Hub connections import what the author wants (Priority: P2)

A Home Assistant install has 600 entities. The author turns on auto import
for the Kitchen and Living room areas and the `light` and `switch` domains
only. Those entities arrive as devices, including one added to the kitchen
next week. openHAB, Frigate, and ESPHome connections work the same way.

**Why this priority**: Hub sources are useful only with a filter; without one
the device list drowns in diagnostic entities.
**Independent test**: Connect to a Home Assistant test instance, turn on auto
import with an area and a domain filter, and check that exactly the matching
entities become devices; add one more matching entity and check it appears.

**Acceptance scenarios**:

1. **Given** a Home Assistant connection with auto import on and area and
   domain filters, **When** it connects, **Then** every entity that matches
   all non-empty filters is added, and no other entity is.
2. **Given** an openHAB connection with location and item type filters,
   **When** an item matching them is added in openHAB, **Then** it is added
   as a device.
3. **Given** an ESPHome connection with auto import on, **When** the node
   connects, **Then** every entity it reports is added; entities added by a
   later reflash arrive when the node reconnects.
4. **Given** a Frigate connection with auto import on, **When** a camera is
   added in Frigate, **Then** its switches are added.
5. **Given** a filter that is changed to exclude devices already imported,
   **When** the connection is saved, **Then** those devices stay and are no
   longer refreshed by import; "Remove after" then treats them as no longer
   reported.

### User story 4: Delete and pick by hand (Priority: P3)

The author deletes an auto-imported device they do not want. It comes back
the next time its source reports it, and the device list says so before the
delete. Connections without auto import keep the "Add from" pickers, and MQTT
connections gain a picker for Home Assistant discovery devices.

**Why this priority**: Deleting is how authors expect to get rid of a device;
they must know auto import will bring it back.
**Independent test**: Delete an auto-imported device that is still
announcing, check the warning, and check that it is added again on its next
report. Open the MQTT discovery picker on a connection without auto import
and add one device.

**Acceptance scenarios**:

1. **Given** an auto-imported device, **When** the author deletes it, **Then**
   the confirmation says its connection imports devices automatically and
   that it will be added again while its source reports it.
2. **Given** a deleted auto-imported device, **When** its source next reports
   it, **Then** it is added again as a new device.
3. **Given** a connection with auto import off, **When** the author opens the
   Devices page, **Then** the "Add from" picker for that connection's kind is
   offered; for a connection with auto import on, the picker is not offered
   for it.
4. **Given** an MQTT connection with auto import off, **When** the author
   opens "Add from MQTT discovery", **Then** devices announced with Home
   Assistant discovery are listed with their proposed kind and can be added.

### Edge cases

- A device name already used gets a numeric suffix, as manual adds do.
- Two discovery configs for the same unique ID on different topics: the
  latest one wins and the device follows it.
- A discovery config without a unique ID is imported with the config topic as
  its identity, as Home Assistant does.
- Device-based discovery configs (one config listing several components)
  import one device per component.
- Retained discovery configs replayed when the connection subscribes import
  devices that are unknown, but do not count as a fresh report for a device
  removed for not reporting; only live activity brings that device back.
- A device auto import added, then edited by the author, keeps the edits;
  later discovery changes update only its topics and availability settings.
- Turning auto import off keeps every device it added as an ordinary device
  and stops removal for them.
- Deleting a connection leaves its devices, as today.
- "Remove after" shorter than one hour is refused.
- An ESPHome connection is the node itself, so the time its node is
  unreachable counts toward removal.
- Several Zigbee2MQTT instances on one broker need one MQTT connection each,
  since a connection has one base topic for import; pickers still accept any
  base topic.

## Requirements

### Functional requirements

- **FR-001**: MQTT, ESPHome, Frigate, Home Assistant, and openHAB connections
  MUST offer an "Auto import devices" option, off by default.
- **FR-002**: With auto import on, every device its source reports that
  passes the connection's filters MUST be added as a device when the
  connection connects and whenever the source reports a new one.
- **FR-003**: MQTT connections MUST read Home Assistant MQTT discovery from a
  configurable prefix, default `homeassistant`, including per-component and
  device-based configs, abbreviated keys, and the `~` base topic.
- **FR-004**: Discovery MUST map switch, light (default and JSON schemas),
  binary sensor, sensor, cover, lock, fan, climate, number, select, button,
  and text components to karakuri kinds with state reading, commands, and
  availability taken from the config; other components and templates karakuri
  cannot evaluate MUST be skipped and reported in the import status.
- **FR-005**: MQTT connections MUST offer a Zigbee2MQTT base topic for import,
  default `zigbee2mqtt`, `off` to turn it off, and MUST import from it as
  feature 032 adds a picked device; a device present in both sources MUST be
  imported once, from Zigbee2MQTT's bridge list.
- **FR-006**: Home Assistant auto import MUST be narrowable by areas and
  domains, and openHAB auto import by locations and item types; an empty
  filter matches everything and an entity must match every non-empty filter.
- **FR-007**: Each device MUST record whether auto import added it and when
  its source last reported it. A report is a live (not replayed) discovery
  config, availability online, or state message for MQTT sources, and presence in the source's
  entity list while connected for ESPHome, Frigate, Home Assistant, and
  openHAB.
- **FR-008**: Connections MUST offer a "Remove after" duration, empty by
  default meaning never, minimum one hour. An auto-imported device not
  reported for that long MUST be removed with its stored state, and the event
  log MUST record the removal.
- **FR-009**: Time while an MQTT, Frigate, Home Assistant, or openHAB
  connection is down MUST NOT count toward removal: the removal clock for its
  devices restarts when the connection connects. Time while an ESPHome node
  is unreachable MUST count.
- **FR-010**: A device removed by the author MUST be imported again on its
  source's next report. A device removed for not reporting MUST be imported
  again only on live activity, not on a retained replay.
- **FR-011**: Import MUST keep an existing device's name, bindings, and author
  edits, updating only its topics, availability settings, and missing state.
- **FR-012**: Each connection MUST show an import status: devices imported,
  devices skipped with reasons, and the last import and removal times.
- **FR-013**: The "Add from" pickers MUST remain for connections with auto
  import off and be hidden for those with it on; MQTT connections MUST gain
  a picker for Home Assistant discovery devices.
- **FR-014**: Deleting an auto-imported device MUST warn that auto import adds
  it again while its source reports it.
- **FR-015**: Auto import MUST act as the server; turning it on or changing
  its settings MUST require edit access to the connection, as other
  connection settings do. Imported devices follow the connection's access as
  feature 047 states.
- **FR-016**: Auto import settings, import status, and the discovery picker
  MUST be exposed through REST, MCP, and the interface with the same rules.

### Key entities

- **Auto import settings**: per connection, whether auto import is on, the
  "Remove after" duration, the MQTT discovery prefix and Zigbee2MQTT base
  topic, and the Home Assistant or openHAB filters.
- **Device origin**: per device, whether auto import added it, the source
  identity it follows (discovery unique ID, IEEE address, or entity key), and
  when it was last reported.
- **Import status**: per connection, imported and skipped devices with
  reasons and the last import and removal times.

## Success criteria

- **SC-001**: With auto import on, a device publishing a supported discovery
  config appears on the device list and works from a dashboard without any
  author action.
- **SC-002**: From recorded discovery configs covering every supported
  component, every config becomes a device that reads, and where the
  component takes commands, commands without hand-written scripts.
- **SC-003**: With "Remove after" set to its minimum and a test clock, a
  silent auto-imported device is removed within one check interval after the
  duration, and a device that keeps reporting is never removed.
- **SC-004**: A Home Assistant connection with an area and domain filter
  imports exactly the matching entities and no others.
- **SC-005**: Relevant Makefile builds and tests pass, the connection form,
  import status, and pickers render without console errors, and the
  development service stays active.

## Assumptions and scope

- Greenfield change: no existing installation depends on the previous device
  records, so device records gain origin fields without a migration path for
  older databases.
- Discovery follows the Home Assistant MQTT discovery format as Home
  Assistant documents it, including device-based discovery; Home Assistant
  itself does not need to run.
- karakuri does not publish Home Assistant's birth message and does not clear
  retained discovery configs when it removes a device.
- Device triggers, tag scanners, image, camera, vacuum, lawn mower, siren,
  valve, water heater, alarm panel, and update components are out of scope
  for this feature and are reported as skipped.
- Discovering new ESPHome nodes and creating connections for them is out of
  scope; a node still needs its own connection and key.
- Pairing devices stays in Zigbee2MQTT, Home Assistant, openHAB, and Frigate.

## References

- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Zigbee2MQTT discovery and device availability](../032-zigbee2mqtt-discovery/spec.md)
- [ESPHome native API](../033-esphome-native-api/spec.md)
- [Frigate NVR integration](../034-frigate-nvr/spec.md)
- [Event log](../037-event-log/spec.md)
- [Home Assistant areas and devices](../039-ha-registries/spec.md)
- [openHAB connection](../040-openhab/spec.md)
- [Connection and agent access](../047-connection-agent-access/spec.md)
