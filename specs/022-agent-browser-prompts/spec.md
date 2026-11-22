# Agent browser prompts

## Scope

A flow asks a person for help in an enrolled agent's browser. The prompt displays
Markdown instructions, attached pictures and files, optional written feedback,
and author-defined buttons. A click resumes the same run through that button's
output. Button labels do not execute retry, bypass, or suppression themselves.

## User stories

### US1: Ask for help after a failure (P1)

Wire a failed action's error output into Browser prompt, choose a desktop agent,
and provide instructions and Retry, Bypass, and Stop buttons. The browser opens
without requiring a Karakuri account. Clicking Retry resumes the waiting run only
on its retry output, preserving the original payload and failure metadata.

Acceptance scenarios:
1. A connected agent opens one prompt per execution; its configured button outputs
   update live on the canvas when buttons are added, renamed, or removed.
2. A valid click completes the request once. A duplicate click cannot execute a
   second branch; other concurrent requests remain independent.
3. A disconnected agent or failed browser launch fails on the ordinary error path.
4. Expiry routes to Error with the input envelope; cancellation invalidates the
   request. Closing the browser alone does not answer it.

### US2: Explain the fix with pictures and files (P1)

Write Markdown and attach photos, diagrams, or a PDF. Insert an image reference
or a download link from the editor and inspect the rendered preview. Attachments
travel with the draft, frozen versions, and exported flow.

Acceptance scenarios:
1. Upload a PNG/JPEG/GIF/WebP and embed it in the instructions; attach another
   file and download it from the prompt. Bytes match the original upload.
2. Removing an attachment removes its references; invalid attachment data and
   excessive sizes are rejected during draft saving and execution.
3. HTML, script URLs, and files masquerading as embeddable images cannot execute
   in the prompt or editor. Non-image files download rather than render inline.
4. Instructions opens the same CodeMirror editor used for scripts with Markdown
   syntax highlighting, line numbers, search, undo and redo. Previewing and
   returning to Write preserves the document, cursor and editing history.
5. Picking, pasting or dropping a file inserts its reference at the editor
   selection. Removing it removes its references, and saving retains the edited
   Markdown and attachments. Cancel discards the dialog's changes.

### US3: Collect written feedback (P2)

Optionally show a feedback field, and require a response before a choice can be
submitted. The selected button ID, label, text, request ID, and answer time are
available in meta.browser_prompt without replacing the input payload.

## Requirements

- FR-001: Browser prompt selects an enrolled agent's registered Open URL capability.
- FR-002: Markdown/title interpolate against the incoming message; raw HTML is omitted.
- FR-003: One to twelve buttons have unique stable IDs and nonempty labels. Each
  creates a normal output carrying the incoming message with response metadata.
  Reserved ID error cannot be used. Label changes retain wiring.
- FR-004: The server parks the run without holding a worker slot. The human wait
  defaults to 15m and is bounded from 1s through 24h. Expiry routes to Error.
- FR-005: Each response capability is random, request-scoped, single-use, expires,
  and is invalidated on cancellation. It grants no access to other application APIs.
  Browser URL fragments carry the token; it is sent in Authorization headers.
- FR-006: Custom buttons have no implicit execution or future suppression policy.
  Flow authors wire the resulting actions explicitly.
- FR-007: Optional feedback is bounded to 4096 UTF-8 bytes; required feedback must
  contain non-whitespace text.
- FR-008: At most twenty attachments per node, each at most 8 MiB, with at most
  32 MiB decoded total. Stored keys contain only letters, digits, dot, dash, and
  underscore and are 1-64 characters. Attachment bytes use strict base64.
- FR-009: Raster images embed only when their detected MIME type matches their
  declared PNG/JPEG/GIF/WebP type. Other files have download disposition and nosniff.
- FR-010: The editor supports file picking, image/file references, removal, and
  Markdown preview. Instructions MUST reuse the script editor with Markdown
  syntax highlighting and retain its search, undo/redo and save shortcut. Preview
  MUST preserve the document, cursor and editing history. Attachment insertion
  MUST replace the current selection. The node's buttons have an editable list
  and live output ports.
- FR-011: Requests are transient; restart cancels waits and makes old URLs unusable.
  The feature does not change enrollment, encryption, or stored database formats.
- FR-012: Meaningful tests cover response isolation, duplicate/invalid/expired
  responses, cancellation, timeout, launch failure, attachments, and port validation.
- FR-013: Rendered instructions are bounded to 64 KiB and titles to 1024 UTF-8
  bytes. The server accepts at most 128 pending questions.

## Edge cases and outcomes

A logged-in browser sees the same limited prompt as an anonymous browser. Required
feedback is checked by the server. Invalid choices leave the request available.
A response and timeout racing are resolved once under the request lock. Attachment
links have no authority beyond their own pending request. No durable wait recovery
is promised. A button named Retry does not permit an otherwise invalid graph cycle.

Successful verification must demonstrate an agent browser opening the prompt,
correct chosen output and feedback, byte-identical attachment downloads, live canvas
port changes, and passing server/frontend Makefile checks.
