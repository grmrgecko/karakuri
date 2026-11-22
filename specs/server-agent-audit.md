# Server and agent specification audit

**Date**: 2026-10-04
**Runtime baseline**: `5158025`
**Specification baseline reviewed**: `a984dea`
**Follow-up specification baseline reviewed**: `0bbb459`
**Cancellation and transport review baseline**: `201caa6`
**Host boundary and evidence review baseline**: `1c0be6f`
**Memory consistency review baseline**: `108b913`
**Scope**: Root features 001-009 and 011-012, including their plans and usage guides.

The review maps all 179 functional requirements in these eleven features to their
implementation. It corrects claims about authorization, persistence, retries, and
resource limits and adds omitted session, credential, rotation, and webhook behavior.
Root feature 010 and the six automation-library features retain their separate contracts.
Agent use of that library was checked at the integration boundary.

The tables identify implementation owners and existing fixtures for each requirement
group. A fixture link means that selected behavior in the group has an executable check;
it does not mean every clause or success criterion has a test. "Source only" identifies
claims without a corresponding fixture or fresh live check. Frontend checks establish
type correctness, not browser interaction. No runtime code or persistent format changed.

## Corrections

| Feature | Corrected or added contract |
| --- | --- |
| 001 | Both JWT kinds depend on a matching live session. Refresh tokens rotate and a replay outside the 30-second grace period revokes the session. User config outranks system config; adjacent resource directories exclude installed resources. WebSockets accept access JWT/render identity rather than REST API keys; open sockets are revalidated and lose revoked subscriptions. Login throttling and all four encrypted-record owners are specified. Database/config key rotation has two commits. |
| 003 | Message-path action retries exclude parameter resolution and pure pulls. Parameter failure bypasses the consumer's error port. Parked intervals pause the shared run budget; retry waits hold their slot. Cancellation is cooperative and shutdown's five-second run wait excludes sink flushes. Only successful pure outputs are cached. History can drop terminal writes after five seconds. Webhook body reads truncate at 1 MiB; response timeout/disconnect does not cancel the run. Public routing exposes GET/POST only and duplicate tokens use the last registration. |
| 004 | Connection startup failure leaves saved config intact. Masked sentinels preserve secrets; undecryptable configs remain replaceable. Kindless MQTT devices bypass control coercion. Device definitions persist, but decoded state resets on restart and commands do not invent observed state. |
| 005 | Interpreter failure discards staged variables. A later message/output-shape error can fail after persistent writes were applied. Pulled scripts use the parameter-failure contract. |
| 006 | Placement catalogs filter loaded packages, but direct writes can retain unloaded packages or invalid geometry. Explicit validation reports these problems; deployment does not run it. Dashboard edits have no revision check. Theme selection is frozen by ID, not by copying CSS/bundles. |
| 007 | Independently public controls appear beyond layout bindings. `allow_public` controls/triggers retain their public operation paths despite display-only or render scope. Render credentials add no write capability but do not disable anonymous routes. Established sockets do not recheck job lifetime or later scope changes. Scheduled results expire after 30 days without replacement. |
| 009 | Expired/disabled agents do not delay CA promotion. The 32 MiB cap applies to returned output; disk spools are unbounded. File rollback can fail and is not crash-atomic. Execution wrapper deadlines, encrypted credentials, overlapping trust, and next-connection renewal are specified. Promotion counts presented certificates and sessions end at certificate expiry. Process termination reaches setsid descendants on Unix and the whole job on Windows; orphans of a Unix descendant that left the group are out of reach. Checkpoints live on the server and carry a byte fingerprint; rotation drains the old file within the per-poll batch, and only post-quiet old-file writes or a mid-drain restart lose log data. |
| 011 | Session isolation applies to recall, while authorized browsing can see all sessions. Reasoning settings map differently by provider. Background embedding is bounded, can drop work, and backfills on load rather than immediately retrying. Late provider responses are discarded after edits. Source replacement is transactional and serialized; chunk budgets count bytes and always advance. |
| 012 | Selected-document restrictions cover writes and specific inspection tools; general flow/reference reads can inspect other documents under ordinary permissions. Allowed node/flow tests can operate devices even though direct command tools are excluded. Standalone tests use a separate wall-clock deadline, a five-second cancellation wait, and bounded capture rather than a whole-response size cap. The assistant stream belongs to `mcpserver/assistant.go`. |

Feature 002's graph/revision/subflow contracts and feature 008's catalog/unload limits
remain as corrected in the [earlier audit](code-audit.md). Plans no longer claim that
all product questions or verification work are resolved.

## Requirement evidence

Requirement numbers below are local to the named feature. Ranges include both endpoints.

### 001 Server operation and identity

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-005 | [Authentication/session service](../pkg/auth/service.go), [bearer/permission middleware](../pkg/middleware/auth.go), [API keys](../pkg/models/api_key.go), [permission matching](../pkg/permissions/registry.go) | [API router fixtures](../pkg/api/flow_handler_test.go), [key round trip](../pkg/models/api_key_test.go), [MCP authorization](../pkg/mcpserver/mcpserver_test.go). Session deletion, JWT audience/expiry, and refresh reuse are source only. |
| FR-006 through FR-009 | [Database setup](../pkg/database/database.go), [migrations](../pkg/migrate/migrate.go), [lookup rules](../internal/resources/loader.go), [server config](../internal/config/server.go), [installer](../install.sh), [encrypted connections](../pkg/models/connection.go) | [SQLite fixtures](../pkg/database/database_test.go), [migration fixtures](../pkg/migrate), [connection masking](../pkg/api/connection_handler_test.go). Install/upgrade and real PostgreSQL/MySQL operation are source only. |
| FR-010 | [Service command](../cmd/karakuri/service.go), [service managers](../internal/service/service.go) | Development user unit restarted and active, HTTP 200. Other service managers are source only in this audit. |
| FR-011 through FR-012 | [Top-level router](../pkg/http/router.go), [health](../pkg/api/health_handler.go), [HTTP limits](../pkg/http/server.go), [socket authentication](../pkg/mq/handler.go), [MCP deadline](../pkg/mcpserver/server.go) | [Topic gates](../pkg/mq/gate_test.go), [render tokens](../pkg/api/render_handler_test.go). Live health/readiness/metrics and unauthenticated MCP refusal checked. Proxy composition is source only; see the limit below. |
| FR-013 | [Login throttle](../pkg/auth/throttle.go), [login handler](../pkg/api/auth_handler.go) | [Throttle fixtures](../pkg/auth/throttle_test.go); login-handler integration is source only. |
| FR-014 | [Rotation command](../cmd/karakuri/config_cmd.go), [CA rekey](../pkg/agenthub/rekey.go), [agent credential rekey](../pkg/models/agent.go) | [Config staging](../cmd/karakuri/config_cmd_test.go). The complete multi-record rotation/final-rename failure path is source only; no real keys were rotated. |

### 002 Flow graphs and deployment

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-004 | [Graph](../pkg/flow/graph.go), [validation](../pkg/flow/validate.go), [lifecycle](../pkg/flowops/flowops.go), [trigger runner](../pkg/engine/runner.go) | [Flow lifecycle](../pkg/api/flow_handler_test.go), [selected test graphs](../pkg/api/run_handler_test.go), [validation fixtures](../pkg/flow/validate_test.go). |
| FR-005 through FR-006 | [Draft saving](../pkg/flowops/flowops.go), [atomic patches](../pkg/flowops/patch.go) | [Patch fixtures](../pkg/mcpserver/inspection_test.go); live stale save and failed patch batch preserved the winning graph revision. |
| FR-007 through FR-010 | [Resolved ports](../pkg/flow/ports.go), [parameters](../pkg/flow/params.go), [type propagation](../pkg/flow/typeflow.go), [validation](../pkg/flow/validate.go) | [Port fixtures](../pkg/flow/ports_test.go), [type fixtures](../pkg/flow/typeflow_test.go), [multi-source pull order](../pkg/engine/pull_test.go). |
| FR-011 through FR-013 | [Subflows](../pkg/flow/subflow.go), [bypass](../pkg/flow/bypass.go), [export](../pkg/flowops/export.go) | [Subflow fixtures](../pkg/flow/subflow_test.go), [bypass fixtures](../pkg/flow/bypass_test.go), [export fixtures](../pkg/flowops/export_test.go). |
| FR-014 through FR-015 | [Editor components](../frontend/src/lib/components/editor), [editor store](../frontend/src/lib/stores/editor.ts), [lifecycle notifications](../pkg/flowops/flowops.go) | Frontend check passed; live port derivation, camera restoration, and editor refresh are source only here. No fresh browser check. |

### 003 Execution, failures, and persistent state

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-003 | [Run creation](../pkg/engine/engine.go), [parking/timeout](../pkg/engine/run.go), [trigger runner](../pkg/engine/runner.go), [draft testing](../pkg/flowops/testrun.go) | [Concurrency/timeout fixtures](../pkg/engine/engine_test.go), [park beyond budget/slot release](../pkg/engine/fanout_test.go), [trigger/test payload fixtures](../pkg/api/run_handler_test.go), [test-run fixtures](../pkg/flowops/testrun_test.go). The shared pause while another branch works and retry-slot occupancy are source only. |
| FR-004 through FR-007 | [Action retries/failure routing](../pkg/engine/retry.go), [pure pulls](../pkg/engine/pull.go), [error flows](../pkg/engine/service.go), [tool calls](../pkg/engine/toolcalls.go) | [Retries/cancel/error-flow fixtures](../pkg/engine/engine_test.go), [contained tool failures](../pkg/engine/toolcall_test.go). Pure-pull retry exclusion and parameter-error bypass are source only. |
| FR-008 through FR-010 | [Scheduler](../pkg/engine/run.go), [pure values](../pkg/engine/pull.go), [variables](../pkg/engine/varstore.go), [durable store](../pkg/engine/durablestore.go) | [Fan-out](../pkg/engine/fanout_test.go), [execute-once](../pkg/engine/executeonce_test.go), [pull memoization](../pkg/engine/pull_test.go), [variables](../pkg/engine/varstore_test.go), [durable round trip](../pkg/engine/durablestore_test.go). |
| FR-011 through FR-014 | [Events](../pkg/engine/events.go), [history queue](../pkg/engine/history.go), [run retention](../pkg/models/run.go), [shutdown](../pkg/engine/engine.go) | [Output events](../pkg/engine/m5_test.go), [100-run retention/exact IDs](../pkg/mcpserver/draft_test.go), [shutdown cancellation](../pkg/engine/engine_test.go). Queue saturation, dropped terminal writes, and the five-second wait with an unresponsive node are source only. |
| FR-015 | [Webhook handler](../pkg/http/webhook.go), [registration/HMAC](../pkg/engine/webhooks.go), [public methods](../pkg/http/router.go) | [Webhook HTTP](../pkg/http/webhook_test.go), [synchronous replies](../pkg/http/webhook_sync_test.go), [signature/IP/method fixtures](../pkg/engine/webhookauth_test.go). Proxy middleware interaction and oversized-body behavior are source only. |

### 004 Connections, MQTT devices, and controls

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-005 | [Connection operations](../pkg/connops/connops.go), [driver lifecycle](../pkg/connmgr/manager.go), [integration drivers](../pkg/integrations) | [Connection API](../pkg/api/connection_handler_test.go), [MQTT](../pkg/integrations/mqtt/driver_test.go), [HA](../pkg/integrations/homeassistant/driver_test.go), [OAuth2](../pkg/integrations/oauth2conn/driver_test.go). |
| FR-006 through FR-007 | [Control catalog](../pkg/controls/kinds.go), [control store](../pkg/controls/store.go) | [Control fixtures](../pkg/controls/store_test.go), [HA bridge](../pkg/api/ha_handler_test.go). |
| FR-008 through FR-010 | [Device subscription/state scripts](../pkg/mqttrelay/device.go) | [Device/state fixtures](../pkg/mqttrelay/device_test.go), [live relay fixture](../pkg/mqttrelay/device_live_test.go). |
| FR-011 through FR-014 | [Command coercion/scripts](../pkg/mqttrelay/command.go), [control kinds](../pkg/controls/kinds.go) | [Command fixtures](../pkg/mqttrelay/command_test.go), [device node fixtures](../pkg/nodes/mqttnodes/devices_test.go). |
| FR-015 through FR-016 | [Discovery](../pkg/mcpserver/tools_discovery.go), [HA Assist](../pkg/nodes/hanodes/assist.go) | [Discovery/schedule fixtures](../pkg/mcpserver/discovery_test.go), [Assist correlation](../pkg/nodes/hanodes/assist_test.go), [HA component tests](../homeassistant/tests). |
| FR-017 through FR-018 | [Config replacement/masking](../pkg/connops/connops.go), [process-local device state](../pkg/mqttrelay/device.go) | [Secret sentinel fixture](../pkg/api/connection_handler_test.go). Undecryptable-list fallback, retained definitions on driver failure, and empty state after restart are source only. |

### 005 Sandboxed logic and server file access

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-002 | [Runtime registry/bridge](../pkg/sandbox/sandbox.go), [JavaScript](../pkg/sandbox/js.go), [Python](../pkg/sandbox/python.go), [Bash](../pkg/sandbox/shell.go) | [Runtime isolation fixtures](../pkg/sandbox/sandbox_test.go), [real HTTP sandbox suite](../test/integration/lifecycle_test.go). Optional Python checks require its wasm asset. |
| FR-003 through FR-005 | [Script ports](../pkg/flow/scriptports.go), [script outputs](../pkg/nodes/core/script.go), [retry/failure handling](../pkg/engine/retry.go) | [Port fixtures](../pkg/flow/ports_test.go), [script nodes](../pkg/nodes/core/script_test.go); live multi-output shape failure recorded against its exact run ID. |
| FR-006 through FR-011 | [Budgets](../pkg/sandbox/sandbox.go), [network policy](../pkg/sandbox/netpolicy.go), [Python runtime](../pkg/sandbox/python.go) | [Timeout/output/network fixtures](../pkg/sandbox/sandbox_test.go). Per-response truncation, fifth redirect, and frozen guest clock also inspected in source. |
| FR-012 through FR-013 | [Script write ordering](../pkg/nodes/core/script.go), [pulls](../pkg/engine/pull.go), [webhook responses](../pkg/engine/responses.go) | [Interpreter-write failure fixture](../pkg/nodes/core/script_test.go), [synchronous response fixture](../pkg/http/webhook_sync_test.go); live checks distinguished interpreter failure from later shape failure. |
| FR-014 | [File roots/read bounds](../pkg/nodes/core/file.go) | [File fixtures](../pkg/nodes/core/file_test.go). Root checks resolve existing symlinks before access; no race-proof filesystem sandbox is claimed. |

### 006 Dashboard layout and publication

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-005 | [Layout/sections](../pkg/dashops/dashops.go), [placements](../pkg/dashops/widgets.go), [loaded catalog](../pkg/api/widget_handler.go), [section handlers](../pkg/api/dashboard_handler.go) | [Section/placement fixtures](../pkg/mcpserver/assistant_test.go). Persistence of invalid placement geometry and unloaded-package rows is source only. |
| FR-006 through FR-010 | [Deployment/restore](../pkg/dashops/dashops.go), [snapshots](../pkg/models/dashboard_version.go) | [Shared-draft lifecycle](../pkg/mcpserver/draft_test.go), [layout inspection](../pkg/mcpserver/inspection_test.go). Live multi-editor updates are source only. |
| FR-011 through FR-012 | [Browser config merge/context stripping](../frontend/src/routes/DashboardEdit.svelte), [config replacement](../pkg/dashops/widgets.go) | Frontend check passed; config event handling is source only. |
| FR-013 through FR-016 | [Explicit validation](../pkg/dashops/validate.go), [bindings/dependencies](../pkg/mcpserver/tools_validate_dashboard.go), [export](../pkg/dashops/export.go), [renderer](../pkg/render/inspect.go), [themes](../pkg/themes/loader.go) | [Dashboard inspection fixtures](../pkg/mcpserver/inspection_test.go), [render diagnostics](../pkg/render/inspect_test.go). Deployment's lack of a validation gate and live theme CSS are source only. |

### 007 Dashboard sharing and rendering

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-004 | [Document access](../pkg/dashops/dashops.go), [share model](../pkg/models/dashboard.go), [request scope](../pkg/dashscope/request.go), [served bindings](../pkg/dashscope/derive.go) | [Public/private dashboard fixtures](../pkg/api/dashscope_test.go), [render draft scope](../pkg/api/render_handler_test.go). Undeployed-draft grants and broken-version fallback are source only. |
| FR-005 through FR-008 | [Scope derivation](../pkg/dashscope/derive.go), [binding permissions](../pkg/dashscope/scope.go), [control routes](../pkg/api/control_handler.go), [HA routes](../pkg/api/ha_handler.go) | [Binding parser](../pkg/dashscope/binding_test.go), [scoped API fixtures](../pkg/api/dashscope_test.go). Independent `allow_public` writes are also inspected in source. |
| FR-009 through FR-011 | [Socket gates](../pkg/mq/handler.go), [plugin grants](../pkg/apiutil/pluginhttp.go), [scope patterns](../pkg/dashscope/derive.go) | [Gate fixtures](../pkg/mq/gate_test.go), [dashboard fixtures](../pkg/api/dashscope_test.go), [revalidation](../pkg/mq/revalidate_test.go). Open sockets are revalidated after writes and every 30 seconds; the revalidation fixture withdraws a subscription after its dashboard goes private. |
| FR-012 through FR-016 | [Render tokens](../pkg/models/render.go), [queue/lifecycle](../pkg/render/render.go), [schedules](../pkg/render/scheduler.go), [diagnostics](../pkg/render/inspect.go) | [Token/URL fixtures](../pkg/api/render_handler_test.go), [image conversion](../pkg/render/render_test.go), [diagnostic redaction](../pkg/render/inspect_test.go). Real Chromium, queue saturation, restart settlement, and 30-day retention are source only. |
| FR-017 through FR-018 | [Upload serving](../pkg/uploads/service.go), [public controls](../pkg/api/control_handler.go), [public trigger invocation](../pkg/api/widgettrigger_handler.go) | [Widget-trigger fixture](../pkg/api/widgettrigger_handler_test.go); upload access and render/public-route exceptions are source only. |

### 008 Plugins and extensible catalogs

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-005 | [Discovery](../pkg/pluginhost/manifest.go), [contributions](../pkg/pluginhost/host.go), [external transport](../pkg/pluginhost/external.go), [AI ownership](../pkg/integrations/ai/providers.go) | [Manifest fixtures](../pkg/pluginhost/manifest_test.go), [external execution](../pkg/pluginhost/external_test.go), [AI lifecycle](../pkg/pluginhost/aiprovider_test.go), [widget loader](../pkg/widgets/loader_test.go). |
| FR-006 through FR-010 | [Manager lifecycle/state](../pkg/pluginhost/external.go), [inventory](../pkg/pluginhost/inventory.go), [registries](../pkg/pluginhost/host.go) | [Built-in registrations](../pkg/pluginhost/host_test.go), [AI provider lifecycle](../pkg/pluginhost/aiprovider_test.go). Vanished-directory cleanup and persisted enable/error state are source only. |
| FR-011 through FR-014 | [Widget schemas](../pkg/widgets/loader.go), [host settings/APIs](../pkg/pluginhost/hostservice.go), [SDK](../pkg/plugin/sdk), [topic gates](../pkg/mq/handler.go) | [Widget loader](../pkg/widgets/loader_test.go), [topic gates](../pkg/mq/gate_test.go). SDK dependency separation and settings permission checks also inspected in source. |

### 009 Remote agent trust and host actions

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-004 | [Join tokens](../pkg/ca/join.go), [enrollment](../agent/identity/enroll.go), [TLS hub](../pkg/agenthub/agenthub.go), [agent administration](../pkg/api/agent_handler.go) | [Join verification](../pkg/ca/join_test.go), [enroll/connect](../pkg/agenthub/agenthub_test.go), [serial revocation validation](../pkg/ca/ca_test.go), [replacement-session isolation](../pkg/agenthub/lifecycle_test.go). Operator revocation through the live hub/API and subsequent reconnect refusal are source only. Failed enrollment may leave a key file before a complete identity exists. |
| FR-005 through FR-007 | [Persisted CA](../pkg/agenthub/castore.go), [renewal](../agent/identity/renew.go), [issued certificate tracking](../pkg/agenthub/handlers.go), [promotion readiness](../pkg/agenthub/agenthub.go) | [Listener restart](../pkg/agenthub/castore_test.go), [renewal/rollover](../pkg/agenthub/renew_test.go). Lost-response/local-storage promotion failures and periodic retry timing are source only. |
| FR-008 through FR-010 | [Agent catalog](../agent/agent.go), [server namespaces](../pkg/pluginhost/agent.go), [node groups](../agent) | [Disable-list fixtures](../agent/agent_test.go), [execution transport](../pkg/agenthub/execute_test.go), [metrics/session fixtures](../agent/system). Cross-platform host actions are source only here. |
| FR-011 through FR-013 | [Process output](../agent/exec/capture.go), [command cancellation](../agent/exec/command.go), [Unix process group](../agent/exec/process_tree_unix.go), [Windows tree/fallback](../agent/exec/process_tree_windows.go), [staged files](../agent/storage/atomic.go), [tail checkpoints](../agent/storage/logtail.go) | [512 KiB mirrored stream](../agent/exec/capture_test.go), [replacement rollback](../agent/storage/transfer_test.go), [rotation/resume/batch/clipping/rewrite](../agent/storage/logtail_test.go). The rewrite and cross-rotation batch fixtures reproduce the losses the earlier temporary checks found. [Termination fixtures](../agent/exec/termination_test.go) cover a setsid descendant. Windows job termination was checked live on the Windows 11 host: an orphaned grandchild that `taskkill /T` left running was stopped. The 32 MiB failure, the taskkill fallback, and rollback failure are source only. |
| FR-014 through FR-016 | [Inspection](../pkg/agentops/agentops.go), [service definition](../cmd/karakuri-agent/service.go), [execution wrapper](../agent/agentnode/budget.go) | [Agent config/dependency fixtures](../cmd/karakuri-agent). Fresh MCP status/capability reads covered ten records, including six connected agents. Service-manager behavior and wrapper deadline/grace are source only. |
| FR-017 through FR-018 | [Password store](../agent/credstore/credstore.go), [key fetch](../agent/credkey/credkey.go), [server key authorization](../pkg/agenthub/handlers.go), [bundle overlap](../pkg/ca/bundle.go), [renewal](../agent/identity/renew.go) | [Credential tamper/AAD fixtures](../agent/credstore/credstore_test.go), [CA bundle verification](../pkg/ca/join_test.go), [renewal fixture](../pkg/agenthub/renew_test.go). No real credential or enrolled identity was changed. |

### 011 AI processing, tools, and memory

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-004 | [Model handles](../pkg/nodes/ainodes/model.go), [typed agent inputs](../pkg/nodes/ainodes/agent.go), [tool dispatch](../pkg/nodes/ainodes/dispatch.go) | [Tool loops](../pkg/nodes/ainodes/agent_test.go), [provider capability rejection](../pkg/nodes/ainodes/capabilities_test.go). |
| FR-005 through FR-008 | [Structured repair](../pkg/nodes/ainodes/structured.go), [schema subset](../pkg/nodes/ainodes/schema.go), [response/replay](../pkg/nodes/ainodes/agent.go), [provider wires](../pkg/integrations/ai) | [Schema fixtures](../pkg/nodes/ainodes/schema_test.go), [repair/transcripts](../pkg/nodes/ainodes/agent_test.go), [streaming chain](../pkg/nodes/ainodes/capabilities_test.go), [wire fixtures](../pkg/integrations/ai/wire_test.go). |
| FR-009 through FR-010 | [Graph tool ports](../pkg/flow/toolports.go), [call groups](../pkg/engine/toolcalls.go), [MCP call](../pkg/nodes/ainodes/mcpcall.go), [MCP connections](../pkg/integrations/mcpconn/driver.go) | [Tool arguments/failures](../pkg/engine/toolcall_test.go), [tool port validation](../pkg/flow/toolports_test.go), [MCP connection fixtures](../pkg/integrations/mcpconn/driver_test.go). |
| FR-011 through FR-014 | [Memory persistence](../pkg/memories/memories.go), [recall/browse](../pkg/memories/recall.go), [embedding completion](../pkg/memories/embed.go), [ingestion](../pkg/nodes/ainodes/ingest.go) | [Session/TTL/caps/vectors/MMR](../pkg/memories/store_test.go), [conversation memory](../pkg/nodes/ainodes/memory_test.go), [source replacement](../pkg/nodes/ainodes/rag_test.go), [chunk boundaries](../pkg/nodes/ainodes/ingest_internal_test.go). Temporary real SQLite/HTTP checks first reproduced partial source replacement and stale-vector persistence; permanent regression tests now cover both and the splitter's progress. |
| FR-015 through FR-017 | [Guardrails](../pkg/nodes/ainodes/guardrail.go), [classification](../pkg/nodes/ainodes/classify.go), [summarization](../pkg/nodes/ainodes/summarize.go), [images](../pkg/nodes/ainodes/images.go), [documents](../pkg/nodes/core/document.go) | [Guardrail fixtures](../pkg/nodes/ainodes/guardrail_test.go), [extract/classify](../pkg/nodes/ainodes/rag_test.go), [summarize](../pkg/nodes/ainodes/summarize_test.go), [image metadata](../pkg/nodes/ainodes/images_test.go), [document parser](../pkg/nodes/core/document_test.go). |
| FR-018 through FR-019 | [Provider reasoning](../pkg/integrations/ai), [embedding queue](../pkg/memories/embed.go), [query fallback](../pkg/memories/recall.go) | [Reasoning/replay fixtures](../pkg/integrations/ai/wire_test.go), [backfill](../pkg/memories/store_test.go). Queue exhaustion, failed-batch retry timing, and real provider compatibility are source only. |

### 012 MCP authoring and editor chat

| Requirements | Implementation | Verification evidence |
| --- | --- | --- |
| FR-001 through FR-006 | [Stateless server](../pkg/mcpserver/server.go), [endpoint guard](../pkg/http/router.go), [tool guards](../pkg/mcpserver/auth.go), [guides](../pkg/mcpserver/resources.go) | [MCP lifecycle/resources/permissions](../pkg/mcpserver/mcpserver_test.go). Live initialization negotiated 2025-03-26, listed 82 tools, and refused an unauthenticated request with HTTP 401. |
| FR-007 through FR-011 | [Node preparation](../pkg/flowops/node.go), [isolated engine](../pkg/engine/nodetest.go), [state reads](../pkg/mcpserver/tools_state.go), [patch/diff/discovery tools](../pkg/mcpserver) | [Standalone tests](../pkg/mcpserver/standalone_nodes_test.go), [placed tests](../pkg/mcpserver/nodes_test.go), [discovery/state](../pkg/mcpserver/discovery_test.go), [inspection](../pkg/mcpserver/inspection_test.go). Live standalone script returned 42 without adding flow-run history. Fresh live checks canceled a parked delay and truncated oversized output. Unresponsive-node `canceling`, the 1000-item cap, and ten capture-error entries are source only. |
| FR-012 through FR-017 | [Assistant allowlist/stream](../pkg/mcpserver/assistant.go), [model profile](../pkg/api/user_handler.go), [editor chat](../frontend/src/lib/components) | [Streamed writes/deploy/cross-document refusal](../pkg/mcpserver/assistant_test.go). General reads outside the selection and real node-test side effects are inspected in source. |
| FR-018 | [Editor chat state](../frontend/src/lib/components), [editor integration](../frontend/src/lib/components/editor/EditorShell.svelte) | Source only plus frontend type check. Stop, refresh after interrupted writes, tab-local replay, and model/document-change clearing need fresh browser interaction checks. |

## Verification performed

- Full `make dev` and `make test` passed. Frontend checking reported zero errors and
  warnings; Home Assistant component tests passed. Go cached results were reused where
  applicable. Tests that need server state used a temporary user data directory rather
  than the development database or CA files.
- `KARAKURI_AGENTS_LISTEN=127.0.0.1:0 make test-integration` passed the existing
  isolated HTTP lifecycle/sandbox suite. The first attempt conflicted with the
  development server's agent port 9443; the retry used an ephemeral agent port
  without changing the development configuration or stopping its listener.
- The development unit was restarted and reported active; the HTTP listener returned 200.
- Live MCP checked revision conflicts and atomic patch refusal in one disposable flow.
  Its first script completed interpretation, wrote a flow variable, then failed its
  multi-output shape check. Its second script threw inside the interpreter and preserved
  that prior variable. Both failures were inspected by their exact run IDs. A standalone
  script returned 42 without adding run history. The temporary flow was deleted and
  absence verified; no test flow remains in the development database.

## Follow-up corrections

The follow-up review of `0bbb459` checked remaining acceptance statements and the
constitution against the same runtime source. It found these additional mismatches:

- The constitution's general strict-deployment rule conflicted with explicit dashboard
  validation. Its layout-only permission statement omitted independent subsystem and
  object-level public grants. Version 1.0.2 clarifies these scopes using features 002,
  006, and 007 without changing their implemented policies.
- Enrollment refuses an existing `agent.json`, not any existing directory. A failed
  attempt can leave files that another attempt overwrites with a fresh key. The hub
  consumes the token before signing, so a later failure or lost response can consume a
  token without completing local identity storage. Sources are
  [enrollment](../agent/identity/enroll.go), [identity files](../agent/identity/identity.go),
  and [hub issuance](../pkg/agenthub/handlers.go). These failure paths are source only.
- Agent renewal runs immediately and every six hours, retrying failures at the next
  interval. It attempts renewal rather than guaranteeing it despite network/storage
  failure. Promotion now counts the root of the certificate each agent presents, and
  renewal reissues from it, so a lost response or failed client write is issued again.
  [Renewal](../agent/identity/renew.go), [handshake records](../pkg/agenthub/handlers.go),
  and [promotion readiness](../pkg/agenthub/agenthub.go) implement this; the
  [renewal fixture](../pkg/agenthub/renew_test.go) covers an unpresented issuance and
  a session closed at certificate expiry.
- Undeployed dashboard viewers use draft bindings, including public viewers, so their
  scope changes with draft edits. With a deployment, a version that cannot be found or
  parsed now fails closed in [layout reads](../pkg/dashops/dashops.go) and
  [scope derivation](../pkg/dashscope/derive.go); it previously fell back to the draft.
  `TestUnreadableDeploymentServesNoDraft` corrupts a disposable test database's snapshot
  to pin it. No live database was damaged to test it.

Follow-up verification passed `make dev TARGET=server`, `make dev TARGET=agent`, full
`make test`, and `KARAKURI_AGENTS_LISTEN=127.0.0.1:0 make test-integration`. The agent
artifact was already current; applicable Go test results reused the cache. The Spec Kit
prerequisite helper resolved feature 009 without changing the branch or local selection.
Fresh MCP checks again confirmed stale-save and atomic-patch refusal, interpreter versus
output-shape variable writes, and history-free standalone execution. The disposable flow
was deleted and absence verified. The restarted user unit was active; `/`, `/health`,
`/api/health/ready`, and `/metrics` returned 200. No runtime source, schema, identity, or
encryption format changed.

## Cancellation and transport review

The review of `201caa6` checked remaining timeout and grant-lifetime claims against the
same runtime baseline. It corrected these contracts:

- [Parking and the watchdog](../pkg/engine/run.go) discount the union of parked intervals,
  including tool waits, even when other branches work. The semaphore bounds active slots,
  not every dispatched goroutine. [Retry waits](../pkg/engine/retry.go) hold their slot and
  do not themselves park. Existing fan-out fixtures cover a ten-minute park under a
  one-minute budget and slot release; mixed active/parked branches remain source only.
- Cancellation stops scheduling but drains active calls; ordinary unhandled failure
  drains without canceling them. [Shutdown](../pkg/engine/engine.go) waits five seconds
  for runs before logging abandonment, then flushes sinks. It cannot forcibly interrupt
  a context-ignoring node, and the wait does not bound sink flushes. An ordinary run can
  remain unsettled. The unresponsive-node path is source only.
- [Pure pulls](../pkg/engine/pull.go) cache successful single-value output collections.
  Failed producer executions are not cached, so another consumer can evaluate them again.
  The existing memoization fixtures prove selected successful paths, not every failure.
- [Standalone tests](../pkg/engine/nodetest.go) use a wall-clock cancellation deadline
  that includes parked time, with a 30-second tool default and 1-120 second range.
  Shutdown can add five seconds before reporting `canceling`. Capture permits 1000
  output/event items or 1 MiB, with at most ten serialization-error entries. It does not
  cap the whole encoded response. Existing MCP fixtures verify cooperative parked
  cancellation, oversized capture, and absence of persistent test records.
- [Socket identity and subscriptions](../pkg/mq/handler.go) were resolved only when
  connecting and subscribing. Open sockets are now revalidated every 30 seconds and after
  API/MCP writes, plugin gate removal, and render settlement; expired sessions are told to
  re-authenticate and settled render sockets close. A live check against the development
  server withdrew an anonymous public-dashboard subscription within 1.5 s of the dashboard
  going private, and a logged-out socket received `auth_expired` and `revoked` and then
  re-authenticated over the same connection.

Verification passed `make dev TARGET=server`, `make dev TARGET=agent`, full `make test`,
and `KARAKURI_AGENTS_LISTEN=127.0.0.1:0 make test-integration`. The agent build was already
current; unchanged Go packages reused cached tests. Frontend checks reported zero errors
and warnings. Fresh MCP checks canceled a ten-second parked delay under a one-second
deadline in 1.01 seconds and omitted output larger than 1 MiB with `truncated: true`.
The updated embedded flow guide was served. No test flow was created. The Spec Kit
helper resolved feature 003 without changing the branch or local selection. The restarted
user unit was active; `/`, `/health`, `/api/health/ready`, and `/metrics` returned 200.
No runtime source, schema, agent identity, or encryption format changed.

## Host boundary and evidence review

The review of `1c0be6f` checked host cancellation, log tailing, and the cited revocation
fixtures against the unchanged runtime source.

- Unix command/script cancellation signals the launched process group with SIGKILL;
  descendants that leave that group escape it. Windows attempts `taskkill /T /F`, then
  kills the parent. If the parent kill succeeds after tree killing fails, the tree error
  is hidden and descendants can remain. Spec 009 now states the platform mechanism and
  limits. The Windows fallback remains a source-only cancellation reliability gap. Later fixed: Windows commands run in a job object, Unix cancellation also
  signals descendants that left the group, and cleanup failures reach the node error.
- Tail checkpoints replay ordinary partial lines. Oversized lines emit a clipped prefix
  and discard their remainder through the newline; the guide previously omitted that
  exception. Existing tail fixtures cover ordinary replay, replacement-file restart,
  clipping, and one-file batch limits.
- Temporary real-filesystem checks run with `make test TARGET=agent` confirmed two
  polling/rotation boundaries. Rewriting a seven-byte consumed file with a longer line
  on the same inode skipped its seven-byte prefix. With a two-line drain limit, a rotated
  five-line old file and three-line replacement returned four lines in one poll and
  discarded the old fifth line. The checks were removed after verification. Spec 009
  and the guide record data loss limits and the per-poll batching implementation gap. Both losses and the batching gap were fixed later; the tail now fingerprints
  the bytes before its offset and drains a rotated file within one per-poll budget.
- The prior evidence table wrongly labeled the enroll/connect fixture as revocation
  coverage. The CA fixture checks revoked-serial validation, and the lifecycle fixture
  protects a replacement session from an older disconnect. Neither proves an operator
  revocation through the live hub/API followed by reconnect refusal. That acceptance
  path is now explicitly source only.

`make dev TARGET=server`, `make dev TARGET=agent`, `make test TARGET=server`, and
`make test TARGET=agent` passed. The agent build was already current; unchanged Go
packages reused cached tests. Agent tests passed both with the temporary boundary checks
and after their removal. Fresh MCP reads returned status and capability lists for ten
existing agents, six connected at the time of inspection. No host command was executed,
test flow created, or enrolled identity changed. The Spec Kit helper resolved feature
009 without changing the branch or local selection. The restarted user service was
active; `/`, `/health`, `/api/health/ready`, and `/metrics` returned 200.

## Memory consistency review

The review of `108b913` checked ingestion failure and embedding completion against the
unchanged runtime. The memory plan's transactional replacement claim was incorrect.

- Ingestion deletes a source before inserting each replacement chunk independently.
  A temporary check used a real SQLite insert trigger to reject the second of two chunks.
  The node failed, the old source was gone, and the first replacement remained in the
  table. Replacement is now one transaction under a per-source lock, and
  `TestReplaceSourceIsAllOrNothing` repeats that trigger check.
- Temporary checks used a real HTTP embeddings peer and the AI driver, delaying a
  response until after a memory text edit or store model change. In both cases, the
  old vector was persisted and marked complete after invalidation; a fresh store load
  treated it as embedded. Results are now stored only while the memory and store still
  match, and `TestLateEmbeddingDoesNotOverwriteAnEdit` repeats the delayed-response check.
- Chunk size and overlap count bytes, defaulting to 1200 and 150, with overlap clamped
  to half the size and omitted when it cannot fit. The field help incorrectly says
  characters. A safe temporary check confirmed `splitPoint("éé", 1)` returns zero.
  Source inspection shows the chunk loop then cannot advance or observe cancellation.
  The splitter now always advances, checks cancellation, and the help says bytes;
  `TestChunkTextAlwaysAdvances` runs the former hang input.

The isolated checks passed through `make test TARGET=server` and were removed afterward.
`make dev TARGET=server`, `make dev TARGET=agent`, and server/agent test targets passed
with the final documentation and original test suite. The agent artifact was already
current; unchanged Go packages reused cached results. Fresh MCP reads confirmed the
updated AI guide and the still-incorrect chunk-size field help. The Spec Kit helper
resolved feature 011 without changing the branch or local selection. The restarted
user service was active, and `/`, `/health`, `/api/health/ready`, and `/metrics` returned
200. No development memory store, test flow, enrolled identity, or runtime source changed.

## Limits and remaining checks

The proxy/allowlist interaction was a security-relevant limit: `server.trust_proxy` let any
direct client supply the address the webhook IP check used. Forwarding headers are now
honoured only from peers in `server.trusted_proxies`, read right to left past trusted hops;
`TestTrustedRealIP` pins the rules.

This audit does not certify fresh browser behavior, every host OS, real HA/MQTT/provider
deployments, real PostgreSQL/MySQL, installation upgrades, complete key rotation, or
queue-pressure paths. Agent process-tree termination and abandoned syscall behavior need
live host evidence. Render conversion fixtures do not prove Chromium screenshots, layout
settling, or schedule cleanup. Success criteria remain acceptance targets where those
checks have not run; passing the test groups does not turn source-only claims into tests.

The specs describe the current design and its limits. They do not promise exactly-once
external actions, crash-atomic filesystem replacement, or lossless asynchronous history.
