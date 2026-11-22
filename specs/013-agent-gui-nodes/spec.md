# Feature specification: Agent GUI nodes

**Feature**: `specs/013-agent-gui-nodes`
**Created**: 2026-10-06
**Status**: Implemented
**Input**: Bring the automation command's inspection, interaction, and synchronization into agent flows.

## User scenarios and testing

### User story 1: Observe and target an application (Priority: P1)

An author inspects an application, clicks a named control, and captures the selected window after the action.

**Why this priority**: Coordinates and stale observations make unattended flows unreliable.
**Independent test**: Inspect a disposable application, click a unique control, and capture the changed state.

**Acceptance scenarios**:

1. **Given** an accessible window, **When** UI Snapshot executes, **Then** it returns fresh window metadata, elements, backend names, and capture time, suppressing protected values.
2. **Given** duplicate labels or window matches, **When** an action selects that target, **Then** it fails without acting on an arbitrary match.
3. **Given** a selected window on another monitor, **When** Screenshot executes, **Then** it captures that window using native capture where available and desktop coordinates for fallback.
4. **Given** a prior screenshot in the same run, **When** a fresh capture executes after a click, **Then** it reads the current screen again.

### User story 2: Synchronize with application state (Priority: P1)

An author waits for a window, accessible state, or stable pixels before proceeding.

**Why this priority**: Fixed sleeps cannot establish application readiness.
**Independent test**: Wait for a disposable window and a known label, then force a timeout and inspect evidence.

**Acceptance scenarios**:

1. **Given** an appearing or disappearing window, **When** its wait condition is met, **Then** execution continues with the observed window result.
2. **Given** a selected window, **When** a state wait runs, **Then** it pins that window identity and checks text, role, enabled state, disappearance, or stable pixels.
3. **Given** an inaccessible provider, **When** waiting for absence, **Then** failed inspection never counts as absence.
4. **Given** a state failure, **When** evidence capture is enabled, **Then** the error identifies the screenshot/snapshot directory or capture failure within a bounded deadline.

### User story 3: Use menus, dialogs, and displays (Priority: P2)

An author lists and invokes menus, discovers application dialogs, and reads or changes display modes.

**Why this priority**: These existing desktop operations currently require command invocation.
**Independent test**: Read a disposable application's menu and dialog, invoke a harmless menu item, and restore a temporary display mode after testing.

**Acceptance scenarios**:

1. **Given** native or accessible menus, **When** listed, **Then** IDs, paths, and available state are returned; disabled or ambiguous invocations fail.
2. **Given** application dialogs, **When** Dialog List runs, **Then** it returns their window metadata and controls with inspection errors distinguished from an empty result.
3. **Given** an available display backend, **When** Display List runs, **Then** it returns reported output geometry and modes; Set Display Mode changes only the primary output temporarily or reports rejection.

### User story 4: Run automation command text (Priority: P3)

An author runs a karakuri-auto command file or literal inline commands through an agent action.

**Why this priority**: Existing desktop sequences should remain usable during conversion to individual nodes.
**Independent test**: Run a disposable command file with a successful line followed by a failing line and verify that later lines do not execute.

**Acceptance scenarios**:

1. **Given** an installed automation executable and command file, **When** the node executes, **Then** it preserves command-file quoting, ordered execution, stop-on-error behavior, and bounded process output.
2. **Given** cancellation, **When** the command-file node is stopped, **Then** it uses the agent's existing process-tree cancellation behavior.

### Edge cases

- Accessibility and global geometry depend on provider support; no OCR is implied.
- Pixel stability does not establish application completion.
- Protected accessibility text does not redact screenshot pixels.
- Elements can become stale; target discovery runs for each action.
- Lost replies to new desktop actions must not cause automatic replay.
- A screenshot or inspection requested through a privileged agent must run in the desktop session.

## Requirements

### Functional requirements

- **FR-001**: Advertise UI Snapshot, Click Control, Wait for Window, Wait for UI State, Menu List, Menu Invoke, Display List, Set Display Mode, Dialog List, and Automation Script nodes.
- **FR-002**: Snapshot and execution-triggered observations MUST read fresh state and preserve unknown window enabled state and geometry availability.
- **FR-003**: Click Control MUST accept a label or element ID, reject ambiguity, and respect enabled/visible state.
- **FR-004**: Window actions and window-relative mouse targeting MUST reject ambiguous patterns; exact IDs take precedence.
- **FR-005**: Screenshot MUST support exact or uniquely matched windows, native window capture, and desktop-coordinate fallback. Existing pure value sources MUST remain usable, with explicit fresh execution variants.
- **FR-006**: Wait nodes MUST poll within their timeout, honor cancellation, and return failure through normal flow error routing. State waits MUST pin window identity and reject ambiguous elements.
- **FR-007**: Failed state waits MUST optionally save bounded screenshot and unfiltered snapshot evidence without replacing the original error or exceeding the node wrapper's grace period.
- **FR-008**: Menu invocation MUST reject ambiguous, disabled, or non-invokable targets; menu and dialog observations MUST preserve provider limits.
- **FR-009**: Display operations MUST report available geometry and temporary primary-output mode changes or backend rejection.
- **FR-010**: New side-effecting nodes MUST disable automatic retries. Desktop actions MUST execute in the selected desktop session, including service agents and shared residents.
- **FR-011**: Automation Script MUST run an explicitly configured automation executable and existing or temporary inline command file using the agent's command output limits and cancellation contract.
- **FR-012**: Existing CLI syntax MUST remain accepted; shared operations MUST serve both CLI and agent consumers.
- **FR-013**: Shared agent window selectors MUST expose regex, contains, and exact matching with separate title/class, PID, and executable filters. Unconfigured fields MUST preserve existing matching behavior.
- **FR-014**: Keyboard MUST optionally select and activate a window and verify focus before input; input without a target MUST preserve current-focus behavior.
- **FR-015**: Wait nodes MUST support fractional seconds, single-check mode, and an optional normal unmatched timeout result. UI state MUST support either, enabled, or disabled state. Only unmet conditions may produce a normal unmatched result; ambiguity, provider failure, and cancellation remain errors.
- **FR-016**: UI Snapshot MUST offer accessibility, native controls, or both, role/text filters, and an optional result limit with truncation reported per source.
- **FR-017**: Dialog List MUST offer optional window scope and omission of control details, applying scope before provider inspection.
- **FR-018**: Screenshot MUST expose display-relative or desktop coordinates for region captures.
- **FR-019**: Click Control MUST expose exact-first, exact, and contains label matching and optional role/immediate-parent constraints without weakening ambiguity or protected-text handling.
- **FR-020**: Automation Script MUST accept either an existing file or inline commands. Inline source MUST be written to a private temporary file, executed without message interpolation of its contents, and cleaned up on success or failure.

### Key entities

- Window target: exact ID or unique pattern, with metadata from the current desktop.
- Snapshot: timestamp, window, backend names, elements, and inspection failure.
- Wait condition: timeout, target, element predicates or stable region, and evidence preference.
- Menu, dialog, and display observations: provider-owned IDs and reported state.
- Evidence directory: host-local screenshots and structured snapshots, retained until removed by the operator.

## Success criteria

- **SC-001**: A flow can inspect, click, wait, and recapture a disposable application without fixed coordinates.
- **SC-002**: Duplicate targets fail without an action; an inaccessible provider cannot satisfy an absence wait.
- **SC-003**: Agent and CLI execute shared native operations through the desktop helper on supported platforms.
- **SC-004**: Existing value-source flows retain their ports; new execution observations refresh within one run.
- **SC-005**: All advertised additions pass relevant builds, tests, catalog checks, and applicable live desktop checks.

## Assumptions and scope

No database, identity, credential, or encryption migration is required. Existing GUI pattern fields default to Go regular expressions and reject multiple matches; explicit modes select literal matching. The CLI retains its substring selectors. Automation Script requires an installed executable; file and inline modes share that interpreter. This feature adds no OCR, persistent desktop watcher, or arbitrary UI value setter.

## Configuration improvement acceptance scenarios

1. Similar application windows are narrowed by title, class, PID, or executable; multiple matches still fail before input or actions.
2. Targeted Keyboard activates when requested and observes that window's focus before sending input; activation failure sends no input.
3. A single check or normal timeout result returns `matched: false` for an unmet condition. Provider failures, ambiguity, and parent cancellation still take the error route. Disabled controls and fractional seconds are accepted.
4. Snapshot source, role/text filters, and result limits return protected-text-filtered observations with explicit truncation. Dialog scope excludes unrelated windows before inspection.
5. Desktop-coordinate screenshot regions pass through to the module capture contract. Control role/parent filters distinguish duplicate labels without arbitrary selection.
6. Inline automation commands preserve literal source in a private temporary file that is removed after execution, including failure and cancellation.

## Related features

- [Agent guide](../../docs/agent-nodes.md)
- [Remote agents](../009-remote-agents/spec.md)
- [Desktop CLI](../010-desktop-automation/spec.md)
- [Capture and inspection](../../automation/specs/003-capture-and-inspection/spec.md)

## Interaction and reference images

[Feature 014](../014-desktop-interaction-vision/spec.md) extends the catalog with
Drag and drop, Window placement, and Image match. The original inspection and
wait contracts remain unchanged. Matching is percentage-based and includes no OCR.
