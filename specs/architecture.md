# System architecture

Karakuri has three executables and one web application. The server owns
persistent automation state and orchestration. The agent runs host actions.
The automation CLI and its standalone Go module reach a local desktop. The
Svelte frontend authors flows and dashboards through the server.

## Components and ownership

| Component | Owns | Boundary |
| --- | --- | --- |
| `cmd/karakuri`, `internal/config`, `internal/resources` | Server composition, config, resources, service CLI | HTTP listener and installation data |
| `pkg/api`, `pkg/middleware`, `pkg/auth`, `pkg/permissions` | REST transport, sessions, API keys, grants | Authenticated user and route/document access |
| `pkg/models`, `pkg/database`, `pkg/migrate` | Persistent records, schema/seed changes | SQLite/PostgreSQL/MySQL |
| `pkg/flow`, `pkg/flowops` | Graph types, validation, revisions, deployment | Draft document versus immutable version |
| `pkg/scriptsdk` | Script function catalogs, call contracts, static diagnostics | Derived registry metadata without node execution |
| `pkg/engine` | Trigger subscriptions, active runs, retry/routing, script invocation, pull/state/history | Node API and bounded workers |
| `pkg/connmgr`, `pkg/connops`, `pkg/integrations` | Connections and drivers | MQTT, HA, model, MCP, OAuth2, time-series endpoints |
| `pkg/tsdb`, `pkg/tsdbops`, `pkg/tswriter`, `pkg/historyfwd`, `pkg/dashchart` | Time-series queries, write buffers, history forwarding, chart data | Prometheus, InfluxDB, PostgreSQL, HA recorder; on-disk spool |
| `pkg/controls`, `pkg/mqttrelay` | Owned values and device normalization | Validated interactions and sandboxed scripts |
| `pkg/dashops`, `pkg/dashscope`, `pkg/render` | Layouts, derived grants, render jobs | Shared/public viewers and read-only browsers |
| `pkg/plugin`, `pkg/pluginhost`, `pkg/widgets`, `pkg/themes` | Contributions and resource catalogs | Trusted plugins and widget packages |
| `pkg/ca`, `pkg/agenthub`, `agent/` | Enrolled identities and remote execution | Mutually authenticated host connection |
| `automation/` | Public desktop capabilities, defined in [module specs](../automation/specs/README.md) | Caller options, OS/session permissions, helper transport |
| `cmd/karakuri-auto` | Desktop commands, waits, evidence, resident installation (010) | Authenticated local forwarding and command status |
| `pkg/nodes/ainodes`, `pkg/memories` | Model workflows and persistent recall | Provider capabilities and session-scoped facts |
| `pkg/mcpserver` | External and editor tool adapters | Common operations plus per-tool/document gates |
| `frontend/src` | Canvas, forms, dashboards, streamed editor chat | Shared server drafts and catalog metadata |

## Request and execution paths

REST and MCP authenticate a principal and check operation permissions. Both
call feature operations, which validate inputs and own transactions. Editor
chat calls the MCP handlers with selected-document restrictions on mutations and
some inspections; permitted general flow reads still use ordinary permissions.
A browser cannot widen its grant by hiding or showing a button. Live events
pass through registered topic gates; unmatched subscriptions fail closed.

Flow edits change a mutable draft. Deployment strictly validates trigger-reachable
execution graphs and freezes the whole graph and metadata as a numbered version.
Shared subflows and graphs without triggers are validated in full. Enabled
triggers subscribe against that version. An explicit test validates and executes
its selected draft execution graph without publishing.
The engine flattens subflows, pulls parameter values, schedules message-group
instances, applies retries, and routes handled failures. JavaScript, Python, and
Bash flow scripts can also invoke eligible nodes synchronously. Those calls execute synthetic node
instances in the same run with separate invocation history, direct outputs, and
per-call retries; they do not route to graph edges. Once any call begins, an
outer script retry is suppressed. Cancellation propagates
to nodes, parked waits, remote requests, script calls, and child processes where supported.
Parked waits release execution slots and pause the engine run budget; standalone node
tests instead use a wall-clock cancellation deadline. Cancellation is cooperative, so
the engine does not promise forced interruption of context-ignoring calls.

Dashboard edits change a shared draft, and deployment freezes published content.
An existing deployment remains the ordinary viewer's version; an undeployed
dashboard shows its draft. Live sharing and public policy decide who may view.
The applicable layout's registered widget bindings decide which data or control
that viewer receives. An editor can inspect its draft. A render job gets a
separate read-only credential and uses the same view/derived-scope model.
Deployment does not invoke the explicit dashboard validator. Controls and triggers
with `allow_public` can be reached independently of dashboard scope.

## Persistent and transient state

| State | Lifetime | Contract |
| --- | --- | --- |
| Flow/dashboard draft | Across saves and restarts | Mutable authoring document; supplied flow graph revisions reject stale saves, patches require revisions |
| Deployment/version | Persistent | Immutable content; enablement/sharing remain separate live state |
| Active run and message/run variables | In-process execution | Cancellable; no restart-resumption guarantee |
| Pending waits (timers, held messages, partial groups) | Across restarts and redeploys | Resumed in a new run from the waiting node; claimed before emitting, so at most once; dropped on disable and for removed or retyped nodes (feature 029) |
| Run/event history | Bounded persistent evidence | 100 completed runs per flow plus active runs; asynchronous bounded writes; script calls carry parent and sequence metadata |
| Flow/global variables, durable node store | Across runs/restarts | Async flush can lag active work; abrupt termination can lose pending writes; script state uses explicit invocation identities |
| Controls and MQTT device values | Persistent controls / stored device state | Controls own stored values; device scripts patch observed attributes, which are stored every few seconds and restored marked stale until the device reports (feature 029) |
| User/role/share/API-key records | Persistent identity/access | Route and document checks stay authoritative |
| Connection configs and encryption key | Persistent secret state | Existing ciphertext requires its original key |
| History forwards / write spools | Persistent settings / on-disk batches per connection | Spools survive restarts, drain oldest first, and are capped; deleted with their connection |
| Agent keys/certificates/CA bundles | Persistent trust state | Renewal preserves key identity and rollover overlaps trust |
| Memory stores/facts/transcripts/chunks | Persistent knowledge | Session scoping, source replacement, optional async embeddings |
| Render jobs/files/schedules | Bounded/job or recurring lifetime | Credential ends with job; schedules retain output until replacement or 30-day expiry |
| Assistant conversation | Current editor tab | Completed writes persist; model replay resets after interruption |

## Trust boundaries

User scripts run in isolated interpreters with mediated HTTP access. Capable
JavaScript, Python, and Bash flow scripts also receive mediated node invocation
through the active run. The `script.network` policy covers only the script HTTP helper; called nodes
keep their canvas authority, and no driver or credential enters the interpreter.
Server file nodes use explicit storage roots. Device scripts have no network or
node-invocation capability and shorter execution budgets. Standalone script tests
also lack invocation. These restrictions do not apply to external plugin
executables or remote agent commands/scripts, which run with their OS account's
permissions.

The agent trusts roots supplied in its enrollment token, not roots supplied by
an unverified server response. Certificate renewal and CA overlap retain that
trust. Revoked serials are refused at handshake and removed from live sessions.
CA promotion counts issued certificates in server records, without acknowledgement of
installed agent trust; failed delivery or storage can strand an agent after promotion.
Desktop access still depends on the selected user's active session, OS privacy
grants, portal consent, and backend support.

Dashboard identifiers in headers or subscription frames are claims. Actual
view access and layout-derived scope remain required. Rendering never borrows
an owner's session and never grants control. Plugin APIs receive trusted grants
separately from client headers. Uploaded widget files follow the dashboard's view
access, checked through the bearer token or the `karakuri_media` cookie, as spec 007
documents. Public control and trigger routes remain independently available during
rendering. A deployed version that cannot be found or parsed fails closed rather than
falling back to draft data.

## Verification and maintenance

The [constitution](../.specify/memory/constitution.md) and `AGENTS.md` govern
builds, tests, state preservation, and commit workflow. Each feature plan maps
its requirements to existing tests and live checks. The Makefile runs component
checks; tagged integration tests cover real HTTP lifecycle and sandbox paths.
The [server and agent audit](server-agent-audit.md) records requirement coverage,
executed checks, and remaining verification gaps.
MCP is the preferred running-service check. Canvas derivations and editor
interaction need browser evidence. Desktop changes need the documented host
checks; unit fixtures do not certify compositor behavior or OS privacy grants.

This baseline changes no runtime behavior, persistent schema, enrolled identity,
or encryption format. A future design change must state its affected formats
and migration before modifying irreplaceable development state.
