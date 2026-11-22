# Feature specification: openHAB connection

**Feature**: `specs/040-openhab`
**Created**: 2026-11-06
**Status**: Implemented
**Input**: Connect karakuri to openHAB so a house run by openHAB can use
karakuri's flows and dashboards: an openHAB connection that follows item
states live and sends commands, openHAB items added as karakuri devices drawn
with the control kind their item type maps to, items found by their semantic
location, flow nodes that trigger on item changes and send commands, and a
catch-up after the connection drops, as the Home Assistant connection has.
Tested against openHAB running in Docker.

## User scenarios and testing

### User story 1: Connect to openHAB and see its items (Priority: P1)

The owner adds an openHAB connection with the server's address and an API
token. The connection shows connected, and the item list shows the house's
lights, switches, blinds, and sensors under their rooms.

**Why this priority**: Nothing else works without a connection that stays up
and knows the items.
**Independent test**: Add a connection to an openHAB in Docker, see it
connect, and list its items grouped by location; stop openHAB and see the
connection report the error, then reconnect on its own.

**Acceptance scenarios**:

1. **Given** an openHAB address and API token, **When** the connection is
   saved, **Then** it connects, and the connection test reports the openHAB
   version and item count.
2. **Given** a wrong token or address, **When** the connection is tested,
   **Then** it fails with the reason openHAB gave (unauthorized, not found,
   unreachable).
3. **Given** items in openHAB's semantic model, **When** they are listed,
   **Then** each shows its label, type, state, and the location it sits in,
   and items in no location are listed last.
4. **Given** the connection drops, **When** openHAB comes back, **Then** the
   connection reconnects with backoff and resumes following item states.

---

### User story 2: openHAB items as devices (Priority: P1)

The owner adds the living room dimmer, the kitchen blind, and the hallway
temperature as devices. A card draws the dimmer as a light with a brightness
slider and the blind as a cover, a flow dims the light with Device Command,
and Device Changed fires when the temperature changes, as for any device.

**Why this priority**: Devices are how karakuri draws and commands hardware;
an openHAB item as a device works on every card, condition, and device node
with no new widget.
**Independent test**: Add a Dimmer, a Rollershutter, a Number, and a Switch as
devices; command them from a card and a flow and watch openHAB's item states
change; change items in openHAB and watch the devices follow.

**Acceptance scenarios**:

1. **Given** a connected openHAB, **When** **Add from openHAB** opens, **Then**
   items are listed by location with the device kind each becomes and whether
   it takes commands; unsupported types say why.
2. **Given** an item device, **When** it is commanded, **Then** the matching
   openHAB command is sent (ON or OFF, a percent, an HSB colour, UP, DOWN,
   STOP, or a position, a number, a string), a value the item's state
   description forbids is refused before anything is sent, and the device's
   value changes only when openHAB reports the new state.
3. **Given** an item whose state is NULL or UNDEF, **When** it is drawn,
   **Then** the device shows as unknown rather than as a value.
4. **Given** an item removed or renamed in openHAB, **When** the item list
   changes, **Then** its device is marked missing, and clears when an item of
   that name returns.

---

### User story 3: Flows on openHAB items (Priority: P2)

A flow starts when any window contact in the upstairs location opens, and
another sends `OFF` to an openHAB group of lights at midnight.

**Why this priority**: Devices cover commanding single items by kind; flows
also need triggers by item, group, and location, and raw commands to groups
and items not added as devices.
**Independent test**: Arm an item trigger on a location and type, change an
item there, and see one run; send a command to a group from a flow.

**Acceptance scenarios**:

1. **Given** an openHAB Item Changed trigger on items, groups, or a location,
   optionally narrowed by item type and from and to states, **When** a
   matching item changes, **Then** the flow runs once with the item, its old
   and new states, and its location.
2. **Given** an openHAB Send Command node, **When** it runs, **Then** the
   command is sent to the item or group, with the command templated from the
   message, and a refusal from openHAB fails the node with the reason.
3. **Given** an openHAB Get Item node, **When** it is pulled, **Then** it
   returns the item's current state, type, and label.

---

### User story 4: Catch up after an outage (Priority: P2)

openHAB restarts for an update while a door opens. When karakuri reconnects,
the door's trigger runs once with the latest state, and dashboards show
current values.

**Why this priority**: The same correctness the Home Assistant connection
gives; an event stream has a gap whenever it drops.
**Independent test**: Stop the openHAB container, change no items (its state
resets) or change items through the console, start it, and see one caught-up
report per changed item.

**Acceptance scenarios**:

1. **Given** items changed while the connection was down, **When** it
   reconnects, **Then** each changed item is reported once with its latest
   state, marked caught up, and unchanged items are not reported.
2. **Given** an item trigger that ignores caught-up changes, **When** the
   catch-up arrives, **Then** it does not run.

### Edge cases

- openHAB's rollershutter position counts 0 as open and 100 as closed; a cover
  device shows position the karakuri way (100 open), converting both ways.
- A Color item's state is hue, saturation, and brightness; the light's colour
  is hex and its brightness the HSB brightness.
- A Number item with a unit (`Number:Temperature`) carries the unit in its
  state (`21.5 °C`); the device's value is the number and the unit is a kind
  option.
- A Group item with a base type (a group of switches) can be a device of that
  type; a group without one is used only for locations and triggers.
- An item whose state description is read only becomes a device that takes no
  commands.
- openHAB may allow anonymous reads but not commands; the connection reports
  a command refused for authorization as such.
- Thousands of items: the item list loads once and follows changes, and a
  trigger on a location costs no more than one on its items.
- An item renamed in openHAB is a new item to the API; the old device is
  marked missing rather than guessed onto the new name.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer an openHAB connection kind with a base URL
  and an optional API token (stored encrypted and masked; openHAB lets
  anonymous clients read and command by default), validated on save, with a
  connection test reporting the version, item count, and whether it
  authenticated, or the failure.
- **FR-002**: An openHAB connection MUST follow item state changes and item
  additions, updates, and removals from openHAB's event stream, reconnecting
  with backoff, and MUST read every item after each connect.
- **FR-003**: Items MUST be listed with label, type, state, state description,
  and semantic location (the nearest enclosing location group), grouped by
  location in pickers and MCP.
- **FR-004**: openHAB items MUST be addable as devices whose kind and options
  follow a fixed mapping of item types (Switch, Contact, Dimmer, Color,
  Rollershutter, Number with or without a unit, String, DateTime, Player,
  Location, and Group with a base type), and whose commands become openHAB
  commands after the kind's checks.
- **FR-005**: Item devices MUST work with dashboard rows, Device Command,
  Device Value, Device Changed, Device Availability, conditions, history
  forwarding, and the event log as other devices do; NULL and UNDEF states
  read as unknown, and a missing item marks its device missing.
- **FR-006**: The system MUST offer an openHAB Item Changed trigger (items,
  groups, or a location; type, from, and to filters), a Send Command action,
  and a Get Item value node.
- **FR-007**: After a reconnect, the system MUST report each item whose state
  changed during the outage once, marked caught up, to triggers, devices, and
  dashboards, and an item trigger MUST offer to ignore caught-up changes.
- **FR-008**: Item states and commands MUST be checked against the item's
  state description (minimum, maximum, step, options, read only) before a
  command is sent.

### Key entities

- **openHAB connection**: base URL, API token, live status, and the item copy.
- **Item**: name, label, type (with dimension), state, state description,
  tags, group memberships, and location.
- **Item device**: a karakuri device bound to one item of one connection.

## Success criteria

- **SC-001**: Against openHAB in Docker, a dimmer, a rollershutter, a switch,
  and a number item each work as a device from a card and a flow, with the
  item's state in openHAB matching what karakuri sent.
- **SC-002**: An item change in openHAB reaches a device and a trigger within
  one second.
- **SC-003**: After openHAB restarts, every item changed during the outage is
  reported once and no unchanged item is.
- **SC-004**: A command the item's state description forbids is refused
  before anything reaches openHAB.

## Assumptions and scope

- openHAB 4 and later, through its REST API and server-sent event stream; the
  MQTT binding and the openHAB cloud connector are out of scope.
- An API token, when used, is created by the owner in openHAB; karakuri does
  not manage openHAB users.
- Editing openHAB items, things, rules, or the semantic model is out of scope;
  openHAB remains where they are managed.
- Testing uses a disposable openHAB in Docker with items defined for the
  tests; no openHAB instance in the house is touched.
- Domoticz, Jeedom, and Homey Pro are not part of this feature; Domoticz and
  Jeedom reach karakuri through their MQTT gateways with MQTT devices.

## References

- [Feature 004: connections and devices](../004-connections-devices/spec.md)
- [Feature 033: ESPHome native API](../033-esphome-native-api/spec.md)
- [Feature 038: media control kind](../038-media-control-kind/spec.md)
- [Feature 039: Home Assistant areas and devices](../039-ha-registries/spec.md)
