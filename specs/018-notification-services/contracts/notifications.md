# Notification contracts

Settings reuse `/api/connections` CRUD with kind `notification` and config
`{service, credentials, endpoints: [{id,label,target}]}`. MCP uses save_connection.
Read/write permissions and masked-secret updates match ordinary connections.

`GET /api/notification_services` and MCP `list_notification_services` expose
catalog IDs, labels, credential fields, target formats and unavailable reasons.
`GET /api/connections/{id}/notifications/endpoints` and MCP
`list_notification_endpoints` return only endpoint IDs/labels. Discovery requires
connection read permission.

`POST /api/connections/{id}/notifications/send` and MCP `send_notification` accept
`endpoint`, `subject`, `message` and `timeout` (integer 1–120, default 30).
Direct actions require connection invoke permission and enforce the same allowlist
as nodes. Success means the selected provider call accepted the send; it does not
prove the recipient received or read it. Timeout/failure may leave delivery uncertain.

`notification` is an impure Network node with Any input/output and an Error
output. Fields are `connection`, `endpoint`, `subject`, `message`, `timeout`.
Subject/body support normal interpolation. Endpoint IDs are literal, including
when supplied through parameter inputs. No raw receiver URL or credential can
be supplied by the node. On success the input envelope passes through unchanged;
on failure the engine routes a sanitized nonretryable error.
