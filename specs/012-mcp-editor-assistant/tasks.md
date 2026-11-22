# Tasks: Assistant round budget and continuation

- [x] T001 Update the loop to allow 50 rounds and emit completed replay history with a continuation notice at the limit (FR-017).
- [x] T002 Display the notice without clearing replay history; encourage batching related graph edits (FR-016, FR-017).
- [x] T003 Verify the HTTP streaming boundary past 20 rounds, at 50 rounds, and on continuation with a synthetic provider and real MCP handlers.
- [x] T004 Build and test server/frontend, check the editor in the browser, update the guide, and verify the running development service.

Verification on 2026-10-05: server and frontend builds and tests passed. The
HTTP fixture completed 21 tool rounds, paused at 50 with complete replay history,
and received that history on continuation. The browser stream fixture verified
the notice, follow-up request bodies, ordinary history retention, and Stop reset;
its console had no errors during those checks. No test flows were added to the
development database. The restarted development service was active and returned
HTTP 200 at localhost:8080. The original model request was not rerun.

## Authoring diagnostics and live model evaluation

- [x] T005 Preserve public validation details, validate stored drafts, and include strict diagnostics in successful draft writes.
- [x] T006 Describe patch fields, switch rule objects, and HA State payloads; update authoring examples.
- [x] T007 Add opt-in bounded private turn traces with raw provider responses and decoded calls.
- [x] T008 Verify error recovery, schema contracts, and trace contents through real handlers; build and test.
- [x] T009 Back up and clear the selected draft, retry the original prompt, inspect traces and the graph, and report the result without deployment or device execution.

- [x] T010 Enforce explicit execution opt-in in the editor and server after the live model attempted unsolicited deployment; verify rejection and allowed execution.

Verification on 2026-10-05: server and frontend builds/tests passed. MCP fixtures
cover actionable malformed-edge errors, unchanged drafts after rejection, repair,
and strict diagnostics on incomplete saves. Assistant fixtures cover trace contents,
private file permissions, bounded valid NDJSON, and rejection of an invented deploy
call with execution disabled. Browser request inspection verified false/true opt-in
values and reset on New chat; the console was clean during those checks.

The unchanged lighting prompt was evaluated twice on the user's cleared Test flow
using llama-swap/gemma-4-26B. The first run built a valid graph in 14 rounds, then
unexpectedly deployed and enabled it. It was disabled; no runs were recorded.
The previous deployment's content was restored as version 3, preserving immutable
history, and the generated graph was saved back to the draft. That result prompted
the server-enforced execution opt-in. The second run, with execution disabled,
completed in 12 rounds after recovering from one unknown node-type lookup. Its
four-node, three-edge graph passed strict validation. Isolated switch checks routed
on/off separately and dropped unknown/unavailable states without calling devices.
The flow remains disabled; the latest draft differs from the restored deployment.

The second turn's private trace is under `build/var/assistant-traces/`; evaluation
requests, streamed events, and draft backups are under `build/var/assistant-eval/`.
All streamed provider tool arguments matched Karakuri's decoded calls in that trace.
No malformed argument strings recurred, so the earlier corruption's origin remains
unproven. No new flows were created. Trace logging remains enabled in the local
untracked development config for further diagnosis.

## Dialog movement and view retention

- [x] T011 Retain the dialog view across close/reopen and add bounded pointer/keyboard movement (FR-022).
- [x] T012 Build/check the frontend, verify movement and retained scroll/details in the browser, and check the running service.

Verification on 2026-10-05: frontend and server builds/checks passed. A browser
response fixture populated the real transcript without invoking the model or changing
the graph. Dispatched pointer events moved the dialog, and native keyboard input moved
it by ten pixels. Closing and reopening retained identical coordinates, a 190-pixel
scroll offset, and an expanded tool result. Resizing to 700x600 and 390x700 kept the
dialog within the viewport. New chat reset the transcript and Escape closed the dialog.
Screenshots were captured after interactions; no new console errors appeared during
these checks. The fixture was removed and no test flows were created. The development
service remained active and answered HTTP 200.

## Compose during an active turn

- [x] T013 Keep the composer editable while preserving the active-turn submission guard (FR-023).
- [x] T014 Verify typing, blocked Enter submission, and retention after completion, failure, and Stop; build/check and verify the service.

Verification on 2026-10-06: server/frontend builds and checks passed. A delayed
browser stream fixture confirmed the textarea stays editable, Enter sends no second
request while busy, and the typed draft survives normal completion, Stop, and a
stream error. Send became available after each turn settled. Screenshots were taken
after interactions and no new console errors appeared. The fixture was removed;
no model requests or dashboard edits were made. The service was active and returned
HTTP 200 after restart.

## Nonmodal window and resizing

- [x] T015 [US3] Open a nonmodal assistant with scoped focus and Escape handling in frontend/src/lib/components/EditorAssistant.svelte; scope canvas shortcut suppression in frontend/src/lib/components/editor/EditorShell.svelte (FR-024).
- [x] T016 [US3] Add bounded pointer/keyboard resizing, flexible transcript height, and retained dimensions in frontend/src/lib/components/EditorAssistant.svelte (FR-022, FR-024).
- [x] T017 [US3] Update docs/ai.md; build/check server and frontend, verify both editors, resize/reopen/viewport/focus behavior, capture screenshots, inspect the console, and record evidence in specs/012-mcp-editor-assistant/tasks.md.

Verification on 2026-10-06: server and frontend builds/checks passed; Svelte reported
zero errors and warnings. The collaborative browser confirmed the dialog is nonmodal,
background node selection opens flow configuration, and background widget selection
opens dashboard configuration while chat stays open. Native keyboard input resized
620x760 to 570x710, and closing/reopening retained identical dimensions and position.
Dispatched pointer events resized it to 400x550 and moved it; viewport checks at
700x600 and 390x700 kept all bounds inside the viewport. The transcript changed
height while the composer stayed visible. Clicking the resize handle gave it focus.
Canvas save shortcut events were handled outside the assistant and ignored inside
it. Native Escape cleared the canvas selection without closing chat; Escape inside
chat closed it. A delayed browser response fixture confirmed completion preserved
background focus. Screenshots were captured after interactions. Console inspection
found no new errors during the editor checks; the flow list had existing missing-run
404s. The fixture was removed, no model requests or document edits were made, and no
test flows were added. The restarted service was active and returned HTTP 200.

## Kuri identity

- [x] T018 [US3] Name the editor assistant Kuri in the shared launch button, window title, reply labels, accessibility labels, and every server-generated system prompt (FR-025).
- [x] T019 [US3] Update the guide, spec, and plan; build/test server and frontend, verify both editors in the browser, capture screenshots, inspect the console, and check the restarted service.

Verification on 2026-10-06: server and frontend builds/tests passed; Svelte reported
zero errors and warnings. Browser checks confirmed **Ask Kuri** opens a window titled
**Kuri** in both flow and dashboard editors, with Kuri conversation/composer labels.
Source review confirmed every authoring turn starts with Kuri's name and editor role
in the system prompt. Screenshots were captured after interactions; no new console
errors appeared. No model requests, document edits, or test flows were made. The
restarted development service was active and returned HTTP 200.
