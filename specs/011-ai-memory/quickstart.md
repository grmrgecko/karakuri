# Quickstart: automatic context compaction

## Deterministic checks

```sh
make test TARGET=server
```

The provider fixtures cover the 2026-11-08 failure shape (SC-007), overflow resend
and double rejection (SC-008), compaction turned off (SC-009), and the editor
assistant turn (feature 012 SC-006). Each fixture server rejects any request over its
window with llama.cpp's overflow text, so a pass also proves no oversized request was
sent.

## Live checks

Prerequisites: the development service running, the `llama-swap` connection
(gemma-4-26B, 32,768 tokens), and an OpenRouter connection.

1. **Detection.** Over MCP, leave the llama-swap connection's Context window empty and
   run an AI Agent test with gemma-4-26B; the debug log reports the window read from
   `/upstream/gemma-4-26B/props`. An OpenRouter model reports `context_length`.
2. **Kuri on a small window.** Ask Kuri on llama-swap gemma-4-26B for the 2026-11-08
   request ("When I turn on my office light, also turn on the workbench light and
   lamp..."). The turn shows "Context compacted" and finishes or stops at a normal
   limit without an overflow error. The trace has a `context.compacted` record whose
   summary names the office light entities.
3. **Node chat.** On an AI Model with Context window 8000 and a tool returning about
   5 KB, chat until "Context compacted" appears; the next message still has the earlier
   facts.
4. **Off.** Turn Automatic compaction off on that model and repeat; the provider's
   overflow error is shown and no summary request is made.

Remove the test flows and connections afterward, and restore Kuri's saved connection
and model if they were changed.
