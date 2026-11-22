# Browser prompts

Browser prompt is a server node under Utility. It opens a question in the selected
agent user's default browser and holds the incoming message until someone presses
a button. Parallel branches keep running. The held message leaves only through the
pressed button's output, and an unwired output ends the branch.

## Configure a question

Choose an agent that advertises Open URL. The agent needs an interactive desktop
session. Set Browser base URL if `server.base_url` is not reachable from that
machine. Use an http or https server origin without a path. For a remote host, use
the server's LAN address or DNS name, not localhost. The node needs no agent update
and no Karakuri browser login.

Title and Instructions accept `{{ }}` expressions against the incoming message:

```markdown
# Pump needs attention

The action failed: {{meta.error}}

1. Check the cable shown below.
2. Reconnect it and choose Retry, or choose Bypass to continue without this step.

![Cable location](attachment:cable.jpg)

[Service instructions](attachment:service-guide.pdf)
```

Instructions opens the script editor with Markdown highlighting, line numbers, undo
and redo, Find & replace, and Ctrl/Cmd+Enter to save. It highlights headings,
emphasis, links, and code, and fenced JavaScript, JSON, Python, shell, and YAML
blocks get their language's highlighting. Write and Preview share one document and
keep the cursor and editing history.

Attach image or file inserts a reference at the cursor. Pasting or dropping a file
also attaches it. Embed inserts an image, Link inserts a download, and Remove
deletes the attachment and its references. PNG, JPEG, GIF, and WebP embed. Every
other format, including SVG and HTML, downloads. An uploaded image's content must
match its declared format.

Attachments live in the node config, so drafts, frozen deployments, and exported
flows carry the same files. A node holds at most 20 files, each up to 8 MiB, with
32 MiB of decoded data in total. Rendered instructions are limited to 64 KiB and the
title to 1024 UTF-8 bytes. Each saved version stores its own copy of these bytes.

## Buttons and feedback

A new node has Retry, Bypass, and Stop buttons. Add your own or change the labels.
Each button's Output ID is its stable graph handle, so changing a label or
reordering buttons keeps the wiring. Changing an ID means rewiring it, and removing
a wired button invalidates its edge. A node has 1 to 12 buttons. Error is reserved
for failures.

A button's name does not decide what happens next. A Retry branch must lead to a new
downstream action or an intentional new run. A back-edge to a node that already ran
cannot retry it, because each node runs at most once per run. Bypass can lead to the
next step, and an unwired Stop output ends that branch. To suppress later prompts,
wire Stop to a flow or global variable setter and check that value before the next
request.

Written feedback adds a text field, and Require feedback rejects a blank response.
Feedback is limited to 4096 UTF-8 bytes. Outputs keep the payload, topic, metadata,
message identity, and group, and add this record:

```json
{"browser_prompt":{"id":"retry","label":"Retry","text":"Cable reconnected",
 "request_id":"...","answered_at":"2026-10-06T20:00:00Z"}}
```

The record is `meta.browser_prompt`. The original action's failure details stay in
the message on every button output.

## Timeouts and cancellation

Answer timeout defaults to 15m and accepts 1s through 24h. A timeout, invalid
config, disconnected agent, or failed browser launch goes to Error. A browser
process that starts has not necessarily loaded the page, and a page that cannot
reach the server eventually times out. Closing the tab also leaves the question
unanswered. A timeout never selects a default button.

A waiting question does not hold up other runs of the flow. Canceling the run,
disabling or redeploying the flow, or restarting the server invalidates unanswered
links, and requests do not resume after a restart. The wait happens on the server,
so once the browser has opened, an agent disconnect alone does not cancel the
request. A standalone node test still stops after at most two minutes, whatever
Answer timeout says.

## Request access

The browser URL holds a random single-use token. It grants access to that pending
question, its attachments, and one response, and nothing else. It cannot sign in or
call other APIs. Treat the link like a password while the question is open. The
prompt page has no app navigation and no account session. Invalid, answered,
expired, and canceled links return unavailable. Only that link serves the
attachments, and an answer cannot submit a button ID the node does not declare. At
most 128 questions can be pending at once, and requests past that fail through
Error.
