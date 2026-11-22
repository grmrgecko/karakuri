# Tasks: AI node chat testing

- [x] T001 Add an isolated graph preview runner and AI chat input adapter.
- [x] T002 Add the authorized streaming chat endpoint and service operation.
- [x] T003 Add Chat to both AI canvas titles and a floating chat window.
- [x] T004 Verify provider replay, tool routing, authorization and cancellation at the API boundary.
- [x] T005 Update the AI guide, run component builds/tests, verify the browser and service, and commit.

## Verification

- `make dev TARGET=server` and `make dev TARGET=frontend` passed.
- `make test TARGET=server` and `make test TARGET=frontend` passed.
- REST fixtures cover Prompt, Transcript and Input modes; a local definition;
  two-turn replay with uninterpreted provider text; graph tool call/return;
  unchanged drafts/history; authentication; cancellation; and no node retry.
- Browser checks cover both canvas title buttons, a nonmodal window, movement
  and resizing, background node selection, live Stream socket derivation,
  chat replay, tool observations, Stop, errors, New chat, and the existing
  authoring assistant. The final console is clear. Browser responses were
  temporary fixtures; they were removed without saving development flows.
- The development service was restarted, is active, and returns HTTP 200.

# Tasks: Automatic context compaction

**Input**: [spec.md](spec.md) user story 5 (FR-021 through FR-029, SC-007 through
SC-009), feature [012](../012-mcp-editor-assistant/spec.md) FR-026 and SC-006,
[plan.md](plan.md), [research.md](research.md), [data-model.md](data-model.md),
[contracts/interfaces.md](contracts/interfaces.md), [quickstart.md](quickstart.md).
Tests are part of the acceptance contract (SC-007 through SC-009, 012 SC-006).

## Phase 1: Foundational (shared AI integration)

- [X] T006 [P] Implement the overflow phrasing list and exclusions as `IsContextOverflow(err error) bool`, and add `overflowLimit(err) int` reading llama.cpp `available context size (N tokens)`, OpenAI `maximum context length is N tokens`, and Anthropic `prompt is too long: X tokens > N maximum`, in pkg/integrations/ai/overflow.go
- [X] T007 [P] Add `PromptTokens int` to `ChatResponse` in pkg/integrations/ai/types.go and set it in `Driver.finishChat` in pkg/integrations/ai/ai.go: `InputTokens`, plus `CachedInputTokens + CacheWriteInputTokens` for the Anthropic wire
- [X] T008 Add `context_window` ("integer, optional ... Zero or absent means detect") to `Config` and the AI Provider kind's fields in pkg/integrations/ai/ai.go and pkg/integrations/ai/providers.go, with help naming Ollama as needing it
- [X] T009 Add `Driver.ContextWindow(ctx, model) int` and `Driver.LearnContextWindow(model, tokens)` with per-model reported/learned caches (reported cached for the five-minute catalog TTL, misses included) returning the connection value, else the smaller of reported and learned, in pkg/integrations/ai/window.go
- [X] T010 Implement the optional wire `contextWindow(ctx, model) (int, error)`: chat-completions reads `data[].context_length` or `max_model_len` from `/models`, then probes `<root>/upstream/<model>/props` and `<root>/props` for `default_generation_settings.n_ctx` with the connection's authorization; Gemini reads `models[].inputTokenLimit`; in pkg/integrations/ai/window.go
- [X] T011 Implement `Compactor` in pkg/integrations/ai/compact.go per data-model.md: settings (window, buffer `"20%"` 1-99% or token count, disabled), measurement, estimate (chars/4, 1,500 tokens per image), threshold `window - max(15%, min(16000, 25%))`, keep `min(15000, 25%)`, minimum removable `max(1000, 5%)`, tail split at a user message, checkpoint render/parse, summary and update prompts with the fixed template and rules, one reminder, text-transcript fallback with the four shortening levels and `[N older exchanges omitted]`, overflow retargeting at 85/70/50/35%, summary output cap of 8,000 tokens or the room left, and `Send(ctx, *ChatRequest, send, notify)` with a single overflow resend
- [X] T012 [P] Test overflow phrasing and limit extraction with captured provider error strings, including the 2026-11-08 llama.cpp error and rate-limit exclusions, in pkg/integrations/ai/overflow_test.go
- [X] T013 [P] Test window detection against `httptest` fixtures shaped like OpenRouter `/models`, llama-swap `/upstream/{model}/props`, and Gemini `/v1beta/models` in pkg/integrations/ai/window_test.go

**Checkpoint**: the shared compactor builds and its wire-facing parts are tested.

## Phase 2: User Story 5 - Keep long conversations inside the context window (Priority: P1)

**Goal**: agent loops, node chat, and Kuri compact instead of overflowing.

**Independent test**: an `httptest` chat-completions fixture with a 32,768-token
window that rejects larger requests with llama.cpp's overflow text.

- [X] T014 [US5] Test the compactor against the overflow fixture in pkg/integrations/ai/compact_test.go: the 2026-11-08 shape (18,000-token fixed prompt, eleven rounds of multi-kilobyte tool results) completes with at least one compaction and no oversized request (SC-007); a first-request overflow succeeds on one resend; a double rejection fails with the provider error after one summary request (SC-008); disabled makes zero summary requests (SC-009); a second compaction sends the update prompt with the previous summary
- [X] T015 [US5] Add `context_window`, `auto_compaction` (default on), and `compaction_buffer` to the AI Model node and handle, rejecting a malformed buffer at execution, in pkg/nodes/ainodes/model.go and pkg/nodes/ainodes/ainodes.go
- [X] T016 [US5] Wrap the AI Agent's model call in `Compactor.Send` (skipped for connections without tool support and provider-managed sessions), add summary usage, emit `context.compacted` on the stream, and record `compactions` in output metadata and the response envelope, in pkg/nodes/ainodes/agent.go
- [X] T017 [US5] Wrap Kuri's per-round `driver.Stream` in `Compactor.Send`, emit `context.compacted` before `reply.start`, and trace the summary, per feature 012 FR-026, in pkg/mcpserver/assistant.go
- [X] T018 [P] [US5] Add a Kuri compaction fixture (012 SC-006) in pkg/mcpserver/assistant_test.go
- [X] T019 [P] [US5] Render `context.compacted` as a "Context compacted" divider in frontend/src/lib/components/EditorAssistant.svelte and frontend/src/lib/components/editor/NodeChat.svelte

**Checkpoint**: SC-007 through SC-009 and 012 SC-006 pass under `make test TARGET=server`.

## Phase 3: Polish

- [X] T020 Document compaction, window detection, and the new fields in docs/ai.md and Kuri's behavior in docs/ai.md's Kuri section
- [X] T021 Run `make dev`, `make test TARGET=server`, `make test TARGET=frontend`, restart the service, and run the quickstart live checks (detection, Kuri on llama-swap gemma-4-26B with the 2026-11-08 request, node chat marker, compaction off) with browser screenshots and a console check
- [X] T022 Commit each verified step with scoped Conventional Commits and record verification below

## Dependencies

T006, T007, and T008 are independent. T009 needs T008; T010 needs T009. T011 needs
T006, T007, and T009. T012 and T013 follow their implementations. T014 needs T011.
T015 and T016 need T011; T017 needs T011; T018 needs T017; T019 is independent of the
server tasks. Polish follows all of them.

## Parallel execution

- T006, T007, and T008 together.
- T012 and T013 together once T006 and T010 land.
- T018 and T019 together once T017 lands.

## Implementation strategy

The MVP is T006 through T017: it fixes the 2026-11-08 Kuri failure and the agent
loop. Frontend markers, docs, and live checks complete the change.

## Verification (automatic context compaction)

- `make dev`, `make test TARGET=server`, and `make test TARGET=frontend` passed.
- Fixtures: the 2026-11-08 shape finishes with two or more compactions, the second
  an update, and no request over the window (SC-007); overflow resend, double
  rejection, and disabled compaction (SC-008, SC-009); the Kuri endpoint on a
  32,768-token window (012 SC-006); captured overflow texts; window detection.
- Live, 2026-11-08: llama-swap reported gemma-4-26B's window as 32,768 from its
  props. Kuri replayed the original request on it: 40 rounds and six threshold
  compactions (about 24,700 tokens down to 19,200) with no overflow, where the same
  request had failed at round 11. Summaries kept entity IDs, the connection ID, and
  the exact tool error. The turn repeated because gemma targeted another flow, whose
  writes the server refused; that flow's draft was unchanged. Node chat on a
  4,000-token Context window compacted on the second turn and still answered a
  question about the first. Model node fields render, Automatic compaction shows
  checked when unset, and unchecking it hides Compaction buffer live. Console clear
  after reload. The compaction-off live case needs a real overflow and is covered by
  the SC-009 fixture.
- Test flows were deleted; the OpenRouter test connection from the earlier check
  remains. Kuri's saved connection and model were not changed.
