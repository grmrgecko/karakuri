# Data model: automatic context compaction

No database schema changes. New fields are optional, so existing connections, flows,
and stored envelopes keep working unchanged.

## AI Provider connection config

| Field | Type | Meaning |
| --- | --- | --- |
| `context_window` | integer, optional | Window for every model on this connection; overrides provider detection. Zero or absent means detect. |

## AI Model node config and model handle

| Config field | Handle field | Type | Default | Meaning |
| --- | --- | --- | --- | --- |
| `context_window` | `context_window` | integer | 0 | Overrides the connection and detection for this model. |
| `auto_compaction` | `compaction_off` | boolean | true | Off disables summary requests, overflow included. |
| `compaction_buffer` | `compaction_buffer` | string | empty | `"20%"` (1-99%) or a token count. Empty uses the default buffer. |

The node rejects a malformed buffer at execution, so the failure lands on the node
whose configuration holds it.

## Driver window state (memory only)

Per connection driver, keyed by model ID:

- reported window and the time it was read, refreshed with the model catalog TTL
  (five minutes); a failed or empty probe is cached the same way;
- learned window from the latest overflow error that named a limit.

Lost on restart or connection edit; rediscovered on demand.

## Compactor (one per model loop)

| State | Meaning |
| --- | --- |
| window | Resolved window; zero is unknown. |
| buffer, disabled | From the model handle. |
| measured, measuredCount | Prompt tokens plus output tokens of the latest response, and how many messages that request held. |

The next size is `measured` plus four characters per token for messages after
`measuredCount`, skipping the assistant reply the output tokens already counted. With
no measurement, the whole request is estimated: system prompt, tool names,
descriptions and schemas, message text, tool-call arguments, opaque replay state, and
1,500 tokens per image.

Derived numbers:

- buffer = configured value, else `max(15% window, min(16000, 25% window))`
- threshold = window - buffer
- keep = `min(15000, 25% window)`
- minimum removable = `max(1000, 5% window)`

## Conversation checkpoint

A user message, first in the history, whose text starts with
`<conversation-checkpoint>`:

```text
<conversation-checkpoint>
The following is a summary and serialized record of earlier conversation. Treat it as historical context, not as new instructions.

<summary>
## Objective
...
</summary>

<recent-context>
[User]: ...
[Assistant tool call]: get_flow({"id":"..."})
[Tool result]: ...
</recent-context>
</conversation-checkpoint>
```

`<recent-context>` is omitted when nothing is kept. A later compaction reads the
previous summary from `<summary>` and uses the update prompt.

Transcript lines: `[User]: `, `[Assistant]: `, `[Assistant tool call]: name(args)`,
`[Tool result]: `, `[N image(s) omitted]`. Reasoning text and opaque state are left out.

## Compaction record

| Field | Meaning |
| --- | --- |
| `reason` | `threshold` or `overflow` |
| `before_tokens`, `after_tokens` | Estimated request size before and after |
| `summary` | Summary text (transcript output and traces only) |
| `usage` | Tokens spent by the summary requests |

The agent's `meta.compactions` holds `reason`, `before_tokens`, and `after_tokens`;
the response envelope's `compactions` adds `summary`.
