# Feature specification: Desktop interaction and vision

**Feature**: `specs/014-desktop-interaction-vision`
**Created**: 2026-10-06
**Status**: Implemented and verified
**Input**: Add drag and drop, window placement, and bounded percentage-based image matching to the library, CLI, and agents.

## User scenarios and testing

### User story 1: Drag a desktop target (Priority: P1)

An author moves a draggable item between coordinates with one operation.

**Why this priority**: Separate hold/move/release steps can leave a button held or interleave with another caller.
**Independent test**: Drag a disposable widget and observe its final state; cancel a drag and verify later pointer input is released.

**Acceptance scenarios**:

1. **Given** valid start/end coordinates, **When** Drag executes, **Then** it moves to the start, presses the selected button, traverses a timed path, and releases.
2. **Given** cancellation or a move failure after pressing, **When** execution ends, **Then** it attempts release with a separate bounded cleanup context and reports cleanup failures.
3. **Given** shared input clients, **When** a drag runs, **Then** other input calls on the composed session cannot interleave with that operation.

### User story 2: Place an application window (Priority: P1)

An author restores, moves, resizes, or sets bounds for a uniquely selected window.

**Why this priority**: Repeatable desktop layouts make coordinate workflows usable.
**Independent test**: Place a disposable window, re-list actual bounds, and restore its initial geometry.

**Acceptance scenarios**:

1. **Given** a supported window backend, **When** placement executes, **Then** it submits the requested restore/move/resize and returns freshly observed window metadata.
2. **Given** a tiled window or unsupported compositor, **When** placement needs unavailable geometry control, **Then** it returns an explicit error without silently converting the window to floating.
3. **Given** duplicate selectors or invalid dimensions, **When** placement executes, **Then** it fails before acting.

### User story 3: Locate a reference image (Priority: P2)

An author finds a same-scale PNG/JPEG reference in an input image or fresh capture.

**Why this priority**: Icon-only custom controls need a visual locator.
**Independent test**: Locate a cropped fixture twice in a larger image; verify distinct matches, scores, nonzero image origins, no-match, and bounded-work errors.

**Acceptance scenarios**:

1. **Given** an opaque reference at the same scale, **When** matching succeeds, **Then** it returns distinct image-pixel rectangles and match percentages, merging overlapping detections.
2. **Given** no match, **When** a complete scan finishes, **Then** it returns an empty match list.
3. **Given** comparison or result limits, **When** matching reaches them, **Then** work exhaustion is an error and an early result cap reports truncation.

### Edge cases

- Negative desktop origins, application minimum sizes, decorations, and compositor ownership affect placement.
- Lost action replies are uncertain outcomes and are never replayed automatically.
- Pixel coordinates do not imply desktop coordinates on Retina or scaled Wayland outputs.
- Image observations preserve normal capture permissions and can contain sensitive pixels.
- Template matching is sensitive to display scale, theme, and rendering changes.

## Requirements

- **FR-001**: Library Desktop, CLI, and agent tools MUST expose one bounded drag operation with configurable button and duration, absolute or selected-window-relative node coordinates, and release cleanup.
- **FR-002**: Composed sessions and helpers MUST serialize a drag with keyboard, mouse, control/menu actions, and window actions sharing that session; drag MUST NOT replay a lost reply.
- **FR-003**: Placement MUST select one window, accept restore/move/resize/bounds requests, validate before mutation, and return fresh observed bounds rather than promising that window-manager requests were honored exactly.
- **FR-004**: Native Windows, macOS, X11, and KWin placement MUST use their existing desktop backends. Hyprland/Sway placement MUST require floating windows. Generic foreign-toplevel Wayland MUST support restore but reject arbitrary placement.
- **FR-006**: Image matching MUST be dependency-free, use bounded same-scale opaque references and deterministic RGB similarity, check cancellation, suppress overlapping detections, and distinguish an incomplete search from no-match.
- **FR-007**: Agent Image Match MUST select conditional binary image value inputs or capture fresh pixels; Image Match MUST accept a binary reference or host-local reference path. Results MUST identify image-pixel coordinate space and dimensions.
- **FR-008**: New mutating nodes MUST disable retries. Nodes MUST expose normal error ports; unavailable providers and dependencies MUST fail explicitly.
- **FR-009**: Existing identities, databases, credentials, portal tokens, signing identities, and desktop grants MUST remain intact.

## Key entities

- Drag request: start/end desktop points, button, duration.
- Placement request: window identity, restore flag, optional position/size.
- Visual observation: image dimensions, source window when known, image-pixel bounds.
- Image match: bounds, match percentage, truncation.

## Success criteria

- **SC-001**: Disposable drags and placement checks pass on applicable desktop hosts, with unsupported paths recorded.
- **SC-002**: Matching fixtures pass relevant Makefile checks and live image matching locates a cropped reference.
- **SC-003**: Agent catalog, MCP executions, and editor fields/ports expose the new tools and their actual defaults.
- **SC-004**: Updated builds reconnect on all seven active enrolled agents without state regeneration.

## Assumptions and scope

OCR is outside this feature. Matching adds no neural model, cloud service, automatic scaling, rotation, or automatic clicking. Results are observations; callers choose actions. Window-manager constraints and input already delivered cannot be undone by cancellation.

## References

- [Agent guide](../../docs/agent-nodes.md)
- [Desktop input](../../automation/specs/002-desktop-input/spec.md)
- [Capture and inspection](../../automation/specs/003-capture-and-inspection/spec.md)
- [Shared transport](../../automation/specs/006-shared-session-transport/spec.md)

## Capture mapping extension

[Feature 020](../020-agent-action-options/spec.md) retains image-pixel rectangles
and adds optional capture mapping and desktop match bounds/centers. Geometry MUST
come from the capture provider and survive helper PNG transport. Binary and
unsupported capture sources MUST report mapping unknown and omit desktop coordinates.
