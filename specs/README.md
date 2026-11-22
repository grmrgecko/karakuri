# Karakuri specifications

These specs describe the implemented system at commit `5158025`. Requirements
are written as the product contract; plans record the architecture that fulfills
it. This baseline was established on 2026-10-04 from feature guides, code, and
tests. The initial [source audit](code-audit.md) corrected selected contracts and
separated the standalone automation module from its CLI. The subsequent
[server and agent audit](server-agent-audit.md) reviews all 179 requirements in
the eleven server/agent features, records corrections, and distinguishes source,
test, and live evidence. These documents do not
invent historical design work, completed tasks, performance promises, or live-test
evidence.

## Feature index

| Feature | Specification | Design plan |
| --- | --- | --- |
| 001 Server operation and identity | [Requirements](001-server-security/spec.md) | [Design](001-server-security/plan.md) |
| 002 Flow graphs and deployment | [Requirements](002-flow-authoring/spec.md) | [Design](002-flow-authoring/plan.md) |
| 003 Execution, failures, and persistent state | [Requirements](003-flow-execution/spec.md) | [Design](003-flow-execution/plan.md) |
| 004 Connections, MQTT devices, and controls | [Requirements](004-connections-devices/spec.md) | [Design](004-connections-devices/plan.md) |
| 005 Sandboxed logic and server file access | [Requirements](005-script-sandbox/spec.md) | [Design](005-script-sandbox/plan.md) |
| 006 Dashboard layout and publication | [Requirements](006-dashboard-authoring/spec.md) | [Design](006-dashboard-authoring/plan.md) |
| 007 Dashboard sharing and rendering | [Requirements](007-dashboard-access-rendering/spec.md) | [Design](007-dashboard-access-rendering/plan.md) |
| 008 Plugins and extensible catalogs | [Requirements](008-plugin-system/spec.md) | [Design](008-plugin-system/plan.md) |
| 009 Remote agent trust and host actions | [Requirements](009-remote-agents/spec.md) | [Design](009-remote-agents/plan.md) |
| 010 Desktop automation CLI and local command execution | [Requirements](010-desktop-automation/spec.md) | [Design](010-desktop-automation/plan.md) |
| 011 AI processing, tools, and memory | [Requirements](011-ai-memory/spec.md) | [Design](011-ai-memory/plan.md) |
| 012 MCP authoring and editor chat | [Requirements](012-mcp-editor-assistant/spec.md) | [Design](012-mcp-editor-assistant/plan.md) |
| 013 Agent GUI nodes | [Requirements](013-agent-gui-nodes/spec.md) | [Design](013-agent-gui-nodes/plan.md) |
| 014 Desktop interaction and reference images | [Requirements](014-desktop-interaction-vision/spec.md) | [Design](014-desktop-interaction-vision/plan.md) |
| 016 Server utility nodes | [Requirements](016-server-utility-nodes/spec.md) | [Design](016-server-utility-nodes/plan.md) |
| 017 Isolated SQLite queries | [Requirements](017-isolated-sqlite/spec.md) | [Design](017-isolated-sqlite/plan.md) |
| 018 Configured notification services | [Requirements](018-notification-services/spec.md) | [Design](018-notification-services/plan.md) |
| 019 Network socket nodes | [Requirements](019-network-socket-nodes/spec.md) | [Design](019-network-socket-nodes/plan.md) |
| 020 Agent action options and HTTP requests | [Requirements](020-agent-action-options/spec.md) | [Design](020-agent-action-options/plan.md) |
| 021 Graceful process termination | [Requirements](021-graceful-process-termination/spec.md) | [Design](021-graceful-process-termination/plan.md) |
| 022 Agent browser prompts | [Requirements](022-agent-browser-prompts/spec.md) | [Design](022-agent-browser-prompts/plan.md) |
| 023 Dashboard display control | [Requirements](023-dashboard-display-control/spec.md) | [Design](023-dashboard-display-control/plan.md) |
| 024 Home media and list widgets | [Requirements](024-home-media-widgets/spec.md) | [Design](024-home-media-widgets/plan.md) |
| 025 Dashboard variables | [Requirements](025-dashboard-variables/spec.md) | [Design](025-dashboard-variables/plan.md) |
| 026 Script node SDK | [Requirements](026-script-node-sdk/spec.md) | [Design](026-script-node-sdk/plan.md) |
| 027 Time-series connections and history forwarding | [Requirements](027-timeseries-connections/spec.md) | [Design](027-timeseries-connections/plan.md) |
| 028 Dashboard charts | [Requirements](028-dashboard-charts/spec.md) | [Design](028-dashboard-charts/plan.md) |
| 029 Durable waits and device state | [Requirements](029-durable-waits/spec.md) | [Design](029-durable-waits/plan.md) |
| 030 Calendar connections and event triggers | [Requirements](030-calendar-connections/spec.md) | [Design](030-calendar-connections/plan.md) |
| 031 Location control kind | [Requirements](031-location-control/spec.md) | [Design](031-location-control/plan.md) |
| 032 Zigbee2MQTT discovery and device availability | [Requirements](032-zigbee2mqtt-discovery/spec.md) | [Design](032-zigbee2mqtt-discovery/plan.md) |
| 033 ESPHome native API | [Requirements](033-esphome-native-api/spec.md) | [Design](033-esphome-native-api/plan.md) |
| 034 Frigate NVR integration | [Requirements](034-frigate-nvr/spec.md) | [Design](034-frigate-nvr/plan.md) |
| 035 Energy tracking | [Requirements](035-energy-tracking/spec.md) | [Design](035-energy-tracking/plan.md) |
| 036 Backup and restore | [Requirements](036-backup-restore/spec.md) | [Design](036-backup-restore/plan.md) |
| 037 Event log | [Requirements](037-event-log/spec.md) | [Design](037-event-log/plan.md) |
| 038 Media control kind | [Requirements](038-media-control-kind/spec.md) | [Design](038-media-control-kind/plan.md) |
| 039 Home Assistant areas and devices | [Requirements](039-ha-registries/spec.md) | [Design](039-ha-registries/plan.md) |
| 040 openHAB connection | [Requirements](040-openhab/spec.md) | [Design](040-openhab/plan.md) |
| 041 Weather station overrides | [Requirements](041-weather-station-overrides/spec.md) | [Design](041-weather-station-overrides/plan.md) |
| 042 Faceplates | [Requirements](042-faceplates/spec.md) | [Design](042-faceplates/plan.md) |
| 043 Flow sharing | [Requirements](043-flow-sharing/spec.md) | [Design](043-flow-sharing/plan.md) |
| 044 Tile appearance | [Requirements](044-tile-appearance/spec.md) | [Design](044-tile-appearance/plan.md) |
| 045 Home and overview | [Requirements](045-home-overview/spec.md) | [Design](045-home-overview/plan.md) |
| 046 Help center | [Requirements](046-help-center/spec.md) | [Design](046-help-center/plan.md) |
| 047 Connection and agent access | [Requirements](047-connection-agent-access/spec.md) | [Design](047-connection-agent-access/plan.md) |
| 048 Automatic updates | [Requirements](048-automatic-updates/spec.md) | [Design](048-automatic-updates/plan.md) |
| 049 Device auto import | [Requirements](049-device-auto-import/spec.md) | [Design](049-device-auto-import/plan.md) |

The standalone automation library's six spec/plan pairs live under
[automation/specs/](../automation/specs/README.md). They cover composition, input,
capture/inspection, desktop services, session access/helpers, and shared-session
transport. Root feature 010 remains the CLI consumer contract.

## Reading and changing the baseline

Start with [system architecture](architecture.md) and the relevant feature pair.
Requirement IDs are local to a feature; cite `003-flow-execution/FR-004` rather
than an unqualified `FR-004` when discussing several specs. Each user story has
acceptance scenarios and an independent verification path. Success criteria use
observable outcomes rather than invented latency or throughput promises.

Plans include state models, interfaces, failure handling, source references,
and the applicable Makefile/live checks. Feature guides in `docs/` remain the
usage reference. Update guide, spec, and plan together when behavior changes.
See [the Spec Kit workflow](spec-kit.md) and
[constitution](../.specify/memory/constitution.md) before planning new work.

There are no baseline task lists. Generate tasks for actual changes after
reviewing their spec and plan. Select a feature with `SPECIFY_FEATURE_DIRECTORY`, not a
branch checkout. Scripts number root and module features independently. Use
`--scope automation` when creating library features and their full module path
when selecting them. The root aggregate `013-automation-module` was replaced
by the module-local specs; their index records its requirement relocation.

## Coverage

| Existing behavior | Governing specs |
| --- | --- |
| Configuration, install/upgrade, database, users/groups/roles, API keys, health/metrics, logs | 001 |
| Graph JSON, ports, parameters, editor, subflows, draft revisions, import/export, restoration | 002 |
| Scheduling, retries, failure branches/error flows, cancellation, test runs, run history, variables/node stores | 003 |
| MQTT/HA/OAuth2 connections, controls, MQTT devices/scripts, HA Assist bridge, service and payload discovery | 004 |
| JavaScript/Python/Bash sandbox, HTTP network policy, synchronous webhooks, server file roots | 005 |
| Dashboard drafts/versions, grid/sections, widgets, config validation, themes, import/export | 006 |
| Dashboard/group sharing, layout grants, public control, plugin scopes, renders/schedules/tokens | 007 |
| Built-in/executable/widget-only plugins, registration, settings, lifecycle, SDK and catalogs | 008 |
| Agent enrollment, mTLS, revocation, certificate/CA rotation, host nodes and service install, agent container image | 009 |
| Desktop commands, SSH attachment, resident installation, screenshot targets, state waits, command files, evidence | 010 |
| AI providers/models/chains/agents/tools, transcripts/streams, memory/embeddings, RAG and guardrails | 011 |
| MCP tools/resources/auth, node tests, dependencies/diffs/state inspection, scoped editor chat | 012 |
| Time windows, connection status triggers and numeric transforms | 016 |
| Private flow and shared global SQLite queries, persistence and SQL confinement | 017 |
| Notification provider catalog, encrypted settings and endpoint allowlists | 018 |
| Agent process policies, HTTP requests/health, service confirmation, guarded writes and capture mapping | 020 |
| Agent command/script cooperative termination and bounded group escalation | 021 |
| Agent browser questions, selected outputs, Markdown attachments and feedback | 022 |
| Shared TCP, UDP and WebSocket sockets, framing, replies and private requests | 019 |
| Dashboard variables: custom, constant, time range, interval, and Home Assistant or time-series query variables, templated bindings, option-bounded grants, pickers, commands and conditions | 025 |
| Script-hosted node functions, invocation history, generated script SDK metadata, JavaScript/Python/Bash completion, hover and verification | 026 |
| Prometheus, InfluxDB and PostgreSQL connections, Home Assistant history queries, query/write nodes, history forwarding and write buffering | 027 |
| Chart widget visualizations, placement-bound queries, field options, overrides and transformations | 028 |
| Connection and agent owners and shares, run users, run-time and deploy access checks | 047 |
| Server and agent self-update from GitHub releases, verification, rollback | 048 |
| Connection auto import of discovered devices, Home Assistant MQTT discovery, removal of devices no longer reported | 049 |
| Public automation API, options/lifecycle, backend selection, restore tokens | automation/specs/001 |
| Keyboard/mouse, key parsing, clocks, portal pointer geometry | automation/specs/002 |
| Capture, windows/actions, accessibility, displays/modes | automation/specs/003 |
| Clipboard, notifications/dialogs, speech, media/power, idle, opening | automation/specs/004 |
| Desktop discovery, account selection, helpers, session actions, secure input | automation/specs/005 |
| Authenticated shared-session transport, serialization, cancellation, no replay | automation/specs/006 |

A catalog node's particular fields and ports are defined by its registered
metadata and config schema. The specs define shared behavior rather than a copy
of every node's field list. Widget packages own their documented configuration
and optional schemas. This avoids duplicating catalogs that the application
already serves.

Proposals written outside these specs do not override the implemented
behavior they describe. New integrations, performance targets, protocol
guarantees, and compatibility migrations need their own requirements.

[015 Stateful flow nodes](015-stateful-flow-nodes/spec.md) adds hysteresis, fresh condition waits, keyed timers, dedupe expiry/reset, HA confirmation and HTTP retry controls.
