# Design plan: Capture, windows, and inspection

**Feature**: `automation/specs/003-capture-and-inspection` | **Date**: 2026-10-04
**Spec**: [spec.md](spec.md) | **Status**: Implemented baseline

## Summary

Return provider-backed images and structured desktop observations. Keep unknown
state and geometry explicit, suppress protected text, and retain provider IDs.

## Technical context and design

`autoapi.Screen` returns `image.Image` through the platform capture backend.
`CaptureRequest` carries display, region, and desktop-coordinate selection.
Platform `newInspection` composition and window enrichment add native/provider
metadata. `autoapi.Inspection` enumerates elements/displays, invokes provider
actions, and requests temporary primary-output modes.

Unix inspection uses its available compositor and accessibility providers.
macOS uses Accessibility and display APIs; Windows uses its native providers.
Enumeration is bounded within those implementations. Public DTOs preserve
provider IDs, roles, parent relationships, geometry, enabled/visible/protected
state, and exposed action names. No OCR pipeline is implied.

## State and data model

Images and enumeration results are per-call observations. Element IDs belong
to the provider and can become stale after UI reconstruction. Window enabled
state can be unknown. Geometry can be absent or provider-relative, especially
on Wayland. Display modes target the primary output and remain subject to
driver/compositor acceptance.

## Contracts and failure handling

Inspection suppresses protected text and preserves the protected flag; screenshots
can still show sensitive pixels. Capture, accessibility, and window actions have
separate grants and backend support. macOS requires the executable's authorized
identity and relevant privacy grants. Helper transport sends captured images as
PNG under [005/006](../README.md). PipeWire screenshot capture is not implemented.

Library observations do not perform CLI substring matching, unique-element state
selection, snapshot filtering, stable-pixel waits, or evidence collection. Those
consumer behaviors remain in root feature 010.

## Constitution check

- Preserve protected-value handling and report provider limits without inventing geometry.
- This split changes no observation DTO, transport encoding, or OS grant.
- Use the Makefile and documented desktop hosts for changed provider behavior.

## Verification

Run `make dev TARGET=auto` and `make test TARGET=automation` from the repository
root. Unix provider-decoding fixtures and helper image fixtures check data and
transport. On changed backends, enumerate a disposable window, inspect protected
values, exercise exposed actions, capture supported regions, and verify rejected
display changes. Follow `AGENTS.md` for canonical binaries and macOS signing.
The baseline records source behavior, not a completed cross-platform live check.

## Risks and limits

Accessible elements are narrower than everything visible on screen. Provider
geometry may not support global cropping. Temporary display changes can disrupt
the current desktop and must be verified on an authorized host.

## References

- [Public observation contracts](../../autoapi/inspection.go)
- [Window enrichment](../../window_metadata.go)
- [Unix provider fixtures](../../internal/inspectunix/protocol_test.go)
- [Capture transport fixtures](../../helper_test.go)
- [CLI capture and state behavior](../../../specs/010-desktop-automation/plan.md)

## Shared desktop operations

Session.Desktop composes native platform controls, menus, label clicks, dialog discovery, and window capture. desktop_helper.go adds RPC proxies; ServeHelper, newDelegated, and NewRemoteSession wire the same capability. ui.go and ui_target.go share caller-side snapshots and polling. ui_evidence.go writes partial evidence within the caller deadline. These utilities now serve both root feature 010 and agent feature 013.

AT-SPI uses `Properties.Get(Action, NActions)` and signed-index `GetName` calls.
It never calls the bulk `GetActions` method. The isolated D-Bus fixture leaves
one name unavailable and checks that later actions keep their provider indices.
Live Rocky 9 GTK inspection, label click, wait, and capture verify the provider
no longer aborts.

Shared window selection accepts exact, contains, or regex matching and independent
title, class, PID, and executable constraints while retaining exact-ID precedence.
Single window/state checks distinguish unmet conditions from provider failure.
State checks support enabled, disabled, or either; snapshots can return filtered
accessibility elements and native controls with per-source output truncation.
DialogOptions scopes discovery before inspection and can omit controls.
ControlTarget carries role, immediate-parent, and label-match constraints through
helper calls. Desktop.Dialogs now takes DialogOptions; callers use an empty value
for the previous unscoped behavior. This library interface change requires caller
updates; no enrolled agent, database, or credential format changes.

## Placement and reference matching

An optional native Place method avoids changing autoapi.Window; metadata and
Unix inspection wrappers forward it for native IDs. Desktop validates and holds
the composed input gate. Placement requests use SetWindowPos, AX attributes,
EWMH, KWin scripting, or existing-floating Hyprland/Sway compositor IDs. Window
manager responses remain separate from observed bounds. Generic foreign-toplevel
only restores; AT-SPI IDs cannot request placement.

vision.go checks encoded size and dimensions before PNG/JPEG decoding.
image_match.go normalizes image origins, compares RGB at one scale, rejects
transparent references, and bounds comparisons and distinct results. Percentage
is mean RGB agreement; source alpha is ignored. Overlap uses IoU above 0.3.
Fixtures cover scores, repeats, image origins, truncation, and work exhaustion.
Consumer configuration and live checks are in root feature 014.

## Capture geometry

Feature 020 adds optional autoapi.CaptureMapping on returned image values without
changing Screen or Window interfaces. X11 and Windows use actual physical capture
bounds; macOS derives logical display-point scale from actual image dimensions.
Portal and unmapped native window sources omit it. Screen and window helper replies
carry PNG plus optional mapping. Images retain their pixels and match coordinates
stay image-relative; MapImageMatches adds desktop bounds and centers when known.
The internal window helper reply changes from PNG bytes to the same capture envelope
as screen capture; helpers re-execute the composing binary, so both peers upgrade
together. There is no persistent format or privacy-grant change.

Window capture tries the window's own pixels first: Windows `PrintWindow` with
`PW_RENDERFULLCONTENT` on 8.1 and later (two-second caller limit), the X11
XComposite backing pixmap, and macOS `screencapture -l` with the CoreGraphics
window ID. Each falls back to cropping the visible desktop rectangle; macOS
desktop-coordinate regions also use `screencapture`. Windows `controls` reads
text with `WM_GETTEXT` under a 300 ms message timeout, and label clicks send
`BM_CLICK` to standard buttons. Windows `elements` runs the system PowerShell
with the .NET UI Automation assemblies, stays PowerShell 2 compatible, and writes
JSON straight to stdout to avoid console line wrapping. Display management uses
CoreGraphics, XRandR, and Hyprland, Sway, Mutter (D-Bus), KScreen, or
wlr-output-management on Wayland.
