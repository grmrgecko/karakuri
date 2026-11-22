# Plan: Agent browser prompts

## Technical context

Go server, SQLite graph storage, Svelte 5 editor. Reuse goldmark through docs.Render,
the default node registry, enrolled agents' Open URL nodes, and SignalParker.
The Markdown editor adds @codemirror/lang-markdown to the existing CodeMirror
dependencies. No agent binary changes, database tables, or identity migration.
Work remains on master. Existing feature contracts 002, 003, and 009 apply.

## Constitution check

Failure routing and bounded waits are explicit. Drafts remain independent of
frozen deployments. Prompt tokens confer only request-local authority. Existing
state remains readable. Makefile tests/builds, MCP functional checks, browser
canvas checks, and one real agent launch are required before committing.
All checks pass at design time; no exceptions.

## Architecture

pkg/flow/browserprompt.go owns config, button derivation, and attachment validation.
Attachments live in node config as named {filename,type,data} records, data strict
base64. They travel with frozen graphs and exports. Limits bound decoded storage:
20 entries, 8 MiB each, 32 MiB total. Embedded images must match detected MIME.

pkg/browserprompt owns a mutex-protected transient request manager. Each request
has a random 256-bit token, context, expiry, rendered HTML, frozen choices and
attachments, and a completion channel. Response validation and state transitions
are atomic. At most 128 pending requests are accepted. Requests are removed on
completion, cancellation, timeout, or launch failure. Restarts invalidate URLs.

Core BrowserPromptNode creates a request, resolves the selected agent's Open URL
node from the current registry, and executes it with a Config/Ports override.
Only an empty launch envelope and URL go to the agent. The original envelope stays
on the server. The human wait uses the original SignalParker, releasing its slot.
Timeout and launch/config failures use Error. Parallel branches continue while
this node waits. Responses preserve incoming data and add meta.browser_prompt. No automatic retry or suppression.

The URL uses server.base_url by default with an optional per-node browser_base_url
for remote hosts, and /prompt#token. Dedicated HTTP handlers accept bearer tokens
only for prompt reads, one response, and request attachments. No-cache/no-referrer
headers apply. Other files download with nosniff and sandbox; raster images can
render inline. The standalone Svelte prompt route skips application authentication,
WebSockets, themes API, and navigation. Files are fetched with bearer headers and
mapped to blob URLs, revoked on teardown. Tokens never go into attachment URLs.

ConfigForm adds an agent browser picker from existing catalog capabilities, a
Markdown control with rendered preview and image/file attachment upload, and a
button list. Both frontend and server derive ports from stable button IDs.
Changing labels/reordering retains wiring. Removing a wired choice surfaces the
existing edge validation. Markdown uses the existing safe server renderer.

MarkdownField lazily loads ScriptEditor, the component already used for scripts.
ScriptEditor accepts toolbar, preview and attachment snippets plus file events and
document-change callbacks. Its CodeMirror view stays mounted while Preview hides
the editing surface, preserving selection and undo history. Attachment insertion
and removal dispatch editor transactions rather than modifying a textarea.
The shared language extensions add Markdown and fenced code modes; the shared
theme styles headings, emphasis, links and code. File paste/drop handling applies
only when the caller supplies an attachment callback. Scripts keep their existing
save, validation, documentation and test controls.

## Validation

Real HTTP handlers and in-memory registry cover token isolation, malformed replies,
feedback limits, MIME/disposition, duplicates, expiry/cancellation and cleanup.
Node tests use the real engine and broker with only the external browser opener
replaced. They verify preserved failure data, one-worker parking, selected output
and Error routing. Flow tests exercise
strict/lenient config and stable ports. Frontend checks run through make test.
MCP starts a prompt on an enrolled desktop; browser checks instructions, response,
attachments, button changes, canvas and console. Visual checks leave no prompt nodes in saved drafts.

## Design review

No DB migration. Portable attachments increase graph/version size within stated
limits. Concurrent prompts are separate windows/tabs as the default browser chooses.
Agent disconnect after successful launch does not cancel a server-owned request.
Standalone node tests retain their existing two-minute wall-clock cap. Ordinary
back-edges cannot retry an already-dispatched action; document downstream actions.
