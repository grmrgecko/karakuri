# Design plan: Agent GUI nodes

**Feature**: `specs/013-agent-gui-nodes` | **Date**: 2026-10-06 | **Spec**: [spec.md](spec.md)

## Summary

Compose native desktop operations in the automation module, share snapshots and polling between CLI and agent, and expose explicit execution nodes for fresh observations.

## Technical context

Go with the repository's existing automation, nodeapi, Win32, X11, and accessibility dependencies. Native operations live in `automation/desktop*.go`; polling and snapshot records live in `automation/ui*.go`. CLI files retain argument parsing and rendering. Agent nodes live in `agent/gui` and `agent/exec`. Existing pure screenshot/window sources remain unchanged in their port contracts; fresh variants accept execution input. No database changes are needed.

## Constitution check

Normal flow error ports carry failures. Mutating nodes and new native helper calls do not replay lost replies. Polling honors context and fractional timeout seconds; agent evidence has at most eight seconds under the wrapper's ten-second grace. Existing privileged desktop delegation remains the OS permission boundary. Catalog metadata uses the normal agent advertisement path. Identity, CA material, credentials, encryption keys, and stored flow versions are preserved. Relevant Makefile targets and live checks are required before commit.

## Design and contracts

Add a composed Desktop capability for native controls, menus, label clicks, dialog discovery, and native window capture. Wire local, delegated, and resident sessions through the existing authenticated helper protocol. Keep snapshot selection and polling in the caller so helper requests remain bounded operations. New helper mutations use a single attempt.

Keep existing agent regex matching but reject ambiguity. CLI substring matching remains supported. UI Snapshot, Menu List, Display List, Dialog List, Screenshot Now, and Window List Now are execution nodes. Wait for UI State returns the observed pinned window and matched result; errors include evidence paths. Agent evidence uses the parent execution context and a total eight-second deadline. The value-source Screenshot and Window List cache their result within a run; the Now variants exist for fresh observations after actions.

Automation Script adapts the existing Command node to an explicitly configured `karakuri-auto` executable and host-local command file. It preserves process output limits, cancellation, CLI script semantics, and session installation requirements. It does not spawn the agent as a CLI interpreter.

## State and data model

See [data-model.md](data-model.md) and [node contracts](contracts/nodes.md). Observations and targets are transient. Evidence is host-local retained data. No persistence migration occurs. Ambiguous regex actions now fail rather than selecting the first matching window.

## Verification

Run agent and automation builds/tests, platform cross builds, and server build/tests for embedded guide updates. Exercise native inspection, menus, labels, waits, screenshots, dialogs, and temporary display rejection/restoration on reachable desktop hosts. Use MCP for node advertisement and execution; inspect the editor for new config/port behavior. Save disposable evidence outside tracked source; remove test windows and disclose any added development flows. Restart and verify the development service.

## Risks and open decisions

Provider support and grants still govern desktop operations. Pixels may expose secrets. Element IDs can become stale; discovery repeats per invocation. Native PrintWindow can outlive its two-second caller deadline. Automation Script requires a separately installed executable and follows its resident/session setup. No unresolved design questions remain.

## Configuration improvements

Extend WindowTarget with a match mode and independent title, class, PID, and
executable filters. Share node field definitions and decoding in agent/gui.
ID precedence remains unchanged. Keyboard optionally activates its unique target
and polls focus for a bounded interval before sending input.

Share single-observation window/state checks with polling. An unmet condition
uses a dedicated sentinel; provider errors retain their original identity.
Normal timeout results are permitted only when the local wait expires with no
provider error and the parent is still active. Single checks cannot request
pixel stability because elapsed stability cannot be established in one read.
Retain the existing enabled boolean and add an explicit state select whose value
wins when configured. Evidence remains bounded and applies to error outcomes.

Snapshots add native controls through the existing Desktop capability. Filter
both sources without exposing protected text, and report each source's truncation.
Dialog options travel through helper RPC and scope windows before inspection.
Control constraints travel in ControlTarget; filtered actions re-enumerate and
act on the unique provider ID, preserving native Windows button handling. Legacy native label clicks keep their behavior.

Inline automation commands use os.CreateTemp, close before execution for Windows,
and remove on every exit. Literal contents never enter the message template
interpreter. Existing file mode remains the default. No stored state migration,
identity change, or credential regeneration is required.

## Interaction and reference images

Root feature 014 adds the interaction and image-match consumers to this GUI group.
The registered agent schemas define conditional binary value sockets and shared
capture/window selectors. Both mutating nodes disable automatic retry.
