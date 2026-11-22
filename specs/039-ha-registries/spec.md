# Feature specification: Home Assistant areas and devices

**Feature**: `specs/039-ha-registries`
**Created**: 2026-11-06
**Status**: Implemented
**Input**: Read Home Assistant's area, floor, device, entity, and label
registries and keep them current, so entities are found and named by room and
device rather than by entity ID; let flows target and watch an area, a device,
or a label rather than a hand-kept list of entities; and let Home Assistant
entities be added as karakuri devices, so flows command and read them by kind
attribute with Device Command, Device Value, and Device Changed, as ESPHome and
MQTT devices already are.

## User scenarios and testing

### User story 1: Find entities by room (Priority: P1)

The owner adds a row to the kitchen card. The entity picker shows the
kitchen's lights, sensors, and speaker under **Kitchen**, named as Home
Assistant names them ("Ceiling light", not `light.hue_ambiance_3`), and typing
"kitchen" finds them. The same grouping appears in flow node pickers and over
MCP.

**Why this priority**: An installation with hundreds of entities is
unusable as a flat list of IDs, and every binding starts with a pick.
**Independent test**: Open the entity picker on a connection whose Home
Assistant has areas; entities appear grouped by floor and area with their
device, and a search by area name narrows to that area.

**Acceptance scenarios**:

1. **Given** a Home Assistant with areas and devices, **When** an entity
   picker opens, **Then** entities are grouped by area (in floor order when
   floors exist), each shown with its display name and device, and entities
   with no area are grouped last.
2. **Given** an entity whose own area is unset, **When** it is listed,
   **Then** it takes its device's area, as Home Assistant does.
3. **Given** a search term, **When** typed, **Then** it matches entity names,
   entity IDs, device names, area names, and labels.
4. **Given** entities Home Assistant marks hidden or disabled, **When** the
   picker opens, **Then** they are left out unless the author asks to show
   them, and a bound entity that becomes hidden keeps working.
5. **Given** an area or device renamed in Home Assistant, **When** a picker
   or a list is next opened, **Then** it shows the new name without a
   reconnect.

---

### User story 2: Target and watch a room (Priority: P1)

A flow turns off every light in the living room at midnight, and another
starts when any motion sensor downstairs detects motion. Neither lists
entities, so a lamp added to the living room next month is covered.

**Why this priority**: Rooms are how people think about automations, and a
hand-kept entity list goes stale the first time a device is added.
**Independent test**: Call a service on an area and arm a state trigger on a
floor and domain; add an entity to that area in Home Assistant and see it
included without redeploying.

**Acceptance scenarios**:

1. **Given** the call service node, **When** the author targets areas,
   floors, devices, or labels (chosen from pickers), **Then** Home Assistant
   receives that target and acts on every matching entity.
2. **Given** a state trigger set to an area, floor, device, or label and
   optionally a domain and device class, **When** any matching entity
   changes, **Then** the flow runs with that entity's change and the area it
   matched through.
3. **Given** such a trigger is armed, **When** an entity is added to, moved
   out of, or removed from the area in Home Assistant, **Then** the trigger
   follows within a minute without a redeploy.
4. **Given** the get state node, **When** it is pointed at an area with a
   domain, **Then** it returns the states of every matching entity.
5. **Given** a target that matches nothing, **When** the flow runs, **Then**
   the call service node fails with "no entities in <area>" rather than
   reporting success, and a trigger arms and waits.

---

### User story 3: Home Assistant entities as devices (Priority: P2)

The owner adds the hallway light and the living room speaker from Home
Assistant as devices. A flow dims the light with Device Command `brightness`
40 and pauses the speaker with `state` `paused`, exactly as it does an
ESPHome light and an MQTT speaker, and Device Changed fires when either
changes.

**Why this priority**: Flows reach Home Assistant only through service calls
today, so a flow written for an ESPHome light cannot be pointed at a Home
Assistant one. Devices are the one model karakuri already offers for this.
**Independent test**: Add a Home Assistant light and media player as devices;
command and read them with the device nodes and see them on a card.

**Acceptance scenarios**:

1. **Given** a connected Home Assistant, **When** **Add from Home Assistant**
   opens, **Then** entities are listed by area with the device kind each
   becomes, using the same mapping as the dashboard faces, and read-only
   domains marked as taking no commands.
2. **Given** a Home Assistant device, **When** Device Command writes a kind
   attribute, **Then** the matching Home Assistant service is called, a write
   the entity's features do not allow is refused before anything is sent, and
   the device's value changes only when Home Assistant reports the new state.
3. **Given** a Home Assistant device, **When** Home Assistant reports a state,
   **Then** Device Value, Device Changed, conditions, history forwarding, and
   the event log see it as for any device, and availability follows the
   entity's `unavailable` state and the connection.
4. **Given** an entity removed or renamed in Home Assistant, **When** the
   registry changes, **Then** its device is marked missing or keeps working
   under its new ID respectively, as ESPHome devices do when a node is
   reflashed.

---

### User story 4: Cards from a room (Priority: P3)

Building a dashboard, the owner picks **Kitchen** in the entities card and
gets a row for each of its useful entities, which they can then trim.

**Why this priority**: A convenience on top of US1 that saves the most
repetitive part of building a dashboard.
**Independent test**: Fill a card from an area and compare the rows with the
area's entities.

**Acceptance scenarios**:

1. **Given** the entities card config, **When** the author picks an area,
   **Then** rows are added for that area's visible entities, ordered by
   device, skipping diagnostic and configuration entities, and duplicates of
   rows already present.
2. **Given** a card filled from an area, **When** the area later gains an
   entity, **Then** the card is unchanged; filling is a one-time copy.

### Edge cases

- Older Home Assistant versions have no floors or labels, and a Home
  Assistant may refuse a registry read. A connection that cannot read a
  registry works as today for what it lacks (a flat list when areas are
  missing), and its status says which registries are unavailable and why.
- Registries change while karakuri is connected; changes are followed from
  Home Assistant's registry update events, and the whole set is reread after
  a reconnect, since events during an outage are lost.
- An area target is resolved by Home Assistant for service calls, so its
  meaning matches Home Assistant's; triggers resolve areas in karakuri from
  the registries, including an entity's inherited device area.
- Thousands of entities: a picker searches without loading every row at
  once, and a trigger on an area costs no more than one on its entities.
- Two Home Assistant connections each have their own registries; nothing is
  merged across them.
- A deleted area leaves bindings and devices working, since they hold entity
  IDs; a trigger on that area arms with nothing to watch and says so.
- A Home Assistant device that duplicates an existing binding is allowed; a
  card row bound to the entity and one bound to the device both work.
- Entities that are not states, such as Assist satellites' pipelines or
  configuration-only entities, are listed only when asked for.

## Requirements

### Functional requirements

- **FR-001**: The system MUST read each Home Assistant connection's floors,
  areas, devices, entity registry entries, and labels on connect and after
  every reconnect, and follow their update events while connected.
- **FR-002**: An entity's area MUST be its own area when set and its device's
  area otherwise; its display name MUST be the name Home Assistant shows.
- **FR-003**: Entity pickers in dashboards and flow nodes, and the MCP state
  and entity listing, MUST group or annotate entities by floor, area, and
  device, search across names, IDs, devices, areas, and labels, and leave out
  hidden and disabled entities unless asked.
- **FR-004**: The call service node MUST accept targets by area, floor,
  device, and label as well as entity, chosen from pickers.
- **FR-005**: The state trigger and the get state node MUST accept an area,
  floor, device, or label with an optional domain and device class, resolve
  them from the registries, and follow registry changes without a redeploy.
- **FR-006**: Home Assistant entities MUST be addable as devices whose kind,
  options, value, and commands follow the same domain mapping the dashboard
  faces use, so a device and a card row bound to the same entity behave the
  same.
- **FR-007**: Home Assistant devices MUST work with Device Command, Device
  Value, Device Changed, Device Availability, dashboard rows, conditions,
  history forwarding, and the event log as other devices do.
- **FR-008**: Commands to a Home Assistant device MUST be checked against its
  kind and features before the service is called, and the device's value
  MUST change only when Home Assistant reports it.
- **FR-009**: The entities card MUST offer filling rows from an area.
- **FR-010**: A connection that cannot read a registry MUST keep working as
  before for what that registry would add and report the reason in its
  status.

### Key entities

- **Registry**: per connection, floors (order), areas (floor, name), devices
  (area, name, manufacturer, model), entity entries (device, area, name,
  hidden, disabled, category, labels), and labels.
- **Home Assistant device**: a karakuri device bound to one entity of one
  connection, with the kind its domain maps to.
- **Area target**: an area, floor, device, or label, with optional domain and
  device class, resolved to entities at use.

## Success criteria

- **SC-001**: On an installation with 1,000 entities, an author finds an
  entity by area name in the picker within three keystrokes of the area name.
- **SC-002**: A state trigger on an area includes an entity added to that area
  in Home Assistant within one minute, with no redeploy.
- **SC-003**: A flow that dims a light with Device Command works unchanged
  when its device is switched from an ESPHome light to a Home Assistant light.
- **SC-004**: A Home Assistant device and a card row bound to the same entity
  show the same face and the same value.
- **SC-005**: A connection whose Home Assistant refuses or lacks a registry
  loses no current behavior.

## Assumptions and scope

- The lab Home Assistant is read only for testing: registries are read, never
  written, and commands to Home Assistant devices are checked against the test
  fake and by reading the service call a command would make.
- Editing areas, devices, or labels from karakuri is out of scope; Home
  Assistant remains where they are managed.
- karakuri areas of its own (grouping karakuri controls and MQTT devices into
  rooms) are out of scope; a later feature can map them onto these.
- The domain-to-kind mapping that dashboards apply becomes the single mapping
  that devices use too; domains the mapping draws as readings become
  read-only devices.
- Commanding Home Assistant players from flows by kind attribute, deferred by
  feature 038, is covered by US3.

## References

- [Feature 004: connections and devices](../004-connections-devices/spec.md)
- [Feature 033: ESPHome native API](../033-esphome-native-api/spec.md)
- [Feature 034: Frigate NVR](../034-frigate-nvr/spec.md)
- [Feature 038: media control kind](../038-media-control-kind/spec.md)
