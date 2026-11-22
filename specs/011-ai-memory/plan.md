# Design plan: AI processing, tools, and memory

**Feature**: `011-ai-memory` | **Date**: 2026-10-04, revised 2026-11-08 | **Spec**: [spec.md](spec.md)
**Status**: Implemented baseline

## Summary

Compose model-backed processing and bounded tool use in typed flows, preserve
conversation continuation, and recall scoped persistent knowledge.

This plan records the design supporting the baseline requirements. It is not
a pending implementation queue. Requirement IDs are local to this feature.

The 2026-11-08 revision adds automatic context compaction (user story 5, FR-021
through FR-029). Supporting material:
[research](research.md), [data model](data-model.md),
[interfaces](contracts/interfaces.md), and [quickstart](quickstart.md).

## Technical context and design

`pkg/nodes/ainodes` defines pure model/tool/memory handles and executing
agent, chain, guardrail, extraction, classification, summarization, and ingestion
nodes. `pkg/integrations/ai` implements supported provider wire formats and
continuation state. `pkg/memories` owns storage, embedding workers, and hybrid
recall. Connection registration supplies provider metadata, not callbacks to
external plugin processes for every inference request.

Tool branches use engine call groups and return wiring; argument values are
resolved per invocation. Provider-specific signatures/reasoning data remain
opaque and return only to their matching wire. Structured-output validation
occurs after inference regardless of native provider schema support.

A plugin provider definition names one of the three wires (chat-completions,
Anthropic messages, Gemini generateContent) and a default address, so most new
providers are data. The ChatGPT-account mode of the OpenAI provider uses the
Codex backend's OAuth grant; the refresh token is stored in the encrypted
connection settings and a rotated token is written back to the stored connection.
Readable reasoning on the chat-completions wire comes from `reasoning`,
`reasoning_content`, or the text in OpenRouter's `reasoning_details`; that array is
rebuilt from stream fragments and kept as opaque state. OpenAI receives a stable
`prompt_cache_key` for HA Assist and memory conversations; Anthropic requests
automatic caching. Hermes' Response output reads the session messages named by the
completion only when the output is wired.

## State and data model

Memory stores share identity across flows. Sessions own transcript windows
and session-scoped facts. Facts/chunks persist before asynchronous embedding,
so keyword recall remains available. Store settings control transcript/fact
caps, decay, importance, embedding connection, and model. Source ingestion
replaces a source's chunks with `Store.ReplaceSource`: `models.ReplaceMemoriesBySource`
deletes and inserts in one transaction, and a per-store-and-source mutex serializes
replacements; `TestReplaceSourceIsAllOrNothing` pins it with a rejecting insert trigger. Response envelopes retain
session, cache, provider replay, and response data; image replay entries keep counts
rather than duplicating image bytes.
Recall filters session-only entries, while authorized administrative browsing may inspect
all sessions. Query embedding times out after ten seconds and falls back to keywords.
The 4096-item background queue processes batches of 16; failed or dropped work waits
for a later load/backfill rather than an immediate retry.

Text edits and embedding-configuration changes invalidate cached vectors and queue
replacement work. The worker snapshots each memory's text and tags with the request. `storeVector` keeps a
result only if the memory and its store's connection and model still match, holding the
store lock across the check and the write, and `models.UpdateMemoryEmbeddingIf` makes the
table write conditional on the row's text and tags; `TestLateEmbeddingDoesNotOverwriteAnEdit`
pins it.

## Contracts and failure handling

Permanent model/tools/memory/image fields use ai-model, ai-tool, ai-memory,
and image value types. Tool arguments use `arg:<id>` value outputs and legal
call/return cycles. Final response port ID `transcript` carries object or
ai-stream according to configuration. Streams end with `response.completed`.
JSON validation supports types, required, properties, items, and enum. Document
parser formats are pdf/docx/odt; format selection reads declared MIME/path
metadata and refuses content sniffing. Summarization caps calls at 64 and
reduction rounds at eight.
Gemini and Anthropic give positive thinking budgets precedence over effort. OpenAI
chat-completions and ChatGPT-account requests send effort only. Anthropic omits
temperature for reasoning requests and models that do not support it.

Ingestion defaults to a 1200-byte chunk and 150-byte overlap, clamping overlap to zero
through half the chunk size. Word and UTF-8 boundaries determine cuts; overlap is omitted
when it would overflow the next chunk. `splitPoint` never returns zero, keeping a character
wider than the budget whole, the overlap never rewinds a split to its start, and
`chunkText` checks cancellation between splits; `TestChunkTextAlwaysAdvances` pins both.

## Node chat testing

The canvas node title sends a chat request to EditorShell, which owns draft
saving and local subgraph selection. A shared floating window supplies movement
and resizing for the authoring assistant and node chat. Node chat contains no
provider/model picker or execution-permission toggle.

`POST /api/flows/{id}/nodes/{node_id}/chat` requires flow-write permission and
streams NDJSON. The service selects the draft node, removes its incoming message
and outgoing message edges, and retains pulled values and tool branches through
ExecutionGraph. Strict validation uses that executable graph. A temporary engine
copies the live connection and subflow resolvers but has fresh variables/stores,
no history sink, and no document publisher. The target node runs once without
retry; tools retain their configured behavior. Output capture observes Response
without adding a graph edge. No schema or encryption changes are required.

The AI chat adapter renders the configured request against the typed In payload,
appends it to the preceding response envelope, and executes the registered AI
node in incoming-transcript mode. System instructions are rendered against the
original message. Only authored text is interpolated; returned transcript text
and provider state stay data. Streaming is enabled for the temporary request.
The client keeps completed response envelopes in memory, resets replay after
failure/cancellation, and supports New chat and Stop. Disconnect and a five-minute
request deadline cancel the temporary engine. Wired tools and memory may write to
external systems or the configured memory store.

Verification covers the real REST middleware, SQLite draft, engine scheduling,
provider wire requests and tool call/return routing. Browser checks cover both
title buttons, background interaction, resize, transcript/replay, Stop, errors,
and the console. No live provider calls are needed for deterministic checks.

## Automatic context compaction

`pkg/integrations/ai` owns compaction so the agent node, node chat, and the editor
assistant (feature 012) share one implementation. Three files hold it:

- `overflow.go`: `IsContextOverflow(err)` matches the provider phrasing list and its
  exclusions; `overflowLimit(err)` extracts a window from llama.cpp, OpenAI, and
  Anthropic overflow text.
- `window.go`: `Driver.ContextWindow(ctx, model)` returns the connection's
  `context_window`, else the smaller of a reported and a learned window.
  `Driver.LearnContextWindow` records a learned limit. Wires that can report a
  window implement an optional `contextWindow(ctx, model)`: chat-completions reads
  `context_length` or `max_model_len` from `/models`, then probes llama-swap's
  `/upstream/{model}/props` and llama.cpp's `/props` under the base URL's root;
  Gemini reads `inputTokenLimit`. Results, including misses, are cached per model for
  the catalog TTL. `finishChat` sets `ChatResponse.PromptTokens` from usage, adding
  cache reads and writes for the Anthropic wire, whose input count excludes them.
- `compact.go`: `Compactor`, created per loop with the connection, model, and the
  handle's window, buffer, and switch. `Compactor.Send(ctx, &req, send, notify)`
  compacts `req.Messages` when the estimate passes the threshold, calls `send`, and on
  a recognized overflow learns the limit, compacts with reason `overflow`, and calls
  `send` once more. It records the response's measurement for the next round and
  reports each compaction to `notify`. Callers continue from `req.Messages`.

Summarizing works in steps: split at the kept tail (newest messages within the
keep budget, moved back to a user message, latest exchange only when everything
fits, nothing for a single exchange), render the tail as text, and request a summary
of everything before it. The request reuses the loop's system prompt, tools, model,
and cache key and appends the summary prompt as a user message, so prompt caches still
match; when that would not fit the window it becomes a text transcript shortened by
the four levels. A reply missing every template heading gets the reminder, then fails.
The result replaces history with one checkpoint user message. The update prompt is
used when the history already starts with a checkpoint, whose `<summary>` it carries
forward. A summary request's output is capped at 8,000 tokens or the room left in the
window. Compaction is skipped when history beyond the checkpoint is under the minimum
removable size, when the connection has no tool support (Hermes), and when the
loop continues a provider-managed session.

The AI Agent wraps its model call in `Compactor.Send`, adds compaction usage, emits
`context.compacted` on its stream, and records compactions in output metadata and the
response envelope. Its envelope's transcript starts with the checkpoint after a
compaction, so node chat and replayed conversations continue compacted. Memory still
saves the raw prompt and answer. The model node gains `context_window`,
`auto_compaction`, and `compaction_buffer`; the connection kind gains
`context_window`. Structured-output repair calls and the basic chain are single
requests and are not compacted.

Failure handling: an overflow that compaction cannot fix, a second overflow, or a
failed summary returns the provider's error, so the node fails onto its error branch
and its retry policy applies. A threshold compaction failure returns
`context compaction failed: ...`. Compaction never runs twice before one request.

## Constitution check

- Keep the authorization and failure boundaries described above explicit.
- Route authoring through shared operations and preserve draft/deployment separation
  where applicable.
- This baseline adds no schema, wire, identity, or encryption-format migration. Future
  changes must assess the affected persistent state before implementation.
- Compaction adds optional connection and model fields and an envelope key. Existing
  connections, encrypted secrets, flows, and stored envelopes stay readable and keep
  their behavior apart from compaction now being on by default.
- Compaction failures return provider errors to the node's existing failure routing
  and retry policy (principle I); summary requests are visible in usage, metadata,
  and transcripts rather than hidden side effects (principle V).
- Use the component checks below and the service handoff checks in `AGENTS.md`.

## Verification

Run `make dev TARGET=server` and `make test TARGET=server`. Compaction tests use an
`httptest` chat-completions fixture that rejects requests over its window with
llama.cpp's overflow text, covering SC-007 through SC-009 and feature 012 SC-006;
overflow phrasing and window-detection tests use captured provider responses.
[quickstart.md](quickstart.md) lists the live checks. Provider wire,
reasoning/schema, agent, tool-call, memory, ingestion, and guardrail tests check
contracts without relying on unpredictable model text. Live provider checks require a
configured connection and should verify bounded use plus intact continuation. Canvas
socket changes also require frontend and browser checks.

These are the acceptance checks for changes to this feature. They do not claim
that every live scenario ran while the baseline documents were written.

## Risks and limits

Provider/model capabilities can change; the implementation returns provider errors
rather than promising all models support every setting. Tool side effects are real.
Sanitizers/guardrails do not establish a general security boundary for arbitrary model
output.

New behavior needs reviewed requirements and an update to this plan.

## References

- [Server and agent audit](../server-agent-audit.md)

- [System architecture](../architecture.md)
- [docs/ai.md](../../docs/ai.md)
- [pkg/nodes/ainodes](../../pkg/nodes/ainodes)
- [pkg/integrations/ai](../../pkg/integrations/ai)
- [pkg/memories](../../pkg/memories)
- [pkg/engine/toolcalls.go](../../pkg/engine/toolcalls.go)
- [pkg/flow/toolports.go](../../pkg/flow/toolports.go)
