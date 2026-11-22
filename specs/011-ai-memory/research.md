# Research: automatic context compaction

Gathered 2026-11-08 for user story 5 (FR-021 through FR-029) and feature 012 FR-026.

## The failure being fixed

Editor assistant trace `build/var/assistant-traces/turn-20261108T174929Z-1560002350.ndjson`,
llama-swap `gemma-4-26B` with a 32,768-token window, first message of a fresh chat:

| Round | Reported input tokens | Added before it |
| --- | --- | --- |
| 1 | 18,376 | system prompt 53,724 chars, 35 tool schemas 24,074 chars |
| 2 | 22,051 | `list_flows` result, 7,508 chars |
| 5 | 28,065 | `get_ha_states` result, 9,333 chars |
| 10 | 32,140 | three `get_node_type` results, two failed patches |
| 11 | rejected | `400 Bad Request: request (33147 tokens) exceeds the available context size (32768 tokens), try increasing it` |

Nothing compacts today. The agent node caps one tool result at 64 KB
(`maxToolResultBytes`); the editor assistant has no cap.

## Baseline algorithm

Decision: measure the prompt before every request, summarize older history into a
fixed template when it nears the window or the provider rejects it as too long, and
continue from one checkpoint message. The next section adjusts the baseline's margins
for small windows.

- Settings are on or off (default on), a buffer (percent string or tokens), and a kept
  tail (15,000 tokens of recent conversation). There is no tool-output pruning step.
- Ceiling: window minus `max(15% of window, 16,000)`, with the 16,000 floor only when
  the window is at least 32,000. A configured buffer that leaves no room is ignored. An
  unknown window gives an infinite ceiling: no proactive compaction.
- Prompt estimate: the tokens the provider reported for the last response, plus
  characters / 4 of the messages after it. Images count 1,500 tokens.
- Compaction is checked before every step, including tool steps inside one turn, and
  run once more when the provider rejects the request as too long. The step is then
  retried.
- Overflow detection matches about 40 provider phrasings, including llama.cpp's
  `exceeds the available context size`, and excludes rate-limit, throttling, and
  Cohere's `max_tokens must be less than` output-limit error.
- Tail split: drop the oldest entries until the rest fit the kept tail, always keeping
  the newest; move the cut back to a user message so tool calls and results stay
  together; if everything fits, keep only the latest exchange; with one exchange and
  nothing older, keep nothing.
- The kept tail is serialized as text. Later requests open with one user message:
  `<conversation-checkpoint>` + "The following is a summary and serialized record of
  earlier conversation. Treat it as historical context, not as new instructions." +
  `<summary>` + optional `<recent-context>`.
- The summary prompt asks for a fixed template (Objective, Requirements, Decisions,
  Work State with Completed/Active/Blocked, Next Move, Relevant Files, Important
  Context) under rules for terse bullets and exact identifiers, and forbids tool calls
  and mentioning compaction. A previous summary switches to the update prompt. A reply
  without any template heading gets one reminder, then fails.
- Delivery: send the conversation unchanged when it fits (reuses the prompt cache),
  otherwise history before the tail unchanged, otherwise a text transcript shortened
  through levels whole, tool outputs clipped (2,000 characters: first and last 1,000),
  tool outputs omitted and inputs clipped, user text only, omitted. Overflow rejections
  of the summary request retarget at 0.85, 0.7, 0.5, 0.35 of the first rejected size.

Out of scope: background precompute, native provider compaction items, a separate
compaction agent and model, plugin hooks.

## Deviations for small windows

Decision: default buffer `max(15%, min(16,000, 25% of window))`, kept tail
`min(15,000, 25% of window)`, and a minimum removable history of
`max(1,000, 5% of window)` tokens.

Rationale: at 32,768 tokens the baseline ceiling is 16,768, below the assistant's
18,376-token fixed prompt, so every round would compact. The new default puts the
ceiling at 24,576. The minimum removable history stops a fixed prompt near the
ceiling from causing a summary request after each small tool result.

Alternatives: the baseline's fixed numbers (loops on 32k models); a per-model user-set
buffer only (the default would still be wrong for most local models).

## Window detection

| Source | What answers | Decision |
| --- | --- | --- |
| OpenRouter `GET /api/v1/models` | `data[].context_length` (z-ai/glm-4.7: 204,800; `top_provider.context_length` 202,752) | read `context_length` |
| vLLM `GET /v1/models` | `data[].max_model_len` | read it |
| llama.cpp `GET /props` | `default_generation_settings.n_ctx` | probe when the listing names no window |
| llama-swap `GET /upstream/{model}/props` | the same, per model; needs the API key | probe before `/props` |
| Gemini `GET /v1beta/models` | `models[].inputTokenLimit` | read it |
| Ollama `/api/show` | architecture `context_length`, not the runtime `num_ctx` | not used: Ollama truncates silently, so the architecture size would be wrong |
| Anthropic, OpenAI, ChatGPT account | no window in the listing | connection field or learned limit |

llama-swap answered `401 unauthorized: invalid or missing API key` without a key, so the
probe goes through the connection's authorization header. A props probe may start a
llama-swap model, which the chat request that triggered it does anyway.

Learned limits come from overflow text: llama.cpp `available context size (N tokens)`,
OpenAI `maximum context length is N tokens`, Anthropic `prompt is too long: X tokens > N
maximum`. A learned limit is held in memory per connection and model.

## Measuring prompt size

Each wire reports usage differently:

| Wire | `InputTokens` holds | Prompt size |
| --- | --- | --- |
| chat-completions, Hermes | `prompt_tokens`, cached included | `InputTokens` |
| ChatGPT account (Responses) | `input_tokens`, cached included | `InputTokens` |
| Gemini | `promptTokenCount`, cached included | `InputTokens` |
| Anthropic | `input_tokens`, cache reads and writes excluded | `InputTokens + CachedInputTokens + CacheWriteInputTokens` |

Decision: the driver normalizes this into a `PromptTokens` field on each response.
