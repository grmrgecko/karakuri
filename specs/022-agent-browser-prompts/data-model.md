# Data model

BrowserPromptConfig has agent ID, optional browser_base_url, title, markdown,
buttons [{id,label}], attachments {key:{filename,type,data}}, timeout, feedback
and require_feedback. Button IDs are stable, unique, 1-64 ASCII letters/digits/
dot/dash/underscore, excluding error. A config has 1-12 buttons.
Title and Markdown interpolate; attachments do not. Feedback is at most 4096 bytes.

Attachments have safe keys of 1-64 characters, filename at most 255 bytes without
control characters or path separators, MIME type and strict base64 bytes. Images
embed only for matching PNG/JPEG/GIF/WebP signatures. Other files download.
Limits are 20 entries, 8 MiB each, 32 MiB total.

Transient request states are pending, answered, expired and canceled. Only pending
accepts a response. Invalid responses preserve pending. The first valid response
atomically records {id,label,text,request_id,answered_at} and wakes the node.
Tokens authorize only a pending request and are never included in run outputs.
