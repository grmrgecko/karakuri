# Tasks: Agent browser prompts

**Feature**: specs/022-agent-browser-prompts

## Phase 1: Preparation

- [x] T001 Read existing contracts and produce specs/022-agent-browser-prompts design artifacts.

## Phase 2: Shared contracts

- [x] T002 Implement button/config/attachment contracts and validation in pkg/flow/browserprompt.go, pkg/flow/ports.go and pkg/flow/validate.go.
- [x] T003 Implement transient requests and capability HTTP endpoints in pkg/browserprompt and pkg/api/browserprompt_handler.go.

## Phase 3: US1

Goal: An agent opens a question and the same run resumes on the selected output.
Independent check: MCP executes the node, browser chooses, original data returns.

- [x] T004 [US1] Implement BrowserPromptNode in pkg/nodes/core/browserprompt.go and register it.
- [x] T005 [US1] Implement agent picker/button editor and live ports in frontend/src/lib/components/editor/ConfigForm.svelte and frontend/src/lib/utils/nodePorts.ts.
- [x] T006 [US1] Implement standalone prompt route in frontend/src/routes/BrowserPrompt.svelte and frontend/src/App.svelte.
- [x] T007 [US1] Verify response isolation, duplicates, launch errors, cancellation, timeout errors and failure data with contract tests in pkg/browserprompt and pkg/nodes/core.

## Phase 4: US2

Goal: Portable attachments embed or download from preview and prompt.
Independent check: Upload an image/file, inspect preview, download matching bytes.

- [x] T008 [US2] Implement Markdown attachment editor/preview in frontend/src/lib/components/editor/MarkdownField.svelte and frontend/src/lib/utils/promptAttachments.ts.
- [x] T009 [US2] Verify attachment validation, MIME/disposition and scoped download handlers in pkg/flow and pkg/api tests.

## Phase 5: US3

Goal: Optional required written feedback reaches selected output metadata.
Independent check: Server rejects blank required text and preserves a valid answer.

- [x] T010 [US3] Implement feedback validation and response metadata in pkg/browserprompt and frontend/src/routes/BrowserPrompt.svelte.

## Final verification and documentation

- [x] T011 Update docs/browser_prompts.md, docs/flow-authoring.md, docs/agent-nodes.md and spec indexes.
- [x] T012 Run relevant make dev and make test targets; verify agent/browser/attachments/canvas/console.
- [x] T013 Restart and verify karakuri-dev.service and HTTP listener, review diff and commit verified feature.

## Markdown editor follow-up

- [x] T014 [US2] Reuse ScriptEditor for Markdown instructions, add its Markdown language mode and themed syntax highlighting, and preserve preview and attachment controls.
- [x] T015 [US2] Verify highlighted Markdown, editing/search/undo, preview switching, attachment insertion/removal, save/cancel and ordinary script editing in the browser; run frontend Makefile checks, restart the service and commit.

## Dependencies

Contracts precede requests/node/UI; editor and prompt page use the same attachment
shape. US2 and US3 depend on US1 scaffolding. Independent server/frontend builds
may run in parallel. Research was delegated under the planning skill; implementation
remains local. No branches or external publication are authorized.

## Verification evidence

- `make dev TARGET=server` and `make dev TARGET=frontend` passed.
- `make test TARGET=server` passed, including a real engine/broker check with one
  worker: parallel work completes while the question remains pending; timeout and
  launch failure execute the wired Error branch.
- `make test TARGET=frontend` reported zero errors and warnings.
- MCP `test_node` opened Firefox on the enrolled Windows 11 agent. Instructions
  interpolated the failure, the attached JPEG rendered, required feedback was
  entered, and Bypass returned only its output with the original payload, topic,
  failure metadata and answer text. An unanswered request failed after 110s.
- The downloaded guide SHA256 matched the uploaded bytes:
  `0d7b29e9ccfe2d46c77acbfcbcb6606711dc628495d656e35349a08b3cb4748f`.
- Collaborative browser interactions verified live button add/rename/remove,
  stable output IDs, agent choices, and Markdown image/file upload and preview.
  The console was clear during feature interactions. The preview screenshot tool
  repeatedly failed, so canvas screenshot evidence is unavailable. Windows
  desktop captures verified the actual prompt and successful response.
- Editor autosave initially persisted two temporary prompt nodes in the existing
  disabled Agent test localhost draft. Both were removed through MCP and the
  editor reloaded the shared draft. No new flows or retained test nodes remain.
  Test browser windows were closed.
- The development service was restarted, reported active, and returned HTTP 200.

### Markdown editor follow-up

- Frontend build and checks passed with zero Svelte errors or warnings.
- Browser DOM checks confirmed themed headings, bold/italic text, links, inline
  code, strikethrough and fenced JavaScript highlighting in ScriptEditor.
- Write/Preview retained the same CodeMirror surface, selection and undo history.
  Find & replace opened search; Escape closed search without closing the dialog.
- File picking replaced selected text with an image reference. File paste and drop
  inserted download links. Preview showed a decoded image and file download; Remove
  deleted the attachment and its reference from both source and preview.
- Ctrl+Enter saved Markdown and both remaining attachments, confirmed through MCP.
  Cancel left saved Markdown and an ordinary script unchanged. Script highlighting
  and documentation/test controls remained present. Console checks were clear.
- The temporary disabled `Markdown editor verification` flow was deleted after
  the checks. Screenshot capture continued to fail in the collaborative browser.
