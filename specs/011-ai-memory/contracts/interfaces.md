# Contract: automatic context compaction interfaces

## Connection kind `ai`

New optional field **Context window** (`context_window`, number). Help: tokens the
model accepts; empty detects it where the provider reports one. Ollama needs it.

## Node `ai-model`

New fields, after reasoning settings:

- **Context window** (`context_window`, number, optional).
- **Automatic compaction** (`auto_compaction`, boolean, default on).
- **Compaction buffer** (`compaction_buffer`, string, shown while automatic
  compaction is on, placeholder `15%`).

## Node `ai-agent`

- `out` metadata: `compactions: [{reason, before_tokens, after_tokens}]` when any ran.
  `usage` includes summary requests.
- `transcript` envelope: `compactions: [{reason, before_tokens, after_tokens, summary}]`.
  `transcript` entries start with the checkpoint user entry after a compaction, so
  replaying the envelope stays compacted.
- `ai-stream` gains event `{"type": "context.compacted", "index", "reason",
  "before_tokens", "after_tokens"}` between response deltas.

## Node chat stream (`POST /api/flows/{id}/nodes/{node_id}/chat`)

Relays the agent's `context.compacted` stream event unchanged. The chat window shows
"Context compacted" at that point in the conversation.

## Editor assistant stream (`POST /api/assistant/chat`)

New event `{"type": "context.compacted", "reason", "before_tokens", "after_tokens"}`,
sent before the model round it shrank. The `done` event's `messages` start with the
checkpoint user message after a compaction. Traces record `context.compacted` with the
summary.

## Errors

A compaction failure returns the original request error when one exists (overflow),
otherwise `context compaction failed: <reason>` from the loop's model request.
