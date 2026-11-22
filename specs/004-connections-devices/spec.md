# Feature specification: Connections, MQTT devices, and controls

**Feature**: `004-connections-devices`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Connect automations to Home Assistant and MQTT, represent devices with typed
controls, and discover real input shapes before issuing actions. Then, as two
correctness fixes: after a Home Assistant reconnect, catch up on every entity that
changed during the outage so triggers and dashboards stop showing pre-outage values;
and record which flow and run wrote a control, so Control Changed ignores only its own
flow's writes and a loop between flows writing controls is stopped and reported. Then,
for MQTT devices: per-device QoS for state subscriptions and commands, a retain flag
for commands, a `retained` flag telling state scripts whether a message came from the
broker's retained store, and per-device time limits for the state and payload
scripts.

## User scenarios and testing

### User story 1: Reuse an integration connection (Priority: P1)

An operator saves one tested connection for several flows and dashboards.

**Why this priority**: Connectivity and protected credentials are prerequisites for
device work.

**Independent test**: Save and test an enabled connection, inspect its status, and use
it from two consumers.

**Acceptance scenarios**:

1. **Given** a registered connection kind with required fields, **When** the operator
   saves invalid configuration, **Then** the save is refused with field or driver
   validation context.
2. **Given** an enabled valid connection, **When** the server starts,
   **Then** it attempts driver startup and reports connection status or failure to
   interested consumers without discarding the saved definition.

3. **Given** a failed startup with no running driver, **When** its status is read,
   **Then** the retained failure is visible without changing the saved definition.

### User story 2: Model an MQTT device (Priority: P1)

An operator describes state topics and command payloads once for every dashboard row
using the device.

**Why this priority**: A widget must not implement its own device protocol.

**Independent test**: Feed partial state messages and dry-run a validated control change
through device scripts.

**Acceptance scenarios**:

1. **Given** a device with brightness already known, **When** a state message reports
   only color, **Then** color is merged and brightness stays known.
2. **Given** a payload script returns no topic, **When** a command or test is evaluated,
   **Then** no publish occurs and the result identifies the no-op.

### User story 3: Discover and invoke real capabilities (Priority: P2)

An author reads HA services, captures MQTT samples, or sets a local control before
wiring a graph.

**Why this priority**: Observed schemas reduce guesswork in automation inputs.

**Independent test**: List HA services, capture a subscribed sample without publishing,
and set a bounded numeric control.

**Acceptance scenarios**:

1. **Given** an out-of-range control value, **When** the user attempts a set, **Then**
   validation refuses it before an MQTT payload script or action runs.
2. **Given** an active HA Assist route, **When** Home Assistant sends a matching
   request, **Then** the flow responds through the same HA connection with the
   correlated answer.

### User story 4: Catch up after a Home Assistant outage (Priority: P1)

Home Assistant restarts for an update while the front door is opened and the hallway
light is turned on. When karakuri reconnects, a flow watching the door runs for the
door now being open, and the wall panel shows the light on, without anyone reloading
it.

**Why this priority**: Today a reconnect silently skips everything that happened
during the outage: triggers never fire and dashboards keep showing old values until a
reload. An automation engine that misses the front door opening is not reliable.

**Independent test**: Change several entities while the connection is down, bring it
back, and check that each watching trigger runs once with the current state and that
an open dashboard shows the current values without a reload.

**Acceptance scenarios**:

1. **Given** a flow triggered by `binary_sensor.front_door` turning on, **When** the
   door opens while Home Assistant is unreachable and karakuri then reconnects,
   **Then** the flow runs once with the door's current state, marked as caught up
   after a reconnect.
2. **Given** an entity that changed three times during the outage, **When** karakuri
   reconnects, **Then** the trigger runs once for its latest state, not three times.
3. **Given** an entity that did not change during the outage, **When** karakuri
   reconnects, **Then** no trigger runs for it.
4. **Given** an open dashboard showing the hallway light off, **When** the light is
   turned on during the outage and karakuri reconnects, **Then** the dashboard shows
   it on within a few seconds without a reload.
5. **Given** a trigger whose author chose to ignore caught-up changes, **When**
   karakuri reconnects, **Then** that trigger does not run for them.

---

### User story 5: Hand a control from one flow to another (Priority: P1)

A "goodnight" flow sets the `house_mode` control to night. A separate "night mode"
flow, started by Control Changed on `house_mode`, turns the lights off. Today the
second flow never runs, because Control Changed ignores writes from every flow, not
just its own.

**Why this priority**: Controls are how flows hand state to each other; a handoff that
silently does nothing breaks the pattern the documentation recommends for modes and
presence.

**Independent test**: Flow A sets a control; flow B, triggered by that control,
runs once. Two flows that each set the control the other watches are stopped by the
loop guard with a reported reason.

**Acceptance scenarios**:

1. **Given** flow B triggered by Control Changed on `house_mode` with writes from its
   own flow ignored, **When** flow A sets `house_mode`, **Then** flow B runs once.
2. **Given** the same trigger, **When** flow B itself sets `house_mode`, **Then**
   flow B does not start again.
3. **Given** a trigger set to ignore writes from any flow, **When** flow A sets the
   control, **Then** flow B does not run, as before this change.
4. **Given** flow A triggered by control X writing control Y and flow B triggered by
   Y writing X, **When** a dashboard sets X, **Then** the chain stops once it reaches
   the loop limit, the trigger that would exceed it records a skipped start naming the
   loop, and nothing runs forever.
5. **Given** a Control Changed message from a flow's write, **When** a flow reads it,
   **Then** it carries the writing flow and run.

### User story 6: Tune an MQTT device's delivery (Priority: P2)

A garage door controller on a flaky Wi-Fi link drops QoS 0 commands, so the owner
sets the device to command at QoS 1. A thermostat's set-point topic must be retained
so the thermostat picks it up after it reboots. A state script for a bridge that
replays its last state as a retained message on every reconnect ignores retained
"pressed" events, so a button pressed yesterday does not fire again today. A state
script that parses a large JSON document from an energy meter needs more than a
quarter second.

**Why this priority**: Without these, devices that need reliable delivery or
retained commands need a hand-built flow instead of a device, and state scripts
cannot tell a stale retained message from a live one.

**Independent test**: Set QoS and retain on a test device, command it, and capture
the publish; send a retained and a live message and read `retained` in the state
script; raise a script's time limit and see a slow script succeed.

**Acceptance scenarios**:

1. **Given** a device with command QoS 1 and retain on, **When** it is commanded,
   **Then** the message is published at QoS 1 with the retain flag set.
2. **Given** a payload script that returns `qos` or `retain` with its topic,
   **When** it runs, **Then** those values apply to that message instead of the
   device's defaults.
3. **Given** a device with state QoS 1, **When** its topics are subscribed,
   **Then** the subscription asks the broker for QoS 1, and the device shows the QoS
   the broker granted when it is lower.
4. **Given** a retained message on a device's topic, **When** the device subscribes,
   **Then** its state script sees `retained` true; a message published while it is
   subscribed arrives with `retained` false.
5. **Given** a state script time limit of one second, **When** a script takes half
   a second, **Then** it succeeds; with the default limit it fails with a timeout
   reported like any decode failure.

### Edge cases

- A device commanded with retain on leaves its last command on the broker; clearing
  it is a publish of an empty retained message, which a payload script can return.
- Retained commands are echoed to a device subscribed to its own command topic;
  `retained` lets its state script ignore the echo after a reconnect.
- Two devices subscribing the same topic at different QoS share one broker
  subscription at the higher QoS; each still sees every message.
- A script time limit outside its range is refused when the device is saved, not
  clamped.

- A failed state script retains the last good device values and reports the failure.
- A reporting-only MQTT device rejects commands when its payload script is empty.
- Button controls notify repeated presses even when the action value is unchanged.
- An entity removed from Home Assistant during an outage is reported once as gone,
  as Home Assistant reports a removed entity, and an entity added during it once as new.
- A reconnect that fails partway through reading states catches up on nothing and
  tries again on the next successful connection; it never reports half a catch-up as
  complete.
- A catch-up after a long outage over thousands of entities sends only the changed
  ones, and a dashboard updates only the widgets showing them.
- The first connection after the server starts is not an outage: entity states
  already restored or read at startup do not fire triggers as caught-up changes.
- Disabling and enabling a connection, or editing its settings, is caught up like a
  dropped connection. Pointing it at a different Home Assistant address starts over,
  since another server's states are no baseline.
- A run the loop limit refuses appears in the flow's runs as canceled, with the
  chain as its reason.
- A control written by a dashboard, the API, or MCP carries no flow; it starts every
  Control Changed trigger watching it, as it does today.
- The loop limit counts control-to-flow-to-control hops in one causal chain, not
  writes in general: a flow that sets the same control twice in one run is not a
  loop.
- A chain that passes through a delay, a wait, or another trigger kind (a timer, an
  MQTT message) is a new chain; only direct control handoffs count toward the limit.

## Requirements

### Functional requirements

- **FR-001**: The system MUST provide registered connection kinds with schemas, secret
  field declarations, validation, test hooks, and live lifecycle status.
- **FR-002**: The system MUST encrypt saved connection configs, mask declared secrets,
  and reuse enabled drivers across consumers.
- **FR-003**: The system MUST support MQTT subscriptions and publishing with explicit
  payload formats and standard topic filter matching.
- **FR-004**: The system MUST support Home Assistant WebSocket state reads, state/event
  triggers, service discovery, and service calls.
- **FR-005**: The system MUST manage OAuth2 connections and provider connection kinds
  through their registered lifecycle rather than hard-coded UI forms.
- **FR-006**: The system MUST use a common control-kind catalog to validate
  scalar/composite controls and describe HA entities and MQTT devices.
- **FR-007**: The system MUST persist local control values and publish changes; repeated
  button presses count as changes.
- **FR-008**: The system MUST subscribe saved MQTT devices independently of open
  dashboards and merge only state fields returned by their state script.
- **FR-009**: The system MUST pass concrete topic, textual payload or base64 binary
  payload, and previous state to state scripts; allow null to ignore a message.
- **FR-010**: The system MUST run device state scripts without host/network access under
  the device's state script time limit (250 ms by default, 50 ms to 5 s) and retain last
  good state on failure.
- **FR-011**: The system MUST validate field/value changes before payload scripts; pass
  field, value, optional on, and previous value to the script. Devices with no declared
  control kind MUST pass field/value through; unknown nonempty kinds MUST be rejected.
- **FR-012**: The system MUST publish string payloads verbatim and encode other payloads
  as JSON; publish nothing when the payload script returns no topic.
- **FR-013**: The system MUST run payload scripts without network access under the
  device's payload script time limit (2 s by default, 100 ms to 10 s) and offer script
  tests that neither publish nor mutate live state.
- **FR-014**: The system MUST normalize brightness, cover position, and fan percentage
  to 0..100, light color to a hex value, and color temperature to kelvin.
- **FR-015**: The system MUST offer bounded MQTT sample capture, HA service metadata,
  and schedule preview without arming or firing an automation.
- **FR-016**: The system MUST route HA Assist requests by configured agent ID over the
  existing HA connection and return correlated text, response envelopes, or streaming
  progress.
- **FR-017**: Connection updates MUST preserve a stored declared secret when its masked
  sentinel is submitted. Undecryptable configs MUST remain listable with empty config
  and an error status so an operator can replace them. A driver startup failure MUST
  leave a successfully saved connection definition intact.
- **FR-018**: MQTT device definitions MUST persist. Accumulated device values MUST be
  stored and restored as stale after restart under feature 029. Commands MUST NOT update
  observed state optimistically; a later state message supplies that state.

- **FR-019**: HA calls MUST accept entity, area, device and label registry targets,
  while dashboard invocation retains its allowed-entity restrictions.
- **FR-020**: Optional HA confirmation MUST observe one explicit entity using fresh
  reads, route timeout separately, and never resend an accepted command due to
  confirmation failure.
- **FR-021**: HA state holds MUST isolate entity timers, cancel on contrary selected
  values before filters, and invalidate on disarm or event continuity gaps.

- **FR-022**: The manager MUST retain process-local lifecycle reports, including
  disabled and failed-start states without a driver, and provide an atomic
  snapshot/watch subscription. Retired driver callbacks MUST NOT overwrite the
  replacement status. Repeated states MUST preserve their entry timestamp.

- **FR-023**: After a Home Assistant connection reconnects from an outage, the system
  MUST read every entity's state once and report, for each entity whose state or
  attributes differ from the last state it knew, one change from that last state to
  the current one, marked as caught up after a reconnect. Entities that did not
  change MUST NOT be reported. Removed and added entities MUST be reported as Home
  Assistant reports them.
- **FR-024**: Home Assistant state triggers MUST start once for each caught-up change
  that matches their filters, carrying the caught-up mark, unless the trigger's author
  chose to ignore caught-up changes. State holds MUST treat the outage as a continuity
  gap under FR-021.
- **FR-025**: After a catch-up, open dashboards showing that connection's entities
  MUST show current values within 5 seconds without a reload; each widget updates once
  for each of its entities that changed, and not at all for the rest.
- **FR-026**: A control change MUST record what wrote it: a dashboard, the API, MCP,
  or a flow, and for a flow its flow and run. It MUST also carry the actor the
  event log records (feature 037): the user or API key and the dashboard for
  dashboard, API, and MCP writes. Control Changed MUST offer ignoring
  writes from none, from its own flow (the default), or from any flow, and its message
  MUST carry the writer.
- **FR-027**: A run started by a control change written by a flow MUST carry the
  chain's depth, one more than the run that wrote it. A Control Changed trigger MUST
  NOT start a run whose depth would exceed the loop limit, 8 by default; it MUST
  record a skipped start naming the controls and flows of the chain, and log it.
  A depth reset happens whenever a run starts from anything other than a control
  change written by a flow.

- **FR-028**: MQTT devices MUST carry a state QoS (0, 1, or 2, default 0) used for
  their state and availability subscriptions, and a command QoS (default 0) and
  command retain flag (default off) used for their commands. A payload script MAY
  return `qos` and `retain` to override them for one message.
- **FR-029**: State scripts MUST receive `retained`, true when the message was
  delivered from the broker's retained store and false for a live message.
- **FR-030**: Connections and the devices on them MUST be reachable only by users
  holding use or above on the connection, its owner, or holders of
  `api:connections:manage`, as [feature 047](../047-connection-agent-access/spec.md)
  defines; a device follows its connection.

### Key entities

- Connection: kind, encrypted configuration, enablement, and driver status.
- Control: owned typed value and validation options. Each change records its writer:
  dashboard, API, MCP, or a flow with its flow, run, and chain depth.
- MQTT device: connection, state filters, kind, scripts with their time limits, state
  and command QoS, command retain, buttons, and last known values with their report time
  and stale flag.
- HA Assist request: request ID, route, transcript, and continuation envelope.

## Success criteria

- **SC-001**: Partial MQTT messages preserve omitted state fields; a failed message
  changes none of the last good values.
- **SC-002**: Script dry-runs publish zero broker messages and perform zero live device
  state writes.
- **SC-003**: Out-of-range commands are rejected before device protocol code executes.
- **SC-004**: An Assist fixture correlates the response to its request and retains the
  full continuation envelope for the next turn.
- **SC-005**: With ten entities changed during an outage, every matching trigger runs
  exactly once per changed entity within 5 seconds of the reconnect, and none runs for
  unchanged entities.
- **SC-006**: An open dashboard shows every entity's current value within 5 seconds of
  a reconnect, without a reload.
- **SC-007**: A handoff from one flow to another through a control runs the second flow
  once; two flows writing each other's controls stop at the loop limit within a second,
  with one skipped start recorded.
- **SC-008**: A command from a device set to QoS 1 with retain is captured on the
  broker with QoS 1 and the retain flag; one with defaults is captured at QoS 0
  without it.
- **SC-009**: Of two messages on a device's topic, the one delivered from the retained
  store reaches the state script with `retained` true and the live one with false.

## Assumptions and scope

Device commands perform real external actions. The Home Assistant catch-up reports
latest states, not history: transitions that came and went during an outage are not
replayed, and a flow needing them reads Home Assistant's history (feature 027).
Existing Control Changed triggers with "ignore writes from flows" set now ignore only
their own flow's writes; a graph that relied on ignoring every flow sets the new "any
flow" option. MQTT device definitions are authored
explicitly or added from Zigbee2MQTT discovery under
[feature 032](../032-zigbee2mqtt-discovery/spec.md), which also gives every device an
availability, or added by a connection's auto import under
[feature 049](../049-device-auto-import/spec.md). The Assist component targets the
documented Home Assistant interfaces. Existing MQTT devices keep QoS 0, no retain, and
the 250 ms and 2 s script limits. A device still republishes its state for every
message its script decodes, unchanged or not: a repeated report is meaningful for
devices that report events, such as a button reporting the same press twice.

## Network socket extension

[Feature 019](../019-network-socket-nodes/spec.md) adds settings-owned TCP, UDP
and WebSocket connections under the same encrypted records and lifecycle.
Its protocol requirements govern framing, session replies, multicast and
listener policy. See [network socket nodes](../../docs/network-sockets.md).

## Time-series extension

[Feature 027](../027-timeseries-connections/spec.md) adds Prometheus, InfluxDB,
and PostgreSQL connections under the same lifecycle, Home Assistant
history and statistics queries, and history forwarding from controls, MQTT
devices, and Home Assistant entities. The control and MQTT device editors gain
its history setting.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [docs/mqtt-device-state-script.md](../../docs/mqtt-device-state-script.md)
- [docs/mqtt-device-payload-script.md](../../docs/mqtt-device-payload-script.md)
- [Durable waits and device state](../029-durable-waits/spec.md)
- [homeassistant/README.md](../../homeassistant/README.md)
- [pkg/connops/connops.go](../../pkg/connops/connops.go)
- [pkg/controls/kinds.go](../../pkg/controls/kinds.go)
- [pkg/mqttrelay/device.go](../../pkg/mqttrelay/device.go)
