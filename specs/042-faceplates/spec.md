# Feature specification: Faceplates

**Feature**: `specs/042-faceplates`
**Created**: 2026-11-21
**Status**: Implemented 2026-11-21
**Input**: Give a flow simple settings screens so someone can adjust its
important options without finding them in the graph and its subflows. A
Faceplate node is one named tab of controls whose values feed the flow
through value outputs. A flow with faceplates opens on its first faceplate
tab, with the flow editor as the first tab. Controls can show a bound
entity's live reading, can use an HTML and JavaScript template for a custom
UI, and a faceplate script can change other controls when a value changes.
Apply saves and deploys. An administrator can let users work with faceplates
without access to flows by sharing the flow at faceplate level (feature 043).
Kuri can build faceplates.

## User scenarios and testing

### User story 1: Tune a flow from a settings screen (Priority: P1)

A flow drives a TV's art mode brightness from a light sensor in the room. The
author adds a Faceplate node named "Simple" with a light sensor entity
control, a minimum and maximum brightness, and an art gallery choice, and
wires its outputs into the nodes that use them. Later, anyone who opens the
flow lands on the Simple tab, sees the sensor's current reading beside its
field, changes the maximum brightness, and presses Apply. The TV follows the
new maximum on the next reading without anyone opening the graph.

**Why this priority**: It is the feature. Every later story builds on a
faceplate tab whose values reach the deployed flow.
**Independent test**: Add a Faceplate node with a number control wired into a
node parameter, open the flow, change the value on the tab, Apply, and run the
flow to see the new value used.

**Acceptance scenarios**:

1. **Given** a flow with one Faceplate node named "Simple", **When** a user
   opens the flow, **Then** the Simple tab is shown and the flow editor is
   the first tab in the tab bar.
2. **Given** a flow with no Faceplate node, **When** a user opens it, **Then**
   the flow editor opens as it does today and no tab bar is shown.
3. **Given** a number control wired to a node parameter, **When** the user
   changes it on the tab and presses Apply, **Then** a new numbered version is
   deployed and later runs use the new value.
4. **Given** an entity control bound to a light sensor, **When** the tab is
   open, **Then** the sensor's current reading is shown beside the field and
   updates while the tab stays open.
5. **Given** a value outside a control's declared range or type, **When**
   Apply is pressed, **Then** nothing is saved or deployed and the control
   shows the error.
6. **Given** a draft that fails strict validation, **When** an author presses
   Apply, **Then** nothing is deployed and the validation errors are shown,
   as Deploy does.

### User story 2: Split settings across named tabs (Priority: P1)

The author adds a second Faceplate node named "Advanced" holding the
brightness curve and a third named "Gallery" holding the art choices. The tab
bar shows Flow editor, Simple, Advanced, Gallery. The author drags Gallery
before Advanced.

**Why this priority**: Separating everyday settings from rare ones is what
makes a faceplate simpler than the graph.
**Independent test**: Add three Faceplate nodes, rename one, reorder the
tabs, save, and reopen the flow.

**Acceptance scenarios**:

1. **Given** three Faceplate nodes, **When** the flow opens, **Then** one tab
   per node is shown after the flow editor tab, in the flow's saved tab
   order, and the first faceplate tab is selected.
2. **Given** a renamed Faceplate node, **When** the draft is saved, **Then**
   the tab shows the new name.
3. **Given** a reordered tab bar, **When** the flow is saved and reopened,
   **Then** the order is kept.
4. **Given** a Faceplate node deleted from the graph, **When** the draft is
   saved, **Then** its tab is gone and any edge from its outputs is removed
   with it.

### User story 3: Keep related controls in step with a script (Priority: P2)

On the Simple tab, minimum and maximum brightness set the range of the curve
on the Advanced tab. The Simple faceplate's change script rebuilds the curve's
points whenever either value changes, so the Advanced tab shows the matching
curve before Apply. Someone who then edits points on the Advanced tab keeps
their custom curve until they change the range again.

**Why this priority**: Without it, the simple and advanced tabs drift apart
and the author has to explain which one wins.
**Independent test**: Write a change script on one faceplate that sets a
control on another, change the first value, and see the second change on its
tab before Apply.

**Acceptance scenarios**:

1. **Given** a change script that sets the Advanced tab's curve from the
   Simple tab's range, **When** the range changes, **Then** the curve on the
   Advanced tab changes before Apply.
2. **Given** a change script that throws, **When** a value changes, **Then**
   the edited value is kept, the other controls are unchanged, and the error
   is shown on the tab.
3. **Given** a change script that sets a value of the wrong type, **When**
   Apply is pressed, **Then** server validation rejects it like any other bad
   value.
4. **Given** two scripts that set each other's controls, **When** one value
   changes, **Then** the chain stops after a bounded number of rounds and an
   error is shown instead of the page hanging.

### User story 4: Build a custom control (Priority: P2)

The author needs a curve editor: a chart of sensor reading against
brightness with draggable points and a marker at the sensor's current
reading. They add an array control to the Advanced faceplate and give it an
HTML and JavaScript template that draws the editor.

**Why this priority**: Built-in control types cannot cover every device, and
a template keeps unusual UI out of the core product.
**Independent test**: Add an object control with a template that renders two
inputs, edit them, Apply, and read the deployed value.

**Acceptance scenarios**:

1. **Given** an array control with a template, **When** the tab opens,
   **Then** the template renders with the control's current value and theme.
2. **Given** a template that calls set-value, **When** Apply is pressed,
   **Then** that value is validated against the declared type and deployed.
3. **Given** a template that reads another control's value or a bound
   entity's live reading, **When** that value changes, **Then** the template
   is told about the change.
4. **Given** a template that tries to reach the network or the parent page,
   **When** it runs, **Then** the attempt is blocked and the rest of the tab
   keeps working.
5. **Given** a template that fails to load or throws, **When** the tab
   renders, **Then** that control shows an error and a plain editor for its
   value type, and the other controls still work.

### User story 5: Let someone adjust settings without flow access (Priority: P2)

An administrator wants a household member to pick the art gallery and adjust
brightness without seeing or changing the graph. They share the flow with
the member at faceplate level. The member sees the flow's faceplate tabs but
no flow editor tab, and their Apply deploys only their value changes.

**Why this priority**: Faceplates are most useful to people who should not
edit automations.
**Independent test**: Log in as a user holding a faceplate share, change a
value, Apply, and confirm the deployed graph matches the previous
version except for that value, and that the graph routes refuse the user.

**Acceptance scenarios**:

1. **Given** a user holding a faceplate share on the flow, **When** they open
   it, **Then** they see its faceplate tabs and no flow editor tab, and the
   graph routes refuse them.
2. **Given** a faceplate-level Apply while an author has unsaved or saved draft
   edits, **When** it deploys, **Then** the new version is the deployed
   version plus that user's value changes, the author's draft graph edits
   stay undeployed, and the draft takes the new values.
3. **Given** a faceplate-level user, **When** they try to change a control's
   definition, a template, a script, or the tab order, **Then** the change is
   refused.
4. **Given** a flow with no Faceplate node, **When** a faceplate-level user
   lists flows, **Then** it is not listed.

### User story 6: Ask Kuri to build a faceplate (Priority: P3)

The author asks Kuri to make a settings screen for the TV flow with the
options that matter. Kuri reads the graph, adds Faceplate nodes with suitable
controls and labels, wires their outputs to the fields they replace, and
leaves the draft for review.

**Why this priority**: It removes the setup work, but faceplates are useful
without it.
**Independent test**: Through MCP, add a Faceplate node with two controls,
wire them, read the faceplate back, and set a value.

**Acceptance scenarios**:

1. **Given** MCP access at edit level on the flow, **When** Kuri adds a
   faceplate and wires it, **Then** the same graph validation applies as for
   any other node and the draft is not deployed.
2. **Given** MCP access, **When** a client reads a flow's faceplates, **Then**
   it receives each tab's name, order, controls, types, and current values.
3. **Given** MCP access by a faceplate-level user, **When** a client sets
   values and applies, **Then** the same rules as story 5 apply.

### User story 7: Use one subflow with different settings (Priority: P3)

Two TVs use the same shared art mode subflow, which contains a Faceplate
node. Each parent flow shows the subflow's tabs under the subflow node's
label, and each keeps its own values.

**Why this priority**: Shared subflows are the main reuse path, and shared
values would make one room's tuning change another's.
**Independent test**: Call one shared subflow from two flows, set a different
value in each, and run both.

**Acceptance scenarios**:

1. **Given** a shared subflow with a Faceplate node, **When** a parent flow
   that calls it opens, **Then** the subflow's tabs appear in the parent's
   tab bar under the subflow node's label.
2. **Given** two parents calling the same subflow, **When** each applies a
   different value, **Then** each parent's runs use its own value.
3. **Given** a parent that never set a value, **When** it runs, **Then** the
   subflow's own faceplate value is used.
4. **Given** a new subflow deployment that removes or retypes a control,
   **When** a parent opens or deploys, **Then** values that no longer fit are
   dropped back to the subflow's value and the parent is told which ones.

### Edge cases

- A control with no wired output still keeps its value and stays on its tab;
  validation does not require faceplate outputs to be wired.
- A Faceplate node with no controls shows an empty tab with a prompt to add
  controls for users who can edit the flow, and is hidden from faceplate-level
  users.
- A bound entity that is unavailable, or a connection that is down, shows the
  reading as unavailable rather than the last value without warning.
- Two users apply at the same time: the second Apply is refused as stale and
  shows the values that won, matching feature 002's revision rules.
- Restoring an earlier version restores its faceplate values into the draft,
  like any other config, without deploying.
- Export includes faceplates, templates, scripts, and values. Import keeps
  them. Entity bindings follow the same rebinding rules as other
  connection-dependent fields.
- Local subgraphs are part of the flow document, so their Faceplate nodes are
  tabs of the flow, not of a separate caller.
- Tab names need not be unique; tabs are identified by node.

## Requirements

### Functional requirements

- **FR-001**: The system MUST provide a Faceplate node type whose config holds
  a tab name, an ordered list of controls, each control's current value, and
  an optional change script. Each control MUST produce one pure value output.
- **FR-002**: A control MUST declare an ID, a label, a value type (boolean,
  integer, number, string, array, or object), and a default, and MAY declare
  a description, a range or step for numbers, a list of options, an entity
  binding, and a template. The canvas MUST update the node's outputs when
  controls are added, removed, or retyped.
- **FR-003**: Built-in editors MUST cover each value type, plus option lists
  and entity pickers. A number control MUST honor its range and step.
- **FR-004**: Strict and lenient validation MUST check every control value
  against its declared type, range, and options. Lenient validation MUST
  allow incomplete control definitions in drafts; strict validation MUST
  reject them.
- **FR-005**: The flow page MUST show a tab bar when the flow, or a shared
  subflow it calls, has a Faceplate node. The first tab MUST be the flow
  editor for users at view level or above, followed by faceplate tabs in the
  flow's saved order. The flow MUST open on the first faceplate tab.
- **FR-006**: Users at edit level or above MUST be able to reorder faceplate
  tabs. The order MUST be versioned with the flow.
- **FR-007**: A control with an entity binding MUST show that entity's live
  reading next to it while the tab is open, and show unavailability when the
  reading cannot be had.
- **FR-008**: Apply MUST validate all faceplate values, save the draft
  including any draft graph edits, and deploy it as a new numbered version,
  for a user at edit level or above. It MUST fail with the same strict
  validation errors as Deploy.
- **FR-009**: A user holding the faceplate share level on a flow, as feature
  043 defines, MUST be able to list it when it has a Faceplate node, read its
  tabs, set control values, and Apply, and MUST NOT be able to read or change
  the graph, control definitions, templates, scripts, or tab order. View
  level MUST show faceplate tabs read only.
- **FR-010**: A faceplate-level Apply MUST deploy a version built from the
  currently deployed version with only that user's value changes, and MUST
  write those values into the draft without disturbing other draft edits.
- **FR-011**: A change script MUST run in the browser when a control value
  changes and MUST be able to read and set any control value on any faceplate
  in the flow. Changes it makes MUST show on their tabs before Apply. Script
  errors MUST be shown on the tab without discarding the user's edit.
  Repeated script-triggered changes MUST stop after a bounded number of
  rounds.
- **FR-012**: A template MUST run isolated from the page, with no network
  access and no access to the parent page, session, or other templates. It
  MUST receive its control's value and definition, the theme, the bound
  entity's live reading, and the other controls' values, be told when they
  change, and be able to set its own control's value.
- **FR-013**: A template that fails MUST fall back to the built-in editor for
  its value type without affecting other controls.
- **FR-014**: Every Apply MUST write an event log entry naming the user, the
  flow, the new version, and the controls whose values changed.
- **FR-015**: A parent flow MUST be able to override values of Faceplate
  nodes inside each shared subflow it calls. Overrides MUST be stored with
  the parent, show as the subflow's tabs under the subflow node's label, and
  fall back to the subflow's own values when unset or when they no longer
  fit the subflow's deployed controls.
- **FR-016**: MCP MUST offer tools to read a flow's faceplates and values,
  set values, and Apply, under the same permissions as the browser. Building
  and editing faceplates MUST use the existing graph tools.
- **FR-017**: Export, import, restoration, and backup MUST carry faceplate
  definitions, values, templates, scripts, tab order, and subflow overrides.

### Key entities

- **Faceplate**: a node that is one tab. It has a name, ordered controls, and
  an optional change script.
- **Control**: a typed setting on a faceplate with a label, default, current
  value, optional constraints, optional entity binding, and optional
  template. It feeds the flow through one value output.
- **Tab order**: the flow's ordered list of faceplate tabs, versioned with
  the flow.
- **Subflow override**: a parent's value for a control on a Faceplate node
  inside a shared subflow it calls.
- **Faceplate share level**: the flow share level, from feature 043, that
  reads faceplates, sets values, and applies without access to the graph.

## Success criteria

- **SC-001**: A user opens the TV flow, changes the maximum brightness on the
  Simple tab, and applies, in under 30 seconds and without opening the graph.
- **SC-002**: After Apply, the next run uses the new value and the version
  history shows one new version.
- **SC-003**: A user holding only a faceplate share can change and apply a
  value, and every graph route refuses that user.
- **SC-004**: A change script setting a control on another tab shows the
  change on that tab in under a second, with no console error.
- **SC-005**: A template that tries a network request is blocked, and the
  other controls on the tab keep working.
- **SC-006**: Two parents of one shared subflow run with different values for
  the same control.
- **SC-007**: `make test` and `make dev` pass and the development service
  stays active.

## Assumptions and scope

- Values change only through save and deploy. Faceplates add no runtime
  settings store and no live value change without a new version.
- A Faceplate node has no message input or output. A flow that must react at
  once to a new value relies on the redeploy rearming its triggers.
- Change scripts and templates are authored by users at edit level or above
  on the flow. They run in the viewer's browser and are not part of the
  script node sandbox.
- Curve and color editors ship as bundled templates written against the same
  template interface authors use.
- Placing a faceplate tab on a dashboard is out of scope, but nothing here
  should prevent it later.
- Readouts are limited to entity bindings on controls. Other live values,
  such as a node's last output, are out of scope.

## References

- [Flow graphs and deployment](../002-flow-authoring/spec.md)
- [Flow sharing](../043-flow-sharing/spec.md)
- [Flow execution](../003-flow-execution/spec.md)
- [Server security and permissions](../001-server-security/spec.md)
- [MCP and editor assistant](../012-mcp-editor-assistant/spec.md)
- [Event log](../037-event-log/spec.md)
- [Backup and restore](../036-backup-restore/spec.md)
- [Flow guide](../../docs/flow-authoring.md)
