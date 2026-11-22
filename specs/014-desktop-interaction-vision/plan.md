# Design plan: Desktop interaction and vision

**Feature**: `specs/014-desktop-interaction-vision` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Extend Desktop with serialized drag and placement, and add independent image-analysis functions consumed by CLI and GUI nodes. Keep image analysis independent of capture helpers.

## Technical context

Go, existing pure-Go platform bindings, xgbutil/EWMH, KWin scripting, and compositor command tools. No new persistence or native-linking dependency. Files live under automation, agent/gui, and cmd/karakuri-auto. Image analysis is bounded by decoded pixels, encoded bytes, output bytes, comparisons, results, and caller deadlines.

## Constitution check

New mutations never replay lost replies; input serialization and bounded cleanup cover drag. Nodes use normal validation/error routing and agent advertisement. Binary ports use existing editor metadata. Desktop account permissions, grants, identities, credentials, and encryption material remain unchanged. Extend the public Desktop interface and document the source-level change. Run Makefile builds/tests and applicable real desktop/editor checks before commits. Remain on master.

## Design and contracts

Compose a context-aware input gate around local keyboard, mouse, window actions, and Desktop mutations. Drag executes against the underlying mouse under that gate; cleanup uses a two-second context independent of cancellation. Send drag and placement in one helper RPC with callOnce. Shared residents serialize those RPCs across clients. The ordinary helper's bounded drag may finish after caller cancellation; document that transport limit.

Add an optional placement method to native window providers and forward it through metadataWindow. Desktop validates placement flags/bounds and delegates to that method. Windows uses SetWindowPos; macOS sets AXPosition/AXSize; X11 uses EWMH requests; KWin sets frameGeometry. Hyprland and Sway use exact compositor IDs and require floating state. Foreign-toplevel only restores. Placement returns a newly listed window; requested and observed geometry remain separate.

Matching scans an opaque reference at one scale with RGB absolute-difference percentage agreement, early rejection, an explicit comparison budget, and overlap merging. Result-limit termination reports truncation; work exhaustion returns an error. Images are normalized to image-pixel coordinates, independent of their Go Bounds origin.

Nodes select conditional image/reference binary value ports. Selected sockets require value edges; standalone MCP tests accept literal base64 configuration for permanent port fields. Without image input, use existing fresh screen/window/region capture, with shared window selectors. Reference paths accept normal message interpolation; image bytes never enter template interpolation. Return dimensions and provider-authored optional desktop mapping; never infer it
from a requested crop or window geometry. Feature 020 adds mapped bounds/centers.

## State and data model

All requests/results are transient. Host-local reference files are caller-owned. No migrations or identity regeneration. See [data-model.md](data-model.md) and [contracts/tools.md](contracts/tools.md).

## Verification

Small tests cover helper RPC contracts, matching scoring/overlap/resource boundaries. Live disposable fixtures observe drag and window placement, template matches. Use MCP for every advertised node and binary inputs; exercise config and ports in the shared preview. Build/test through Makefile, preserve signing identities, refresh installed executables, and record results in quickstart.md. Restart karakuri-dev and verify HTTP 200.

## Risks and open decisions

Generic Wayland placement is unavailable; tiling windows require the author to float them separately. Image matching requires matching scale/theme and can reject expensive searches. Pixel observations can contain sensitive content. No unresolved design decisions remain after [research.md](research.md).
