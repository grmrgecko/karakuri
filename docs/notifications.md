# Notification services

**Settings, Notifications** holds provider credentials and the destinations
flows may send to. Add a service, fill in its credential fields, and add at
least one endpoint. An endpoint has a stable ID, a label, and a provider
target, and the form describes the target's format. A flow picks the service
and one endpoint ID.

These services can deliver: Amazon SES, Amazon SNS, Bark, DingTalk, Discord,
Firebase Cloud Messaging, Google Chat, HTTP webhook, Lark custom app, Lark
webhook, LINE Messaging API, SMTP mail, Mailgun, Mailtrap, Matrix, Mattermost,
Microsoft Teams, PagerDuty, Plivo SMS, Pushbullet, Pushbullet SMS, Pushover,
Reddit, Rocket.Chat, SendGrid, Slack, Syslog, Telegram, TextMagic, Twilio SMS,
Twitter / X, Viber, Web Push, and WeChat.

LINE Notify is listed as unavailable because LINE ended the service on March
31, 2025. Use LINE Messaging API instead. WhatsApp is listed as unavailable
because it cannot deliver messages. Neither can be saved.

## Karakuri dashboard

The catalog also lists **Karakuri dashboard**. Its endpoint target is a
dashboard ID or slug. A send raises an alert on every open viewer of that
dashboard, the same alert a Dashboard Command node shows, with the subject as
the alert title and the message as its body. Its settings are:

- `style`, `toast` by default, or `fullscreen`.
- `level`, `info` by default, `success`, `warning`, or `error`.
- `duration` in seconds, 0 to 86400, default 10. 0 keeps the alert until a
  command clears it.
- `speak`, `true` to have open viewers read the alert aloud.

A send to a deleted dashboard fails with an error naming it. Anyone who may
configure notification services can address any dashboard this way, the same
reach a flow has, and everyone who may view the dashboard sees the alert text.
See [dashboard security](dashboard-security.md).

## Settings and secrets

A service is a connection of kind `notification`. The server's encryption key
encrypts its credentials. The connection read, write, and invoke permissions
govern discovery, configuration, and direct sends.

Reads mask secret credential fields and every endpoint target. Saving the mask
keeps the stored value. Target masks follow endpoint IDs when rows are
reordered. Changing an ID means entering its target again and updating the
flows that reference it. Removing an endpoint revokes its old ID. The service
type is fixed after creation, so create another service to use a different
provider.

The connection APIs accept configuration of this shape:

```json
{
  "service": "http",
  "credentials": {
    "method": "POST",
    "headers_json": "{\"Authorization\":\"Bearer token\"}"
  },
  "endpoints": [
    {"id": "alerts", "label": "Home alerts", "target": "https://example.com/notify"}
  ]
}
```

| Limit | Value |
| --- | --- |
| Endpoints | 1 to 100 per service |
| Endpoint ID | 1 to 64 letters, digits, underscores, or hyphens, unique within the service |
| Label | 120 bytes |
| Target | 32 KiB |

Google services take inline service account JSON, not credential filenames or
ambient credentials. SMTP uses `host:port`, uses STARTTLS when the server
offers it, and takes one configured email recipient per endpoint. A Syslog
endpoint gives an explicit network and address in JSON.

## Send notification node

The `notification` node is in the **Network** folder. Pick a service and an
allowed endpoint, and give an optional subject and a required message. Subject
and message take `{{ }}` expressions and the editor's ordinary parameter-port
bindings. An endpoint binding must still resolve to a configured ID. A raw URL
or recipient cannot stand in for one.

For example, use subject `Room {{ payload.room }}` and message
`Temperature {{ payload.temperature }}`. A successful send passes the input
message to `out`. A failure goes to the ordinary `error` port. A provider
accepting a message does not prove that a person received it.

| Limit | Value |
| --- | --- |
| Subject | 4 KiB, no line breaks or NUL |
| Message | 64 KiB |
| Timeout | 1 to 120 whole seconds, default 30 |
| Concurrent sends | 4 |

Notification failures never retry automatically, even when a graph asks for
retries. Karakuri replaces provider errors with public errors that contain no
targets, credentials, response bodies, or message content.

A send reaches only the selected endpoint. Providers that send over HTTP do
not follow redirects. Some providers cannot be interrupted once a send starts,
so a timeout or failure can leave delivery uncertain. Do not treat a timeout as
proof that no message was sent. A timed-out send keeps its place among the four
until the provider call ends.

## API and MCP

- `GET /api/notification_services` returns the catalog and field metadata.
- `GET /api/connections/{id}/notifications/endpoints` returns endpoint IDs and
  labels.
- `POST /api/connections/{id}/notifications/send` takes `endpoint`, `subject`,
  `message`, and an optional integer `timeout`.

The MCP tools are `list_notification_services`, `list_notification_endpoints`,
and `send_notification`. Create and edit services with the connection tools
`list_connections`, `save_connection`, and `delete_connection`. Discovery needs
`api:connections:read`, and direct sends need `api:connections:invoke`. A
flow's Send notification node needs neither. Whoever may edit and deploy the
flow decides what it sends, as with any node that uses a connection.
