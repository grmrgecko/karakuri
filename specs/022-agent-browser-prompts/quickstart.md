# Verification

Build and test server/frontend with make dev and make test, restart
karakuri-dev.service, then verify systemd is active and localhost:8080 answers.

In an unsaved editor draft, add Browser prompt. Select an enrolled desktop agent,
set a reachable Browser base URL, write Markdown, attach a raster image and file,
and inspect Preview. Add a button, change its label, then remove it; verify canvas
outputs update and existing button IDs remain stable. Capture each interaction
and inspect the browser console.

Open Instructions and check Markdown highlighting for headings, emphasis, links,
inline code and fenced code blocks. Edit the text, switch to Preview and back,
then undo the edit. Find & replace opens search; Escape closes search while the
editor stays open. Select text and upload, paste or drop a file; its reference
replaces the selection. Remove an attachment and check its references disappear
from Write and Preview. Ctrl/Cmd+Enter saves the text and attachments; Cancel
discards dialog edits. Open an ordinary script and verify its highlighting,
documentation/test controls and Cancel behavior remain available.

Use MCP test_node to execute browser-prompt with a short timeout. On the agent,
verify the browser opens and instructions/image render. Submit written feedback
and a chosen button; assert only that output and original payload/error metadata
are returned. Download the file and compare bytes. Run a forked graph with one worker to
verify other work completes before answering, and wire Error to verify timeout
and launch failures reach it. Check invalid/duplicate token
responses and cancel an unanswered request. Report any saved test flows.
