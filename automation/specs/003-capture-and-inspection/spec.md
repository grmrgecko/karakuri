# Feature specification: Capture, windows, and inspection

**Feature**: `automation/specs/003-capture-and-inspection`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Observe desktop pixels, windows, accessible elements, and displays while
retaining provider identity, protected-value handling, and geometry limits.

## User scenarios and testing

### User story 1: Capture and identify desktop targets (Priority: P1)

A caller lists windows and captures a supported display or region without
assuming that every provider exposes usable global window geometry.

**Why this priority**: Capture and window identity guide later desktop actions.
**Independent test**: List a disposable window and capture a supported display/region
on the selected backend.

**Acceptance scenarios**:

1. **Given** a supported capture request, **When** capture succeeds, **Then** it returns
   an image for the backend's supported display/region targeting.
2. **Given** a provider with unknown enabled state or incomplete geometry, **When**
   windows are listed, **Then** those limitations remain visible in the returned data.
3. **Given** a listed window and supported window action, **When** the action is
   requested, **Then** the selected provider handles it or reports its failure.

### User story 2: Inspect accessible elements (Priority: P1)

A caller uses provider element IDs, roles, and exposed actions while respecting
protected values and enumeration limits.

**Why this priority**: Accessibility provides structured observations without OCR.
**Independent test**: Enumerate a disposable application's accessible controls and
invoke an exposed provider action; inspect protected text handling.

**Acceptance scenarios**:

1. **Given** a protected accessible value, **When** elements are enumerated, **Then**
   its text is suppressed and the protected state remains in the result.
2. **Given** an accessible element, **When** its metadata is returned, **Then** its
   ID and role retain the provider's spelling and available actions are exposed.

3. **Given** an AT-SPI provider whose bulk action query is broken, **When** actions
   are inspected, **Then** bounded individual name reads preserve native indices
   without invoking that bulk query. Unreadable slots do not renumber later actions.

### User story 3: Inspect and change a display mode (Priority: P2)

A caller reads display information and requests a temporary primary-output mode.

**Why this priority**: Display changes must respect compositor and driver ownership.
**Independent test**: Enumerate displays and, on an authorized host, exercise a
temporary primary mode or the backend's rejection.

**Acceptance scenarios**:

1. **Given** a supported display provider, **When** displays are enumerated, **Then**
   their provider identity, geometry, and primary state are returned.
2. **Given** a rejected or unsupported mode, **When** a change is requested, **Then**
   the caller receives an error rather than a claim that the mode changed.

### Edge cases

- Element IDs can change when an application rebuilds its UI; callers must re-enumerate.
- Wayland geometry can be unavailable or provider-relative, so accessibility does not
  establish global screenshot crop coordinates.
- Protected accessibility text does not redact screen pixels.
- Provider support and OS grants can deny capture, inspection, or window actions.

## Requirements

### Functional requirements

- **FR-001**: Screen capture MUST return an image for supported `CaptureRequest`
  display/region targeting and its desktop-coordinate option.
- **FR-002**: Window listing MUST retain provider identity, title/class/PID, active state,
  geometry, capture identity, and unknown enabled state where applicable.
- **FR-003**: Window actions MUST use the selected provider's window contract and report
  unsupported actions or execution failures.
- **FR-004**: Inspection MUST expose provider element IDs, parent IDs, roles, state,
  geometry, text, and available actions without translating them into OCR results.
- **FR-005**: Accessibility enumeration MUST be bounded and suppress protected text
  while retaining the protected flag supplied by the provider.
- **FR-006**: Display mode changes MUST target the primary output temporarily and
  report compositor/driver rejection or lack of support.
- **FR-007**: Display enumeration MUST retain provider IDs, geometry, mode information,
  and primary-output state subject to available backend information.

### Key entities

- Capture request and image: backend-supported display/region targeting and pixels.
- Window: provider identity, actions, optional enabled state, and geometry.
- Element: accessibility identity, hierarchy, state, protected text, and actions.
- Display and mode: provider geometry and a requested primary-output configuration.

## Success criteria

- **SC-001**: Successful capture returns the image's pixels and dimensions; unsupported
  capture paths return errors.
- **SC-002**: Protected-value fixtures expose no protected text, and decoded element
  metadata retains provider IDs and roles.
- **SC-003**: Unknown geometry/state remains distinguishable from known observations;
  denied display mode changes report failure.

## Assumptions and scope

CLI window matching, snapshot wrappers, state waits, crop selection, and failure
evidence are defined in [root feature 010](../../../specs/010-desktop-automation/spec.md).
The library provides shared snapshot, target selection, state polling, and failure
evidence operations for CLI and agent consumers. CLI syntax remains in feature 010;
agent configuration is defined in root feature 013.

## References

- [Design plan](plan.md)
- [Module guide](../../README.md)
- [Capture/window data](../../autoapi/types.go)
- [Inspection data](../../autoapi/inspection.go)
- [Inspection composition](../../inspection.go)
- [Unix inspection](../../internal/inspectunix)
- [Mac inspection](../../internal/macos/inspection.go)

## Desktop operation extension

- **FR-008**: The composed desktop capability MUST expose native controls, menu trees/invocation, unique label clicks, dialog discovery with partial errors, and native window capture with desktop-coordinate fallback.
- **FR-009**: Shared snapshot and state utilities MUST preserve unknown state, protected values, pinned window identity, cancellation, and failed-inspection versus absence distinctions.

Acceptance: a delegated caller can capture a selected window and invoke a unique native control or menu. GTK alert roles appear in dialog discovery. Ambiguous labels fail without a click. Snapshot-compatible records carry these observations to agent feature 013.

AT-SPI action enumeration uses `NActions` and `GetName` with at most 64 actions per element.
Rocky 9 at-spi2-atk 2.38.0 aborts GTK applications in `GetActions`. Machine-readable
action names and their original indices are retained for invocation.

- **FR-010**: Shared selectors MUST support exact, contains, and regex matching and independent title, class, PID, and executable constraints while retaining ID and active-selection precedence.
- **FR-011**: Single checks and polling MUST distinguish unmet conditions, local wait expiry, provider failures, and parent cancellation. State predicates MUST support enabled, disabled, or either state.
- **FR-012**: Snapshot options MUST select accessibility elements, native controls, or both, filter role/text, suppress protected values, and report per-source output truncation.
- **FR-013**: DialogOptions MUST scope windows before inspection and permit omission of controls. ControlTarget MUST carry role, immediate parent, and exact-first/exact/contains matching through helper transport.

Acceptance: scoped discovery excludes unrelated windows before inspection. Exact
matching treats regex punctuation literally. Disabled controls satisfy an explicit
disabled condition. Joined provider errors never become normal wait expiry.
Native Windows control IDs and parent IDs identify a unique enabled button.

Desktop.Dialogs takes DialogOptions; callers use an empty value for unscoped
behavior. This library interface change requires caller updates, with no enrolled
agent, database, or credential format changes.

## Placement and reference matching

- **FR-014**: Desktop.PlaceWindow MUST validate restore/move/resize requests before mutation and use the native window backend. PlaceAndObserveWindow MUST report requested and freshly observed bounds separately, including whether requested bounds matched.
- **FR-015**: Placement MUST preserve unique target selection, report unavailable compositor control, and require existing floating state for Hyprland/Sway. Generic foreign-toplevel can restore but cannot request arbitrary bounds.
- **FR-016**: DecodeImage/ReadImage MUST accept only bounded PNG/JPEG data. MatchImage MUST locate same-scale opaque references with a positive RGB agreement percentage through 100, image-relative rectangles, overlap merging, cancellation checks, and explicit result/work limits.

Acceptance: native placement can be clamped without claiming requested bounds were
honored. A repeated reference returns distinct match regions. Nonzero Go image
origins normalize to image coordinates. Work exhaustion fails; completed no-match
succeeds; an early result cap reports truncation. Matching performs no automatic
input and has no OCR or model dependency.

## Capture geometry

- **FR-017**: Capture providers MAY attach a validated desktop origin and independent
  positive X/Y desktop-units-per-pixel scales to an image. Helpers MUST preserve
  this optional metadata alongside PNG bytes. Match mapping MUST normalize Go
  image origins and omit desktop bounds/centers when metadata is unavailable.

Acceptance: negative origins and independent scales survive PNG helper round trips
without changing pixels. Binary image decoding alone cannot establish mapping.
