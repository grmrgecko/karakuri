# Feature specification: Zigbee2MQTT discovery and device availability

**Feature**: `specs/032-zigbee2mqtt-discovery`
**Created**: 2026-10-31
**Status**: Implemented
**Input**: Zigbee2MQTT device discovery and MQTT device availability. Today
every MQTT device is written by hand: topics, a state script, a kind, its
options, and a payload script. A household with forty Zigbee devices behind
Zigbee2MQTT has to do that forty times, although Zigbee2MQTT already
publishes a full description of every device it manages. Read that
description, list the devices not yet in karakuri, and add the chosen ones as
ordinary MQTT devices with the kind, options, and scripts filled in from what
each device exposes. Keep added devices in step when they are renamed or
removed in Zigbee2MQTT. Separately, give every MQTT device an availability:
online, offline, or unknown, from an availability topic, from Zigbee2MQTT's
own availability messages, from the connection being down, or from a device
going quiet for longer than expected. Dashboards show an offline device as
unavailable, commands to it are refused, and flows can react when a device
goes offline or comes back.

## User scenarios and testing

### User story 1: Add Zigbee devices without writing scripts (Priority: P1)

An author points karakuri at the MQTT connection Zigbee2MQTT publishes on and
opens the discovery list. It shows every Zigbee device Zigbee2MQTT knows
about: name, model, vendor, what it is (a light, a plug, a door sensor, a
thermostat), and whether karakuri already has it. The author ticks the
kitchen lights and the door sensors and adds them. Each one appears as an MQTT
device that already works on a dashboard and from a flow.

**Why this priority**: Writing scripts by hand per device is the main reason
someone with Zigbee2MQTT keeps Home Assistant around.
**Independent test**: Publish a recorded Zigbee2MQTT device list to a test
broker, open the discovery list, add a colour light, a plug, a contact sensor,
and a thermostat, and switch the light and the plug from a dashboard.

**Acceptance scenarios**:

1. **Given** a connection with a Zigbee2MQTT bridge on its base topic,
   **When** the author opens discovery, **Then** every device except the
   coordinator is listed with its name, model, vendor, power source, a
   proposed kind, and whether it is already added.
2. **Given** a light exposing state, brightness, colour, and colour
   temperature, **When** it is added, **Then** it becomes a light device with
   those features on, a state script that reads its messages, and a payload
   script that commands each feature.
3. **Given** a plug, a contact sensor, a cover, a lock, a fan, or a
   thermostat, **When** it is added, **Then** it becomes the matching kind with
   the features it exposes; a read-only sensor becomes a reading with no
   payload script.
4. **Given** a device whose exposes include extra readings such as battery,
   link quality, temperature, or power, **When** it is added, **Then** those
   readings are kept in its state and offered as separate dashboard rows.
5. **Given** a device karakuri does not know how to draw, **When** it is
   listed, **Then** it is proposed as a text reading of its main value, and
   the author can still add it and edit the scripts.
6. **Given** a device already added, **When** discovery is opened again,
   **Then** it is marked added and is not offered twice.
7. **Given** the author edits an added device's scripts or kind, **When**
   discovery runs again, **Then** those edits are kept.

### User story 2: Know when a device is offline (Priority: P1)

The hallway motion sensor's battery dies. The dashboard row shows it as
unavailable instead of its last reading, a flow that turns the light on with
motion is told the sensor is offline, and a notification flow tells the
household.

**Why this priority**: A stale reading drawn as current is the failure people
notice; acting on a dead sensor is worse.
**Independent test**: With a device whose availability topic says online,
publish offline and watch the dashboard row, the device list, and a flow
triggered on availability change.

**Acceptance scenarios**:

1. **Given** a device with an availability topic, **When** the device
   publishes its offline payload, **Then** it becomes offline, dashboards draw
   it as unavailable, and the device list shows it offline with the time.
2. **Given** a device added from Zigbee2MQTT, **When** Zigbee2MQTT reports
   it offline in either its current JSON form or its older plain form,
   **Then** it becomes offline without any setting by the author.
3. **Given** a device with an expected reporting interval and no
   availability topic, **When** nothing arrives for longer than that
   interval, **Then** it becomes offline, and the next message brings it back
   online.
4. **Given** the MQTT connection a device speaks on is down, **When**
   dashboards and flows read the device, **Then** its availability is
   unknown, not online, until the connection is back and the device reports.
5. **Given** an offline device, **When** a dashboard or flow commands it,
   **Then** the command is refused with the reason, unless the author has
   allowed commands to offline devices, as a sleeping battery device may
   still receive queued commands.
6. **Given** a flow with a device availability trigger, **When** a device goes
   offline or comes back online, **Then** the flow runs once per change with
   the device, the new and old availability, and since when.
7. **Given** a server restart, **When** it comes back, **Then** each device's
   last availability is restored and marked stale like its values, until the
   broker or the device confirms it.

### User story 3: Keep added devices in step with Zigbee2MQTT (Priority: P2)

The author renames "0x00158d0001a2b3c4" to "Hallway motion" in Zigbee2MQTT,
and later removes a plug they gave away. Karakuri follows both without
breaking a dashboard or a flow.

**Why this priority**: Zigbee2MQTT topics follow the friendly name, so a
rename silently breaks every hand-written device today.
**Independent test**: Add a device from discovery, rename it in a recorded
device list, and check that its topics follow and that rows and flows bound
to it still work.

**Acceptance scenarios**:

1. **Given** an added device, **When** it is renamed in Zigbee2MQTT, **Then**
   its topics follow the new name and its karakuri name and bindings are
   unchanged.
2. **Given** an added device, **When** it disappears from Zigbee2MQTT's
   device list, **Then** it is marked missing in the device list and shown
   unavailable, and it is not deleted unless auto import removes it after the
   connection's "Remove after" duration (feature 049).
3. **Given** a missing device that reappears, **When** the list is refreshed,
   **Then** it is no longer missing.
4. **Given** a device whose exposes changed after a Zigbee2MQTT update,
   **When** the author asks to refresh it from discovery, **Then** the
   generated kind options and scripts are regenerated, after the author
   confirms that hand edits will be replaced.

### User story 4: Availability for any MQTT device (Priority: P2)

A Tasmota plug and an ESP sensor with a last-will message already publish
online and offline on their own topics. The author sets each device's
availability topic and payloads by hand.

**Why this priority**: Availability is not specific to Zigbee; most MQTT
firmware publishes a last-will message.
**Independent test**: Configure an availability topic with custom payloads on
a hand-written device and drive it from a test client.

**Acceptance scenarios**:

1. **Given** a device with an availability topic and online and offline
   payloads, **When** either payload arrives, **Then** availability follows
   it; any other payload is ignored and noted on the device.
2. **Given** a device with no availability settings, **When** messages
   arrive, **Then** its availability is online while the connection is up,
   as today.

### Edge cases

- Zigbee2MQTT's base topic is not always `zigbee2mqtt`; discovery uses the
  base topic the author gives when opening it, defaulting to `zigbee2mqtt`,
  and added devices remember it.
- Several Zigbee2MQTT instances on one broker are told apart by base topic.
- Discovery reads Zigbee2MQTT's retained device list. If it has not arrived
  within a few seconds, discovery says Zigbee2MQTT was not found on that base
  topic.
- A device name already used by another karakuri device gets a numeric suffix
  when added.
- Zigbee2MQTT groups are not listed.
- A device that Zigbee2MQTT reports as unsupported or still interviewing is
  listed but cannot be added until its interview completes.
- Offline overrides staleness: a restored device that reported offline before
  the restart shows offline, not stale.
- Commands to offline devices that are allowed still do not update state
  optimistically.
- Availability changes reach history forwarding (feature 027) as an attribute
  of the device.
- An expected reporting interval shorter than one minute is refused.

## Requirements

### Functional requirements

- **FR-001**: The system MUST discover Zigbee devices from the Zigbee2MQTT
  device list on a chosen MQTT connection and base topic, listing name, IEEE
  address, model, vendor, description, power source, interview state,
  proposed kind, and whether each device is already added.
- **FR-002**: Adding a discovered device MUST create an ordinary MQTT device
  with topics, kind, kind options, state script, and payload script generated
  from the device's exposes, and record which Zigbee2MQTT device it came from.
- **FR-003**: The generated mapping MUST cover lights (on, brightness, colour,
  colour temperature, effects), switches and plugs, covers (state, position,
  tilt), locks, fans, climate (mode, setpoints, current temperature, fan
  mode), binary sensors, and numeric and text readings; extra readings MUST be
  kept as device state fields.
- **FR-004**: Added devices MUST follow renames in Zigbee2MQTT by IEEE
  address, keep their karakuri name and bindings, be marked missing when
  removed, and keep hand edits unless the author asks to regenerate. Feature
  049 may remove a missing device that auto import added.
- **FR-005**: Every MQTT device MUST have an availability of online, offline,
  or unknown, with the time it last changed.
- **FR-006**: A device MUST take availability from, in order: the connection
  being down (unknown), its availability topic and payloads, Zigbee2MQTT's
  availability messages for added Zigbee devices, an optional expected
  reporting interval, and otherwise online while the connection is up.
- **FR-007**: Dashboards MUST draw an offline or unknown device as
  unavailable, and the device list MUST show availability and its time.
- **FR-008**: Commands to an offline device MUST be refused with the reason
  unless the device allows commands while offline.
- **FR-009**: Flows MUST be able to start on a device's availability change,
  and device reads in flows MUST report availability.
- **FR-010**: Availability MUST be stored with device state and restored as
  stale after a restart, as feature 029 does for values.
- **FR-011**: Discovery and adding MUST require the permissions that creating
  MQTT devices requires; dashboard viewers MUST see availability only for
  devices they may already read.
- **FR-012**: Discovery, availability settings, and the availability trigger
  MUST be exposed through REST, MCP, and the editor with the same rules.

### Key entities

- **Discovered device**: a Zigbee2MQTT device as listed: IEEE address, friendly
  name, model, vendor, power source, exposes, interview state, and the
  karakuri device added from it, if any.
- **Availability**: a device's online, offline, or unknown state, its source,
  and when it last changed.
- **Availability settings**: per device, an availability topic, online and
  offline payloads, an expected reporting interval, and whether commands are
  allowed while offline.

## Success criteria

- **SC-001**: From a recorded Zigbee2MQTT device list of at least ten device
  types, every supported device can be added in one action and switched or
  read from a dashboard with no script edited by hand.
- **SC-002**: A device that goes offline is drawn as unavailable on an open
  dashboard and starts an availability flow within the same time a value
  change takes to reach them.
- **SC-003**: A device renamed in Zigbee2MQTT keeps working on its dashboards
  and in its flows without being edited in karakuri.
- **SC-004**: After a restart, every device shows the availability it had,
  marked stale, until the broker confirms it.
- **SC-005**: Relevant Makefile builds and tests pass, the discovery list and
  availability settings render without console errors, and the development
  service stays active.

## Assumptions and scope

- Zigbee2MQTT 1.x and 2.x device lists and both of its availability payload
  forms are supported; the devices are read from the retained device list it
  publishes on its bridge topic.
- Pairing new Zigbee devices, renaming them, groups, OTA updates, and other
  bridge administration stay in Zigbee2MQTT's own interface.
- Home Assistant MQTT discovery messages (used by Tasmota, ESPHome over MQTT,
  Shelly, and others) and adding Zigbee2MQTT devices without picking them
  belong to [device auto import](../049-device-auto-import/spec.md); the
  availability settings in story 4 cover hand-written devices.
- Added devices are ordinary MQTT devices: everything that works on a
  hand-written device works on them, and an author can edit any part.
- No migration is needed: existing devices have no availability settings and
  behave as they do today.

## References

- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Durable waits and device state](../029-durable-waits/spec.md)
- [Time-series connections and history forwarding](../027-timeseries-connections/spec.md)
- [MQTT device state scripts](../../docs/mqtt-device-state-script.md)
- [MQTT device payload scripts](../../docs/mqtt-device-payload-script.md)
