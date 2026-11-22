# AI nodes

Karakuri talks to language models through the AI node set: a model node that
names which provider and model to use, an agent node that sends a prompt and
runs the tools the model calls, and tool nodes that define what those tools
are. They connect in the flow editor through typed sockets. A model travels on
an `ai-model` wire and a tool set on an `ai-tool` wire, and the editor rejects a
misconnected graph before anything runs.

## Provider connections

Configure a provider once under **Settings, Connections**, then
use it in any number of flows. One **AI Provider** connection kind covers every
provider. Its **Provider** field picks who the connection talks to, and **Test**
checks the endpoint by listing its models. Plugins can add more providers to the
picker.

| Provider | For | Notes |
| --- | --- | --- |
| OpenAI | api.openai.com, or a ChatGPT subscription | API key, or ChatGPT account sign-in (below). |
| OpenAI-compatible server | LM Studio, llama-swap, vLLM, LocalAI, OpenRouter (`https://openrouter.ai/api/v1`) | Base URL required, including the version prefix (`http://localhost:1234/v1`). Key optional. |
| Anthropic | api.anthropic.com | API key required. No embeddings API. |
| Google Gemini | generativelanguage.googleapis.com | API key required. |
| Ollama | A local or remote Ollama | Address only; the `/v1` API path is appended automatically. |
| Hermes Agent | A local or remote Hermes profile gateway | API key required. Hermes runs its own tools, so its models do not accept Karakuri tool or memory wires. |

Every provider connection has an optional **Context window**, the number of
tokens a request may hold for its models. Leave it empty to detect it; Ollama
needs it set. See [Context compaction](#context-compaction).

MCP servers are a separate connection kind, because an MCP connection provides
tools rather than models.

### Signing in with a ChatGPT account

The OpenAI provider's **Authentication** field offers **ChatGPT account
(Codex)** beside the API key. Requests then bill a ChatGPT Plus or Pro
subscription instead of platform credit. Click **Sign in with ChatGPT**, finish
the OpenAI login in the new browser tab, then save the connection. When browser
login is unavailable, use **Import Codex auth file** in the same panel and paste
the contents of `~/.codex/auth.json`. Saving either form tests the account
before the dialog closes. Karakuri stores the sign-in encrypted and renews it.
The model picker offers the models the subscription can use (`gpt-5`,
`gpt-5-codex`, and others). If the connection stops authenticating after the
server has been down for a long time, edit it and sign in again.

## The AI Model node

**AI Model** names a provider connection, a model identifier, sampling settings
(temperature and max tokens), and reasoning settings, and emits a model on its
`ai-model` output. The model field completes from the connection's model list,
which refreshes every few minutes. You can still type any model the provider
accepts, because some providers serve models their listing omits. Wire the
output into an agent's **Model** socket. One model node can feed any number of
agents, so switching every agent to a new model is one edit.

**Reasoning effort** selects the provider's named reasoning depth. **Reasoning
budget** sets a token budget for older Anthropic models and Gemini 2.5. Set one
or the other, not both. Anthropic requires the budget to be below max tokens;
with **Max tokens** empty, an Anthropic request asks for 4,096 tokens plus the
budget. Empty fields leave the provider default in force. Reasoning carries
across tool calls, so an agent can keep reasoning after it sees a tool result.
Support depends on the selected model. A level or budget the model does not
accept fails the model call; Karakuri never changes it silently.

Anthropic reasoning requests leave out temperature, because the API does not
accept sampling controls with thinking. Karakuri also drops a configured
temperature for Claude models newer than Opus 4.6, which no longer accept it.
Older Claude models and non-Claude models behind a gateway still receive it.

**Context window**, **Automatic compaction**, and **Compaction buffer** control
[context compaction](#context-compaction). Context window overrides the
connection's setting and detection for this model. Automatic compaction is on by
default. The buffer is a percentage such as `20%` or a token count; a malformed
buffer fails the model node.

## The AI Agent node

**AI Agent** sends a prompt or conversation to the model and, when tools are
attached, runs whatever the model calls until it settles on an answer.

When **Tools** or **Memory** is wired, the node rejects a model whose provider
does not accept tools. Hermes models work when both sockets are empty, because
the Hermes profile runs its own tools and memory.

- **Model** is a permanent input socket taking exactly one `ai-model` wire.
- **Tools** is a permanent input socket taking **any number** of `ai-tool`
  wires; every attached set is offered to the model.
- **Input** selects where the request comes from. **Prompt** renders one
  configured user-message template. **Transcript** renders a configured list
  of User, Assistant, Tool, and System messages. **Input** reads the incoming
  message directly. A string payload becomes one user message, while an array
  of `{role, text}` entries or an object's `transcript` property becomes the
  conversation. System entries in incoming data are ignored; the node's
  **System prompt** always applies.
- **Prompt** and **System prompt** support `{{ }}` expressions, so
  `Summarize: {{ payload.text }}` works as it does elsewhere.
- Transcript mode shows an **Add message** editor, and every configured message
  supports `{{ }}` expressions. The Input selector and the configured Prompt and
  Transcript values never become parameter sockets. Transcript data received in
  Input mode is used as plain text, so `{{ }}` text written by a model is never
  evaluated.
- **Attach binary payload as image** sends the incoming message's binary
  payload, such as a webhook's camera frame or an HTTP response, to the model as
  an image beside the prompt. Use it for bytes that arrive on the message path.
  The image type comes from what the producing node declared (`meta.mime`,
  `meta.content_type`, or the `meta.format` a screenshot node sets); bytes with
  no declared type fail the node.
- **Images** is a permanent input socket taking **any number** of `image`
  wires, for prompts about more than one picture. Each wire comes from an
  **Image** node (folder Parsers, beside Document). Wire a screenshot or a
  binary file read into the Image node's Source socket and pick its image type
  there, because a value wire carries only the bytes. The model receives the
  payload image first, when attached, then the wired images in wire order.
  Images cannot be placed inside the prompt text, so refer to them by position:
  "image 1 is the driveway camera, image 2 the porch".
- **Response format** set to JSON asks the model for a JSON object and emits it
  decoded (the output port reports `object`). If the first reply is not valid
  JSON, the node shows the model the error and gives it one chance to repair it.
- **Response schema** (JSON only) is a JSON Schema the reply must match.
  OpenAI-compatible providers enforce it directly, Gemini does when no tools are
  attached, and other providers receive it in the system prompt. In every case
  Karakuri checks the reply against types, `required`, `properties`, `items`,
  and `enum`. A reply that still does not match after the repair attempt fails
  onto the error branch.
- **Max iterations** bounds the tool loop. A model that never gives a final
  answer fails the node instead of running up the bill.

A tool failure is *not* a node failure. The error text goes back to the model
as the tool's result, so the model can recover. Provider errors, such as a bad
key, a missing model, or an unreachable endpoint, fail the node onto its error
branch and are retryable.

The answer is the payload. The metadata carries `model`, accumulated `usage`
(input and output tokens across every call of the loop), and `tool_calls`, a
record of each call with its arguments and result. Run history keeps this
metadata, so you can read back what a misbehaving loop did.

The node builds the **Response** output (port ID `transcript`) only when it is
wired. Its
object carries `transcript`, `responses`, and the state needed to continue the
conversation. `transcript` is the replayable message history: earlier turns,
the rendered prompt, each tool round (`tool_calls` on the assistant turn,
`tool_call_id`/`tool_name` on the result), and the final answer. It leaves out
the system prompt so the receiving node keeps its own instructions. `responses`
holds every raw provider response in call order, including tool-loop and
JSON-repair calls. A streamed response is stored as its list of events.

The object can also carry `session_id` when Hermes owns the conversation,
`cache_key` for providers that cache prompts per conversation, and
provider-specific reasoning state. Pass the whole object to the next AI node.
Taking only the `transcript` array loses the session and reasoning state.

This output can go back to **HA Assist** through its Response input. Home
Assistant keeps the complete object and appends the next user turn to its
`transcript` array. Images are kept as a count in the history, so a
screenshot-heavy conversation does not carry the image bytes twice.

HA Assist's **AI decides** follow-up mode reads a JSON response containing a
`response` string and a `continue_conversation` boolean. Set the response format
to JSON with a schema that requires both. HA Assist speaks the response string,
sends the boolean to Home Assistant, and keeps the full object for the next
turn. When streaming, HA Assist holds the text until the complete JSON object
arrives, while tool progress still shows live.

Enable **Stream** to receive text as the provider generates it. This changes
the **Response** output from `object` to `ai-stream`, so it can no longer be
wired into nodes that only accept completed objects. The stream carries
`content.delta` events and any provider progress events, then one
`response.completed` event. Hermes emits `tool.progress` while its built-in
tools run, with the tool call ID, name, label, and status. The final event
carries both the completed `output` value and the full `response` object.

When a provider returns readable reasoning, the assistant entry carries it as
`reasoning_summary`. Encrypted or signed reasoning stays on the assistant turn
as `opaque_state`, and Karakuri sends it back only to the provider that produced
it.

The `usage` metadata reports `cached_input_tokens` and
`cache_write_input_tokens` beside input and output tokens. Karakuri uses OpenAI,
Anthropic, and Gemini prompt caching automatically. A cache hit needs the same
model, system prompt, tools, and earlier messages as the previous request.

### Context compaction

When a conversation outgrows the model's context window, Karakuri summarizes
older history instead of failing. This applies to AI Agent, node chat, and Kuri.

Before every model request, including tool rounds, Karakuri estimates the prompt
size. When it passes the window minus a buffer, Karakuri summarizes older
history. The default buffer keeps 15% of the window free, raised to 16,000
tokens or a quarter of the window, whichever is smaller. On a 32,768-token model
that is 8,192 tokens. When the provider rejects a request as too long, Karakuri
compacts and sends it once more. A second rejection fails the node with the
provider's error, and its retry policy applies.

Compaction makes one extra request to the same model for a structured summary
covering the objective, requirements, decisions, work state, next step,
relevant items, and important context. The newest exchange is kept word for
word, up to 15,000 tokens or a quarter of the window, and the summary replaces
everything before it. The next compaction updates that summary. Compaction
only runs when at least 1,000 tokens or 5% of the window can be removed, so a
large system prompt near the limit does not cause a summary every round. The
system prompt and tool definitions are never summarized, so when they alone
nearly fill the window, only history can shrink.

The window comes from the AI Model node's **Context window**, then the
connection's, then what the provider reports or what an overflow error stated.
OpenRouter, vLLM, llama.cpp, llama-swap, and Gemini report windows. Ollama
silently cuts requests down to its `num_ctx` instead of rejecting them, so set
the Ollama connection's **Context window**. With no known window, only an
overflow error triggers compaction.

Each compaction adds the summary request's tokens to `usage`, adds
`{reason, before_tokens, after_tokens}` to the output's `compactions` metadata,
and adds the summary to the Response object's `compactions`. A streamed agent
emits `context.compacted`. The Response transcript starts from the summary, so
continuing the conversation stays compacted. Providers that keep history in
their own session, such as Hermes, are never compacted.

## Testing an AI node in chat

Click **Chat** at the right of an **AI Agent** or **AI Basic Chain** canvas
title. The chat window moves and resizes like Kuri's window, and the canvas
stays usable behind it. It uses the node's configured model, tools, memory,
images, and response settings. There is no model picker or execution switch.

Each message you type is sent as a string on **In**. In Prompt mode, the
configured prompt renders against it; use `{{ payload }}` where your message
belongs. Transcript mode renders the configured rows against it. Input mode
sends your message directly. The configured system prompt applies on every turn,
and each turn continues the conversation from the previous reply. **New chat**
clears the conversation in the browser. It does not delete memory already saved
by a wired AI Memory node.

Unsaved draft edits are saved before each turn. Only the selected node, the
values it pulls, and its tool branches run. Ordinary output and error branches
do not run, and chat does not replace the flow's last-run history or its
deployment. Variables and node stores start empty for each turn. Tool actions
and memory writes are real. A node inside a local subgraph can be tested
directly, but values that need a caller or an upstream message need a full
flow run.

Replies stream into the transcript, and completed tool calls show their
arguments and results. When the model disclosed reasoning, a collapsed
**Reasoning** section above the reply holds it once the turn completes. A
**Context compacted** divider marks where [compaction](#context-compaction)
summarized earlier conversation. **Stop**, a disconnect, or the five-minute turn
limit cancels the turn. Chat drops a failed or stopped turn from the
conversation, so the next turn does not resend its partial tool rounds. Chat
runs the node once even if the draft has a retry override; tool branches keep
their own retry policies and the agent keeps its iteration limit. The text box
cannot supply a binary image payload. Turn off **Attach binary payload as
image** for chat, or test the flow from its normal image-producing start.

## The AI Basic Chain node

**AI Basic Chain** sends one prompt or transcript to an **AI Model** without
adding Karakuri tools or memory. It has the same prompt, transcript, system
prompt, image, and structured-response controls as **AI Agent**, but no
**Tools**, **Memory**, or **Max iterations** fields.

Use it for a Hermes profile, which keeps control of its own personality, skills,
memory, MCP servers, and tools, or for any request that needs one model reply
without a tool loop.

Hermes serves profiles through the API server attached to its gateway. Enable
it in the profile's `.env`, give it a strong key, then restart that profile's
gateway:

```dotenv
API_SERVER_ENABLED=true
API_SERVER_KEY=<a random secret>
```

Create an **AI Provider** connection in Karakuri with **Provider** set to
**Hermes Agent**. Its default base URL is `http://127.0.0.1:8642/v1`; change the
address when Hermes runs on another host. Paste the same key and test the
connection. Each connection belongs to the profile that gateway serves, so the
model node has no separate profile setting. Connect that **AI Model** to **AI
Basic Chain**, or to **AI Agent** with no tools or memory attached.

The answer is the payload, and the metadata carries the configured model and
token usage. The **Response** output has the same shape as **AI Agent**'s. When
it is wired, the `transcript` includes the tool calls Hermes made with its
built-in tools, at the cost of one extra request to Hermes. The Hermes session
ID is sent back on the next request so Hermes can continue the same session.

The **Images** socket accepts any number of `image` values and sends them beside
the prompt in wire order. **Attach binary payload as image** uses the incoming
message's bytes instead. That payload must declare an image MIME type;
screenshot nodes already do. The selected model must support images.

## Tool nodes

Tool nodes emit an `ai-tool` set, which holds what the model sees (name,
description, and a JSON Schema for the arguments) and what runs when it calls
the tool. The model decides when to call a tool from the description alone, so
say in it when the tool applies.

- **MCP Tools** offers an MCP server's whole catalog. It lists the connection's
  tools live, with an include/exclude filter and an optional name prefix for
  wiring two servers whose tool names collide.
- **Tool** declares what the model sees and runs a branch of the canvas as the
  tool body, so anything a flow can do, such as a script, an HTTP request, a
  Home Assistant call, or a command on a remote agent, can become a tool.

Tool names must be unique across everything wired to one agent (and unique per
graph for Tool nodes), and must be 1 to 64 characters of letters, digits, `_`,
and `-`.

## The Tool node

A Tool node has a name, the description the model decides by, and an
**argument list**. Each argument has a name, a type, a description, and a
required flag, and the node builds the JSON Schema the model sees from that
list.

Three kinds of port make the body work:

- **Call** (output) starts the branch. When the model calls the tool, a message
  with the arguments object as its payload leaves here, and whatever is wired
  downstream runs, on the server or on a remote agent.
- **Return** (the socket on the node's bottom edge) receives the branch's
  result. Wire the branch's last node back into it; the message arriving there
  is what the model sees as the result. The loop this draws on the canvas is
  intended.
- **Each argument is an output port** of its declared type. Branch nodes can
  bind config fields to them with value wires, and each call sees its own
  arguments, even when two calls happen in one run. The call message's payload
  also carries all of them, so a script node in the branch can read
  `payload.city`. Argument ports can only be read inside the branch.

A branch that returns a binary payload with a declared image type gives the
model the picture itself, so a Tool node around an agent's screenshot node is a
vision tool. Binary with no declared image type is reported to the model as an
error.

Concurrent calls (two agents, or one agent calling twice) never share state,
and every node in the branch records its run in run history, so you can trace a
misbehaving tool node by node. When a branch node fails and has no error branch
wired, the failure becomes the call's result for the model, not a run failure.
To shape that result yourself, wire the node's error output into Return.
**Timeout** bounds one call; a branch that never reaches Return reports
`timed out` to the model.

Deploying requires a complete loop, with Call and Return wired and a tool name
unique in the graph.

## Memory

A **memory store** is a named bank of memories, defined under **Settings,
Memory**. It holds the conversation transcripts an agent resumes and
the facts it recalls. Flows share stores by name, like controls, so two flows
naming the same store share one conversation and one set of facts.

Recall combines keyword matching, similarity search, and importance, and older
memories count for less over time at the rate set by the store's halflife.
Similarity search needs the store to name an **embedding connection and model**
(any OpenAI-compatible server or Gemini connection; Anthropic has no embeddings
API). Keyword search finds a new memory immediately, and similarity search finds
it a few seconds later. Changing the embedding connection or model re-processes
existing memories in the background. If the embedding provider fails, the
affected memories stay searchable by keywords, and Karakuri processes them again
the next time the store loads. Editing a memory's text or tags re-processes it.

**AI Memory** emits a memory on its `ai-memory` output; wire it into the agent's
**Memory** socket. It configures:

- **Session** is the conversation key, rendered against the agent's incoming
  message (`{{ payload.user }}`), so one store can hold a conversation per
  person, per room, or one shared conversation (leave it empty).
  Session-scoped memories never appear in another session's recall.
- **Transcript window** sets how many past turns go ahead of the new prompt,
  which lets the agent resume a conversation. The replay always starts on a user
  turn, so when the window cuts an exchange in half, that exchange's answer is
  left out. The store caps transcripts per session and drops the oldest turns
  first.
- **Recalled memories** sets how many memories to recall. The ones most relevant
  to the rendered prompt are added to the system prompt with their dates, so the
  model starts out knowing what matters. **Recall from conversations** also
  searches past transcripts.
- **Diversify results** considers more candidates and drops near-duplicate
  memories, keeping each result's original relevance score.
- **Offer remember and recall tools** gives the model a `remember` tool (text,
  importance, tags, session-only) and a `recall` tool (query, limit), so it can
  save facts worth keeping and search for context. No tool node is needed.
- **Save conversations** adds each prompt and final answer to the session's
  transcript.

The output metadata carries `memory` (store, session, and how many memories were
recalled). The settings page lets you browse, search, edit, and delete memories
and clear sessions; its search ranks results the same way agent recall does.
Clearing the shared session (the one with no name) removes its transcript and
keeps the store's global facts.

Two flow nodes use a store without a model. **Remember** stores a text (with
session, importance, tags, and an optional time to live in hours) and passes the
message through. **Recall** searches and emits the matching memories as the
payload, `[{text, score, kind, importance, created_at, tags?, session?}]`, so a
flow can route on whether anything was remembered. Both nodes have **Diversify
results**; on AI Memory the setting also applies to the agent's recall tool.

## The MCP Call node

**MCP Call** calls one tool on an MCP server directly from the flow, with no
model involved. The arguments document supports `{{ }}` expressions rendered
against the message. A tool failure fails the node onto its error branch.

## Guardrails

Two nodes check the text moving through an AI flow. **AI Guardrail** checks it
and routes, and **AI Sanitize** rewrites it. Both read the text named by their
**Text** template (`{{ payload.prompt }}`), or the payload as text when the
field is empty. Put one in front of an agent to check what reaches the model, or
behind it to check what the model produced.

**AI Guardrail** routes the message to its **Pass** or **Fail** output. Pass
emits it unchanged. Fail adds `meta.guardrail.violations`
(`[{check, kind?, match?, reason?, confidence?}]`) and leaves the payload
alone, so the failing branch can log, alert, or hand the text to a person.
Failing a check is routing, not an error. The error branch is for a provider
failure or an invalid pattern. The checks are:

- **Blocked keywords** match case-insensitively anywhere in the text.
- **PII** covers email, phone, credit card, SSN, IP address, and IBAN. Pick a
  subset, or leave the field empty for all of them. Card numbers must pass the
  Luhn check, so a random run of digits is not flagged.
- **Secret keys** covers AWS access keys, GitHub, Slack, OpenAI, and Google
  tokens, JWTs, PEM private keys, and `password:`/`api_key=`-style assignments,
  plus long random-looking words that match no known format. **Secrets
  sensitivity** tunes that last check. Strict flags shorter and less random
  strings and also checks URL- and filename-shaped words; permissive flags only
  long, very random ones. The reported match is masked after its first
  characters.
- **URLs** requires every URL to use an allowed scheme (default `https, http`)
  and, when a domain list is given, to be on a listed domain or one of its
  subdomains. `evilexample.com` never passes an `example.com` allowance.
- **Custom patterns** takes Go regular expressions, one per line.
- **Model-scored checks** are jailbreak, NSFW, and topic. They need judgment, so
  they ask a model for a verdict (one call covers every selected check) and fail
  the text when a check is flagged at or above the **confidence threshold**
  (default 0.7). Topic takes an **allowed scope** describing what the text may
  be about. Selecting any of these adds a **Model** socket that must be wired. A
  guardrail with only the other checks has no model input.

**AI Sanitize** emits the text with sensitive parts replaced by placeholders:
`[email]` and similar for PII, `[secret]`, `[url]`, and `[redacted]` for
keywords and custom patterns. PII and secret keys are on by default; URLs,
keywords, and patterns are opt-in. One placeholder, named after the widest
match, replaces overlapping matches together, so no part of a credential or
URL is left behind. The sanitized text is the payload (the output port reports
`string`), and `meta.sanitized` counts what each check removed without repeating
it. AI Sanitize uses the same checks as AI Guardrail and never calls a model, so
it is safe to put in front of one.

## Extracting and classifying

These nodes handle one-off structured jobs that do not need an agent. Each takes
a model on its **Model** socket and reads the text named by its **Text**
template, or the payload as text when it is empty.

**AI Extract** pulls declared attributes out of text into a JSON object. The
**attributes** list uses the same editor as a Tool node's arguments (name, type,
description, required), and the node builds the JSON Schema from it. The model
finds each attribute by its description. An optional attribute the text does not
mention is left out; a required one the model cannot supply fails the node onto
its error branch rather than producing a guess. The object is the payload (the
output port reports `object`), with `model` and `usage` in the metadata.

**AI Classify** routes by meaning. Each entry in the **categories** field is an
output port, the model picks one, and the message leaves on that port unchanged,
with the verdict (category, confidence, reason) in `meta.classification`. Text
that fits no category leaves on **Other**. The model decides from the category
names and the **instructions** field, so describe what belongs where when the
names alone are ambiguous. For sentiment analysis, use `positive, neutral,
negative` as the categories.

**AI Summarize** handles documents too long for one model call. It splits the
text into chunks, summarizes each chunk in order, then combines the partial
summaries until one remains. **Text** defaults to the payload, **Instructions**
states what the summary must keep, and **Chunk size** controls the size of each
request. Calls run one at a time, with at most 64 calls and eight rounds of
combining. The metadata reports the model, total token usage, chunk count, call
count, and combining rounds.

For text that fits in one call, use **AI Basic Chain**.

## Retrieval-augmented generation

A memory store becomes a knowledge base when you feed it documents instead of
conversations. Create a store under **Settings, Memory** with an
embedding connection and model (without one, recall falls back to keyword
matching), **recency weight 0** so reference material does not fade, and **no
fact cap**.

**Ingest** splits a document into chunks and stores them. Paragraphs are packed
up to the chunk size and longer ones are split at word boundaries. Each chunk
starts with some overlap from the one before, so a fact on a boundary stays
whole in at least one chunk. Every chunk carries the **source** name, and
ingesting a source again *replaces* its chunks, so `file-watch → file-read →
Ingest` keeps a store in step with a changing file without duplicates.
Ingesting an empty document removes the source. A failed ingest leaves the
previous version in place. Keyword search finds new chunks immediately, and
similarity search finds them shortly after.

Chunk size and overlap are measured in bytes of text, with defaults of 1200 and
150. Overlap is limited to half the chunk size.

Run a PDF or Word file through the **Document** parser (folder Parsers) first.
It takes binary and emits the extracted text, for `pdf`, `docx`, and `odt`. Left
on **From file type**, it picks the format from the declared metadata
(`meta.mime`, or the extension of the `meta.path` a file read sets) and refuses
bytes with no declared type. It extracts text only, so a scanned PDF fails with
a message that it needs OCR.

For example, to give a support agent a procedure document, drop `sop.pdf` where
a **Watch Files** trigger sees it. Wire `file-read` (binary) into `Document`,
and `Document` into `Ingest`, with the path as the source and a "Support docs"
store as the target. Then give the support agent an **AI Memory** node on that
store. Every edit to the file replaces its chunks, and the agent recalls the
relevant procedure for each request.

To read the knowledge back:

- **To have an agent answer from it**, wire an **AI Memory** node on the store
  into the agent's Memory socket. The chunks most relevant to the rendered
  prompt are added to the system prompt. Turn **Save conversations** off unless
  the store should also collect the chats.
- **To use the chunks in a flow**, use **Recall**, which returns them as
  `[{text, score, source, ...}]` so a flow can route on them, build its own
  prompt, or cite `source` in an answer.

## Wiring notes for generated graphs

This section is for writing flow graph JSON directly, for example through MCP.

The agent's model, tools, and memory inputs are **port fields**, which are
permanent value input ports rather than settings. Nothing is typed into them,
and they never appear in a node's `params` list. Supply them by wiring a value
edge (`"kind": "value"`) to `param:model`, `param:tools`, `param:memory`, or
`param:images`. The tools and images ports accept several value edges, and the
values arrive in edge order. The agent's `transcript` config field is an
ordinary setting (an array of `{role, text, ...}` entries in the Response
output's format) that may also be exposed as an object value input via `params`.
A value edge must come from a node with no message ports, so another agent's
Response output cannot be wired in directly. A required port field must be wired
before the flow deploys. The memory port is optional and takes at most one
`ai-memory` edge from an AI Memory node, whose config fields are `store` (a
memory store's name or UUID), the `session` template, `window`, `recall`,
`recall_turns`, `tools`, and `save`. The guardrail's `param:model` is a port
field that exists only while its `model_checks` config names a check
(`jailbreak`, `nsfw`, `topic`, comma-separated), and is required exactly then.
The extractor's and classifier's `param:model` always exist and are required.

The classifier's category outputs come from its `categories` config, a
comma-separated list of display names. A category's handle is `cat:` plus its
lowercased name, so `"categories": "Urgent, Newsletter"` yields `cat:urgent` and
`cat:newsletter` beside the fixed `other` and `error` outputs.

A Tool node's `out` port connects to the agent's `param:tools` with a value
edge. Its `call` port connects to the branch with an ordinary message edge, and
the branch's last node connects back into the tool's `return` input with
another message edge. The graph allows this cycle. An argument declared as
`{"id": "city"}` in the node's `tool_args` config adds the output port
`arg:city`, which connects to a branch node's `param:<field>` input with a value
edge. Argument ports carry values, never messages.

## Editing dashboards and flows with AI

Kuri is the dashboard and flow editor assistant. Both editors show an **Ask
Kuri** button when an enabled, connected AI provider accepts Karakuri tools and
you can read connections. Hermes is not offered because it runs its own tools.
A model that rejects tools reports that error in the chat. Choose a connection
and a model, or type a model ID, then describe the changes. Karakuri saves your
choice in your profile and reused in both editors, including after a reload,
even when the provider's catalog does not list the model.

The window shows streamed replies and each tool call. Expand a call to see its
arguments and result. Reasoning the model disclosed sits in a collapsed
**Reasoning** section above that round's reply. Kuri's replies render Markdown,
including lists, emphasis, links, tables, and highlighted code blocks; raw HTML
and unsafe links are removed. If rendering fails, the plain reply stays visible
with a notice. Your messages and tool arguments and results are shown as plain
text. Successful changes refresh the editor behind the window, and you can keep
working in the flow or dashboard while chat stays open. Both editors autosave
drafts, and opening Kuri on a flow saves pending canvas edits first. Kuri edits
the whole selected flow, including local subgraphs. Shared subflows must be
opened in their own editor.

Kuri's persona is warm, cheerful, and organized. It sometimes jokes about
stubborn bugs or tangled wiring, and it stays calm when something fails. Code,
configuration, and error details stay precise. Configured AI nodes keep their
own system prompts.

Kuri compacts long conversations as described in
[Context compaction](#context-compaction), using the connection's **Context
window** or the detected one. A **Context compacted** divider appears in the
window, and the next message continues from the summary. Kuri's own
instructions and tool definitions take about 18,000 tokens, so on a
32,768-token model there is little room left and compaction runs often.

Kuri acts with your permissions. It can inspect node catalogs, connections,
Home Assistant states, controls, and MQTT devices, and it can change only the
selected document. On dashboards it can place and configure widgets, edit grid
geometry, choose a theme, and add, reorder, rename, or remove sections and
pages. Removing a section also removes its widgets; removing a page moves its
widgets to every page. On flows it can update metadata, validate graphs, and
save the draft.

**Allow deployment and execution** is off by default. Kuri cannot deploy, enable
or disable, test-run, or send dashboard commands until you turn it on. New chat
and changing the model turn it off again. It does not grant permissions you
lack. Restoring a version is a draft change and does not need it. Test runs
perform real actions. Kuri cannot send device commands directly, change sharing,
or edit connections.

Changes made in the browser, through MCP, and by Kuri all go to the same draft,
and open editors refresh when another client edits, deploys, or restores it. The
server refuses a browser save if someone else changed the draft first. Restoring
replaces the whole draft with the selected version without publishing it.

On dashboards, `render_dashboard` returns a screenshot of the draft or deployed
view at the requested size. A model that accepts images sees it, and the chat
shows it under the tool result. Rendering does not publish the draft. Only the
latest screenshot goes to the model on later turns.

Kuri can also make focused graph edits with `patch_flow_graph`, compare
versions, inspect dependencies, validate dashboards, collect render diagnostics,
and read agent status. Patches, comparisons, dependency checks, dashboard draft
reads, and run and state inspection are limited to the selected document.
General lookups, `list_flows`, `get_flow`, and `find_references` can read other
documents you have access to. Node tests and flow test runs can still perform
real device or host actions.

To diagnose a flow, Kuri can run the draft, inspect that run, list recent runs,
and filter run events by node.

Drag the chat title to move the window, or the bottom-right handle to resize
it. Focus either handle and use the arrow keys to move or resize; hold Shift for
larger steps. Closing and reopening keeps the window's position and size, the
scroll position, and expanded tool details until you leave or reload the editor.
The window stays inside the browser window. Escape closes chat when focus is
inside it and no turn is running. Editor shortcuts keep working when focus is
in the editor.

You can type the next message while Kuri works. Sending waits until the current
turn finishes or Stop has taken effect; the text you typed stays in the box.

**Stop** cancels the request. Changes already made stay in the draft, and the
editor refreshes even when the stream fails. Close becomes available once the
request settles. A turn may run at most 50 model rounds and five minutes. Each
model response counts as one round, including lookups and failed tool calls. At
the round limit, Kuri keeps the results and conversation so far. Send another
message to continue. The conversation lasts only for the current editor tab;
leaving the editor, changing models, or choosing **New chat** clears it. After
an interrupted turn, Kuri does not see that turn's partial history, but the
chat stays visible. Each turn reads the current draft again.

Review the result, then use **Deploy**, or ask Kuri to deploy, when it is ready.
Deployed flows and dashboards keep their deployed version until then. A
dashboard that has never been deployed shows its draft, as usual.

### Editor turn traces

To record Kuri's turns for troubleshooting, set `mcp.assistant_trace_dir` to a
local directory and restart the server. It is empty by default, which turns
tracing off. Each turn writes one NDJSON file readable only by the server's
user, and the server creates a missing directory with the same restriction. A
trace holds the prompt and earlier history, the system instructions, tool
definitions, the raw provider responses, tool calls and results, round timings,
and how the turn ended. It never holds HTTP authorization headers. The trace
records raw responses before tool calls run, so you can compare a malformed tool
argument with what the provider sent. A request that fails before the provider
returns a usable response records only its error.

Trace files contain private conversation and tool data, including returned
images and provider reasoning state. Delete them yourself when done. Each file
stops recording at 32 MiB with a `trace.truncated` entry, and the turn
continues. The chat shows any error creating or writing a trace. Clear the
setting when you no longer need traces.
