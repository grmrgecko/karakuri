# Tasks: Desktop interaction and vision

## Setup and foundations

- [x] T001 Record spec, plan, research, data model, contracts, and validation guide under specs/014-desktop-interaction-vision.
- [x] T002 Add Desktop request contracts and helper serialization in automation/desktop.go and automation/desktop_helper.go.

## User story 1: Drag a desktop target

- [x] T003 [US1] Add composed-session input gating and bounded drag cleanup in automation/input.go; wire automation/automation.go and automation/helper_serve.go.
- [x] T004 [US1] Extend real helper transport coverage in automation/desktop_test.go and automation/input_test.go for one drag operation and cancellation.
- [x] T005 [US1] Add CLI drag and agent Drag and Drop configuration in cmd/karakuri-auto/interaction.go and agent/gui/interaction.go.

## User story 2: Place an application window

- [x] T006 [US2] Add placement methods in automation/internal/windows, macos, linuxdesk, kwin, wlwindow, and inspectunix; forward metadataWindow placement.
- [x] T007 [US2] Add CLI placement/restore and agent Window Placement in cmd/karakuri-auto/interaction.go and agent/gui/interaction.go.

## User story 3: Locate a reference image

- [x] T011 [US3] Add scoring/overlap/nonzero-origin/work-budget fixtures in automation/vision_test.go.
- [x] T012 [US3] Add bounded pure-Go matching in automation/image_match.go and CLI/agent consumers in cmd/karakuri-auto/vision.go and agent/gui/vision.go.

## Verification and delivery

- [x] T013 Update docs/agent-nodes.md, automation/README.md, indexed root/module specs and plans, and relevant transport contracts.
- [x] T014 Run required Makefile builds/tests, platform vets/builds, MCP executions, and editor fields/ports checks; record results in specs/014-desktop-interaction-vision/quickstart.md.
- [x] T015 Verify live disposable drag, placement, image-match operations on applicable hosts; preserve identity/grants and close test windows.
- [x] T016 Review and commit verified changes, deploy seven agents and canonical CLIs, verify versions/reconnections, and close verification in specs/014-desktop-interaction-vision/quickstart.md.

## Dependencies and implementation strategy

T002 precedes action consumers. Drag/input gating precedes placement mutation consumers. Matching uses bounded image loading and image-pixel metadata; its algorithm fixtures precede implementation. CLI and nodes use the same public functions. Verification and docs follow the final contracts. Work stays on master; no publishing or remote is involved. Independent fixture capture and platform-source research can proceed alongside local action implementation; no parallel code edits are required.
