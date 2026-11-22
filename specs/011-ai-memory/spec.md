# Feature specification: AI processing, tools, and memory

**Feature**: `011-ai-memory`
**Created**: 2026-10-04
**Status**: Implemented baseline
**Input**: Compose model-backed processing and bounded tool use in typed flows, preserve
conversation continuation, and recall scoped persistent knowledge. Compact long AI
conversations automatically, so a turn or chat that outgrows the
model's context window keeps working (2026-11-08).

## User scenarios and testing

### User story 1: Run a bounded model workflow (Priority: P1)

An author wires a reusable model into an agent or basic chain and gets text, structured
output, or a stream.

**Why this priority**: Model use must obey graph types and execution limits.

**Independent test**: Run an agent against a deterministic provider fixture with tool
and schema-repair turns.

**Acceptance scenarios**:

1. **Given** a model and tool set are wired into an agent, **When** the model calls a
   tool, **Then** the result returns to the model and the final answer is emitted with
   usage and trace metadata.
2. **Given** a first structured reply is invalid, **When** the repair call also fails
   validation, **Then** the node fails onto its error path.

### User story 2: Expose a controlled tool branch (Priority: P1)

An author declares tool arguments and wires a branch to return one observation.

**Why this priority**: Tools are explicit graph capabilities, not arbitrary host
authority.

**Independent test**: Invoke a tool branch with call-scoped parameters and a
deliberately failing branch.

**Acceptance scenarios**:

1. **Given** a declared tool with call and return wiring, **When** two calls supply
   different arguments, **Then** each branch resolves its own call arguments.
2. **Given** an unhandled failure inside that branch, **When** the waiting model call
   receives it, **Then** the error is an observation instead of an unrelated whole-run
   failure.

### User story 3: Retain conversation and reference material (Priority: P2)

An author connects memory, remembers scoped facts, and ingests replaceable document
sources.

**Why this priority**: Useful continuation needs clear session boundaries and
replacement semantics.

**Independent test**: Store session facts, ingest a source twice, and recall with and
without embeddings.

**Acceptance scenarios**:

1. **Given** a session-scoped memory, **When** another session recalls the same store,
   **Then** that memory is not returned.
2. **Given** a source has existing document chunks, **When** the source is re-ingested,
   **Then** its chunks are replaced; an empty document removes them.
3. **Given** a two-chunk replacement whose second database insert is rejected,
   **When** ingestion fails, **Then** the old source is already removed and the first
   replacement chunk remains; the operation does not roll those writes back.

### User story 4: Test an AI node in chat (Priority: P2)

An author clicks Chat in an AI Agent or AI Basic Chain canvas title and tests
its configured request with a conversation.

**Independent test**: Send two turns through the chat API against a provider
fixture and a wired tool branch, then exercise the canvas button in the browser.

**Acceptance scenarios**:

1. **Given** a wired AI node, **When** Chat is clicked in its title, **Then** a
   movable, resizable window opens with a transcript and composer, without
   model selection or permission controls, and the canvas remains usable.
2. **Given** a configured prompt or transcript, **When** a message is sent,
   **Then** it supplies the In payload for authored expressions, previous turns
   are replayed, and the configured system instructions remain authoritative.
3. **Given** an agent with wired tools and outgoing response branches, **When**
   the model calls a tool, **Then** the tool branch runs and its result appears
   in chat, while ordinary response and error branches remain unexecuted.
4. **Given** a running turn, **When** Stop is clicked or the client disconnects,
   **Then** execution is canceled and an incomplete turn is not reused for replay.

### User story 5: Keep long conversations inside the context window (Priority: P1)

A model's context window fills with tool results and earlier turns. Karakuri summarizes
the older part and keeps working instead of failing the request.

**Why this priority**: On a 32,768-token local model, the editor assistant failed on
the first message of a fresh chat (2026-11-08). Its prompt used 18,376 tokens before any
tool ran, and ten rounds of ordinary tool results pushed the eleventh request to 33,147
tokens. Any agent loop with tools can reach the same failure.

**Independent test**: Drive an agent loop against a provider fixture with a small
declared window and large tool results, then against a fixture that rejects an
oversized request with a context-overflow error.

**Acceptance scenarios**:

1. **Given** a model with a known window and an agent loop whose next request would
   exceed the compaction threshold, **When** the loop is about to send it, **Then**
   older history is replaced by a structured summary, the newest exchange is kept, and
   the request proceeds inside the window.
2. **Given** a provider rejects a request because it exceeds the context window,
   **When** the error is recognized as a context overflow, **Then** history is compacted
   and the request is sent once more; a second overflow fails the node onto its error
   path with the provider's error.
3. **Given** a conversation that was already compacted, **When** it outgrows the window
   again, **Then** the new summary updates the previous one rather than nesting it.
4. **Given** a compaction occurred, **When** the author inspects the result, **Then**
   the transcript and node chat show that context was compacted, and usage includes the
   summary request.
5. **Given** a model handle with automatic compaction turned off, **When** a request
   exceeds the window, **Then** no summary request is made and the provider error is
   returned as before.
6. **Given** a provider that reports the model's window, **When** a model handle has no
   window set, **Then** the reported window is used; a window set on the model handle
   or connection takes precedence.

### Edge cases

- Models from providers that do not accept Karakuri tools reject tool/memory wires,
  while tool-free chains can still use them.
- Incoming transcript system entries are ignored and incoming text is not evaluated as
  expression templates.
- Image/document formats must be declared; undeclared bytes are not guessed, and scanned
  PDFs need OCR outside the parser.
- Embedding unavailability retains keyword recall rather than making every memory
  inaccessible.
- Source replacement is one transaction: a failure leaves the previous chunks whole,
  and ingests of the same source are serialized.
- Chunk sizes and overlap are byte budgets. A character wider than the size becomes a
  chunk of its own, and a cancelled run stops between splits.
- The system prompt and tool definitions are never summarized. When they alone exceed
  the compaction threshold, compaction removes what history it can, does not repeat,
  and the provider decides whether the request fits.
- A single in-progress exchange with nothing older is summarized whole; the next
  request continues from the summary's next steps.
- A summary reply that ignores the required headings gets one reminder; a second miss
  fails the compaction and the original request error is returned.
- An unknown window never triggers compaction before a request; only a recognized
  overflow error does. Rate-limit and quota errors are never treated as overflow.
- Opaque reasoning replay state on summarized turns is discarded with those turns.
- A provider that keeps the conversation itself under a session ID, such as Hermes, is
  never compacted locally; its own server bounds that history.

## Requirements

### Functional requirements

- **FR-001**: The system MUST provide AI provider connections and pure model handles
  with provider/model identity, sampling, reasoning settings, and declared tool
  capability.
- **FR-002**: The system MUST accept exactly one model value wire, multi tool/image
  wires, and an optional memory handle through typed permanent sockets.
- **FR-003**: The system MUST support configured prompt/transcript templates and
  incoming transcript mode while keeping configured system instructions authoritative
  and incoming data uninterpreted.
- **FR-004**: The system MUST bound agent tool iterations and return tool failures as
  observations; provider/model errors fail the node and participate in its retry policy.
- **FR-005**: The system MUST validate JSON replies against the supported schema subset
  on the server and allow one repair call before failure.
- **FR-006**: The system MUST emit final payload plus model, accumulated usage, and
  tool-call trace metadata and produce a full response envelope when its output is
  wired.
- **FR-007**: The system MUST preserve session IDs, cache keys, provider responses,
  opaque reasoning replay state, and readable reasoning summaries across continuation.
- **FR-008**: The system MUST expose ai-stream output when streaming is enabled, with
  ordered deltas/progress and a completed response event carrying the final envelope.
- **FR-009**: The system MUST define graph tools with unique names, declared argument
  schemas, call/return message wiring, and call-scoped argument value outputs.
- **FR-010**: The system MUST provide bounded MCP call/tool access using configured MCP
  connections and the flow's explicitly wired capabilities.
- **FR-011**: The system MUST persist memory stores with scoped conversations/facts,
  configurable transcript caps, importance, decay, TTL, and optional embeddings.
- **FR-012**: The system MUST use keyword recall immediately and vector recall after
  asynchronous embedding, backfill existing memories when embedding configuration
  changes, and isolate session-only memories during recall. Administrative browsing
  without a session filter MAY inspect all sessions under memory-read permission.
  Vectors MUST correspond to the current memory text, embedding connection, and model;
  changing text or embedding configuration MUST invalidate prior vectors and queue
  eligible memories for replacement embedding.
- **FR-013**: The system MUST allow remembered facts, raw recall, optional diversity
  selection, and optional model remember/recall tools with conversation-save control.
- **FR-014**: The system MUST ingest source-labeled document chunks with overlap and
  replace-by-source semantics, including removing a source on empty input.
  Chunk size and overlap MUST use byte budgets, defaulting to 1200 and 150; overlap
  MUST be clamped to zero through half the chunk size and may be omitted when it will
  not fit. Replacement MUST report insertion failures. The current sequence deletes
  old source rows before individual inserts and provides no atomic replacement or rollback.
- **FR-015**: The system MUST provide deterministic sanitize/guardrail checks and
  explicitly model-backed guardrail checks only when their model socket is required.
- **FR-016**: The system MUST provide extraction, classification with an Other route,
  and bounded sequential summarization rather than unbounded model work.
- **FR-017**: The system MUST require declared image/document metadata, preserve image
  MIME with bytes on value edges, and reject unsupported or undeclared formats.
- **FR-018**: The system MUST retain provider-specific continuation and opaque reasoning
  replay data. Reasoning effort MUST map to the provider's request field; positive
  thinking budgets MUST take precedence over effort for Gemini and Anthropic, while
  chat-completions and ChatGPT-account requests carry effort only. Anthropic requests
  MUST omit temperature while reasoning is enabled or the selected model excludes it.
- **FR-019**: Query embedding MUST have a ten-second budget and fall back to keyword
  scoring on failure. Background embedding MUST use a bounded 4096-item queue and
  batches of at most 16; dropped/failed work MUST remain keyword-recallable and become
  eligible for backfill on the next store load, without promising immediate retries.

- **FR-020**: AI Agent and AI Basic Chain canvas titles MUST offer Chat to flow
  editors. Chat MUST use the saved draft's value wiring and node configuration,
  save pending edits before each turn, replay completed conversation envelopes,
  and display replies, tool observations, and errors without model or permission
  selectors. Each turn MUST run in temporary engine state without history or
  ordinary output/error branches, use no automatic node retry, and cancel on
  disconnect or after five minutes. Wired tools and configured memory writes
  remain real. Local subgraphs MUST be addressable by their document key.

- **FR-021**: Every model loop (AI Agent, node chat, and the editor assistant in
  feature [012](../012-mcp-editor-assistant/spec.md)) MUST check the prompt size before
  each model request, including requests between tool rounds of one turn. The size
  MUST be the latest response's reported input tokens, cached tokens included, plus an
  estimate of four characters per token for everything added since.
- **FR-022**: The system MUST compact when the size exceeds the threshold: the model's
  window minus a buffer. The default buffer MUST keep 15% of the window free, raised to
  16,000 tokens or a quarter of the window, whichever is smaller. A model handle MAY set
  the buffer as a percentage from 1% to 99% or as a token count; a buffer that leaves no
  room is ignored. A compaction MUST run only when the history it would remove is at
  least 1,000 tokens or 5% of the window, whichever is larger, so a fixed prompt near
  the threshold does not cause a summary request every round.
- **FR-023**: The system MUST recognize provider context-overflow errors, including
  llama.cpp's "exceeds the available context size", "maximum context length is N
  tokens", and "prompt is too long", while excluding rate-limit, throttling, and output
  token limit errors. On an overflow it MUST compact and resend the request once.
- **FR-024**: Compaction MUST make one request to the same model asking for a summary
  in a fixed Markdown template with Objective, Requirements, Decisions, Work State
  (Completed, Active, Blocked), Next Move, Relevant Items, and Important Context
  sections. The instructions MUST ask for terse bullets, exact identifiers, error text,
  entity and document IDs, only unresolved requests, no tool calls, and no mention of
  compaction. A reply without the template headings MUST get one reminder request; a
  second miss MUST fail the compaction.
- **FR-025**: Compaction MUST keep the newest conversation verbatim up to 15,000 tokens
  or one quarter of the window, whichever is smaller, and always the newest exchange.
  Later requests MUST open with one user message holding the summary and the kept text
  in a `<conversation-checkpoint>` wrapper that marks it as historical context, not new
  instructions. A later compaction MUST update the existing summary, with newer history
  taking precedence.
- **FR-026**: When the summary request itself would not fit, history MUST be sent as a
  text transcript and shortened oldest exchange first, one level at a time: tool outputs
  clipped to their first and last 1,000 characters; tool outputs omitted and inputs
  clipped; user text only; exchange omitted with a count of omitted exchanges. The newest
  exchange MUST never be omitted. After each overflow rejection of the summary request,
  the next attempt MUST aim at 85%, 70%, 50%, then 35% of the first rejected size, then
  fail.
- **FR-027**: The model window MUST resolve in this order: the model handle's Context
  window, the connection's Context window, then the smaller of the window the provider
  reports for that model and a window learned from an earlier overflow error naming the
  limit. Providers that report windows include OpenRouter, vLLM, llama.cpp directly or
  through llama-swap, and Gemini. Ollama is not detected, because it truncates silently
  to its configured context instead of reporting an overflow; its connection needs a
  Context window. Without a window, only an overflow error triggers compaction.
- **FR-028**: Model handles MUST offer automatic compaction, on by default. When it is
  off, no summary request is made, including after an overflow error.
- **FR-029**: A compaction MUST be recorded in the agent's output metadata with its
  reason (threshold or overflow) and estimated sizes before and after, and in the
  transcript output with the summary text. Node chat MUST
  show a "Context compacted" marker in the conversation. Accumulated usage MUST include
  the summary requests. Response envelopes MUST carry the checkpoint so continued turns
  and replayed conversations stay compacted.

### Key entities

- Model/tool/memory/image handle: typed flow value selecting an explicit capability or
  data source.
- Response envelope and stream: replayable turns, provider state, usage, and final
  result.
- Memory store, fact, session, and document chunk: persistent scoped knowledge with
  source and ranking metadata.
- Context window: the largest prompt a model accepts, set on a model handle or
  connection, reported by the provider, or learned from an overflow error.
- Conversation checkpoint: a structured summary of earlier history plus the newest
  kept text, replacing that history in later requests and continuation envelopes.

## Success criteria

- **SC-001**: The fixture completes a tool loop within its iteration bound and returns a
  failed tool as a recoverable observation.
- **SC-002**: Invalid structured output gets at most one repair call and cannot pass
  server schema validation unchanged.
- **SC-003**: Cross-session recall returns zero session-only facts from another session.
- **SC-004**: Re-ingestion replaces source chunks and a store without embeddings still
  recalls keyword matches.
- **SC-005**: Streaming completion retains the same continuation envelope as a
  non-streamed response.

- **SC-006**: A two-turn chat fixture retains provider replay state, executes a
  wired tool branch, and leaves the draft, deployment, and last run unchanged.
- **SC-007**: Replaying the 2026-11-08 failure shape (an 18,000-token fixed prompt,
  a 32,768-token window, and eleven rounds of multi-kilobyte tool results) completes
  with at least one compaction and no request larger than the window.
- **SC-008**: A fixture that rejects its first oversized request with a context-overflow
  error succeeds on the single resend after compaction; a fixture that rejects twice
  fails with the provider's error after exactly one summary request.
- **SC-009**: With automatic compaction off, the same fixtures make zero summary
  requests.

## Assumptions and scope

Provider/model capabilities can change; the implementation returns provider errors
rather than promising all models support every setting. Tool side effects are real.
Sanitizers/guardrails do not establish a general security boundary for arbitrary model
output.

Compaction does not include background precompute, native provider compaction, or a
separate compaction model. The kept tail is capped at a quarter of the window instead of a fixed
15,000 tokens, and the default buffer's 16,000-token floor is capped at a quarter of the
window, because small local windows cannot hold a fixed prompt, a summary, and those
margins together: at 32,768 tokens, a fixed-floor threshold of 16,768 is below the editor
assistant's 18,376-token fixed prompt. Summaries cost one extra model request, two when the
reminder is needed, and quality depends on the model. Memory conversations store their
raw turns under their existing transcript caps; recalled history that outgrows the window
is compacted per request.

The background embedder stores a provider result only while the memory's text and tags
and the store's connection and model still match what produced it, so a response
finishing after an edit is discarded rather than marked current.

Ingestion is not serialized by source, so concurrent replacements can mix chunks.
Its field help calls chunk budgets characters, but the splitter counts bytes. For a
positive budget smaller than a UTF-8 character, backing off to a valid split can produce
zero progress. The loop does not check cancellation, so that input can keep the run
unsettled. This needs input validation or a progress fix; it is not intended behavior.

## References

- [Design plan](plan.md)
- [System architecture](../architecture.md)
- [docs/ai.md](../../docs/ai.md)
- [pkg/nodes/ainodes](../../pkg/nodes/ainodes)
- [pkg/integrations/ai](../../pkg/integrations/ai)
- [pkg/memories](../../pkg/memories)
- [pkg/engine/toolcalls.go](../../pkg/engine/toolcalls.go)
- [pkg/flow/toolports.go](../../pkg/flow/toolports.go)
