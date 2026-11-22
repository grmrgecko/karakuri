# Feature specification: ESPHome native API

**Feature**: `specs/033-esphome-native-api`
**Created**: 2026-10-31
**Status**: Implemented
**Input**: ESPHome native API. ESPHome nodes speak an encrypted API of their
own to Home Assistant; today karakuri can only reach them through Home
Assistant or through MQTT, which ESPHome nodes usually do not enable. Connect
to ESPHome nodes directly, the way Home Assistant does: find them on the
network, connect with the node's encryption key, list its entities, keep
their state live, draw them on dashboards with the right control, command
them from dashboards and flows, call the node's own actions, and know when a
node is offline. Together with Zigbee2MQTT discovery, this lets a household
run its devices without Home Assistant.

## User scenarios and testing

### User story 1: Connect an ESPHome node (Priority: P1)

An author opens Connections, sees the ESPHome nodes karakuri found on the
network, picks the garage controller, pastes its encryption key from the
node's YAML, and saves. The connection shows the node's name, model, and
ESPHome version, and lists its entities.

**Why this priority**: Every other story works through a connected node.
**Independent test**: Connect a node with an encryption key, test the
connection, and list its entities.

**Acceptance scenarios**:

1. **Given** ESPHome nodes announcing themselves on the local network,
   **When** an author adds an ESPHome connection, **Then** the nodes found are
   offered with their names and addresses, and a host can also be typed.
2. **Given** a node with an encryption key, **When** the connection is saved
   with the right key, **Then** it connects and reports the node's name,
   model, MAC address, ESPHome version, and entity count.
3. **Given** a wrong or missing key, or a node that uses the older password,
   **When** the connection is tested, **Then** it fails with the reason, or
   connects with the password when one is configured.
4. **Given** a saved connection, **When** anyone reads it, **Then** the key
   and password are masked like other secrets.

### User story 2: See and use the node's entities (Priority: P1)

The author adds the garage node's entities, all of them ticked by default.
The garage door cover, the door contact, the temperature sensor, and the
light relay become devices in the device list, like MQTT and Zigbee2MQTT
devices, each drawn on dashboards with its matching control. Opening the door
from the dashboard moves the door.

**Why this priority**: Drawing and commanding entities is the point of the
integration.
**Independent test**: Add a switch, a light, a cover, a sensor, a binary
sensor, a number, and a select from one node, bind rows to them, operate the
writable ones, and watch the readings change.

**Acceptance scenarios**:

1. **Given** a connected node, **When** the author adds its entities, **Then**
   each becomes a device named after the node and the entity, listed with the
   other devices and offered wherever a device is, and each draws as the
   matching kind: switch as a toggle, light as a light with the colour modes it
   supports, cover, fan, lock, climate, number, select, text, button, sensor
   and binary sensor readings, and text readings.
2. **Given** a writable entity, **When** a viewer operates it, **Then** the
   command reaches the node, and the row shows the state the node reports
   back, not an assumed one.
3. **Given** a value outside what the entity accepts, **When** it is sent,
   **Then** it is refused before reaching the node.
4. **Given** an entity the node reports as unavailable or with no state yet,
   **When** it is drawn, **Then** it shows as unavailable.
5. **Given** entities with units, device classes, and accuracy, **When** they
   are drawn, **Then** readings use the unit and precision the node gives.

### User story 3: Flows with ESPHome entities (Priority: P1)

A flow turns the porch light on when the garage door opens after sunset,
reads the garage temperature, and calls the node's own `beep` action.

**Why this priority**: Automation is what replaces Home Assistant.
**Independent test**: Build a flow that triggers on an entity change, reads
another entity, commands a third, and calls a node action.

**Acceptance scenarios**:

1. **Given** a flow with an ESPHome state trigger on an entity, **When** the
   entity changes, **Then** the flow runs with the old and new state and
   attributes.
2. **Given** a flow reading an entity, **When** it runs, **Then** it gets the
   current state, or fails with the reason when the node is offline.
3. **Given** a flow commanding an entity, **When** it runs, **Then** the value
   is checked against the entity first and then sent; a refusal or an
   offline node takes the error branch.
4. **Given** a node with user-defined actions, **When** a flow calls one with
   arguments, **Then** the arguments are checked against the action's
   declared types and the action runs on the node.
5. **Given** a node that sends events or Home Assistant style service calls,
   **When** they arrive, **Then** a flow trigger can start on them.

### User story 4: Know when a node is down (Priority: P2)

The garage node loses power. Its entities show unavailable on every
dashboard, flows reading or commanding them take their error branch, and a
flow notifies the household. When the node is back, everything resumes on its
own.

**Why this priority**: Nodes go offline with power cuts and Wi-Fi drops; a
dead node must not look alive.
**Independent test**: Power off or disconnect a node, then bring it back.

**Acceptance scenarios**:

1. **Given** a connected node, **When** it stops answering, **Then** the
   connection shows an error within the keepalive period, its entities are
   unavailable, and the connection status trigger fires.
2. **Given** a node that comes back, **When** it answers again, **Then** the
   connection reconnects on its own, entities are re-listed, and states are
   fresh.
3. **Given** a node whose address changed, **When** it announces itself on the
   network under the same name, **Then** the connection follows it.
4. **Given** a server restart, **When** it comes back, **Then** entity states
   from before are shown marked stale until the node reports.

### Edge cases

- A node reflashed with different entities: devices whose entity no longer
  exists are marked missing and draw as unavailable, as feature 032 does for
  Zigbee2MQTT; new entities are offered for adding. Devices follow the
  entity's object ID, which survives reflashing, not its numeric key, which
  may change.
- ESPHome accepts several API clients at once, so Home Assistant can stay
  connected to a node while karakuri is.
- Entity names may repeat across nodes; device names start with the node's
  name, with a numeric suffix if still taken.
- Light colour modes are taken from what the node reports; a light that only
  supports brightness offers only brightness.
- Commands to a node that is not connected are refused with the reason rather
  than queued.
- Discovery uses mDNS on the server's network; nodes on another subnet are
  added by host.
- Bluetooth proxy, voice assistant, camera streams, firmware updates, and
  compiling or flashing ESPHome firmware are out of scope.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer an ESPHome connection kind with host,
  port (default 6053), encryption key, and legacy password, with secrets
  encrypted and masked.
- **FR-002**: The system MUST list ESPHome nodes found on the local network
  when adding a connection, and follow a node to a new address by name.
- **FR-003**: A connection test MUST report the node's name, model, MAC
  address, ESPHome version, and entity count, or the reason it failed.
- **FR-004**: The system MUST keep a node connected, reconnect with backoff
  when it drops, detect a silent node within the keepalive period, and report
  the connection's status.
- **FR-005**: The system MUST list a node's entities with name, object ID,
  type, unit, device class, and what each supports, and keep their state live.
- **FR-006**: Adding a node's entities MUST create devices in the same device
  registry as MQTT devices, each with the control kind that matches its type
  and features, so dashboards, conditions, flows, permissions, availability,
  and history treat them as they treat any device; writable entities MUST be
  operable.
- **FR-007**: Commands MUST be checked against the entity's kind and features
  before they are sent, and MUST NOT update state optimistically.
- **FR-008**: Flows MUST be able to trigger on entity state changes and on
  node events, read entity states, command entities, and call user-defined
  node actions with typed arguments.
- **FR-009**: Entities of an unreachable node, and entities the node reports
  without a state, MUST draw as unavailable, and flows reading or commanding
  them MUST fail with the reason.
- **FR-010**: Entity states MUST be stored and restored as stale after a
  restart, as feature 029 does for MQTT devices.
- **FR-011**: Entity states MUST be available to history forwarding.
- **FR-012**: Devices MUST follow entities by object ID so they survive the
  node renumbering its entities, and MUST be marked missing, not deleted, when
  the entity disappears, unless auto import removes it (feature 049).
- **FR-013**: Entity lists, node actions, and the new nodes MUST be exposed
  through REST, MCP, and the editor with the same rules.

### Key entities

- **ESPHome connection**: one node: host, port, encryption key or password,
  and the node information it reports.
- **Entity**: one of the node's components: object ID, numeric key, type,
  name, unit, device class, supported features, and current state. Added, it
  is a device whose transport is the ESPHome connection.
- **Node action**: a user-defined action with named, typed arguments.

## Success criteria

- **SC-001**: A node on the local network is found, connected with its
  encryption key, and its entities listed without typing its address.
- **SC-002**: Each supported entity type on a test node, once added, draws
  with its matching control, and every writable one can be operated from a dashboard
  and from a flow.
- **SC-003**: A state change on the node reaches an open dashboard and starts
  a triggered flow within the same time a Home Assistant state change takes.
- **SC-004**: With the node powered off, its entities show unavailable within
  the keepalive period, and they recover without any action when it returns.
- **SC-005**: Relevant Makefile builds and tests pass, the connection form,
  entity pickers, and rows render without console errors, and the development
  service stays active.

## Assumptions and scope

- Nodes run ESPHome 2023.x or later with the native API enabled; the
  encryption key is the recommended setup and the legacy password is
  supported for older nodes.
- Karakuri is a client of the node, like Home Assistant; the node needs no
  change, and Home Assistant can stay connected at the same time.
- Live checks need a reachable ESPHome node; where none is available, the
  protocol is tested against a simulated node that speaks the native API.
- Bluetooth proxy, voice assistant, cameras, OTA updates, and the ESPHome
  dashboard or compiler are out of scope.

## References

- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Durable waits and device state](../029-durable-waits/spec.md)
- [Zigbee2MQTT discovery and device availability](../032-zigbee2mqtt-discovery/spec.md)
- [Time-series connections and history forwarding](../027-timeseries-connections/spec.md)
