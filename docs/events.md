# Event log

The event log records what happened in the house and in karakuri, what it
happened to, and who or what did it: a person on a dashboard, an API key, a
flow and its run, an agent, or karakuri itself. Read it under **Activity** in
the navigation, on dashboards with the activity widget, in flows with the Log
Event and Event Logged nodes, or through the REST routes and MCP tools listed
at the end.

## What is recorded

| Category | Entries |
| --- | --- |
| Device | A control's value changing, with old and new values, and every press of a buttons control, including the same button again; a command sent to a device; a device going offline and coming back; state changes of the Home Assistant entities listed in the settings. |
| Connection | A connection failing, with its error, connecting, reconnecting after a failure, and being stopped. |
| Flow | A run failing, with its error and a link to the run; a flow deployed, enabled, or disabled. |
| Agent | An agent enrolling, connecting, and disconnecting. |
| Security | Sign-ins, failed sign-ins, sign-ins refused after repeated failures, sign-outs, revoked sessions, a refresh token used twice; users, passwords, groups, memberships, roles, permissions, role assignments, and API keys created, changed, or deleted; connections created, changed, or deleted; agents edited, deleted, or revoked, enrollment tokens, CA rotations; backups downloaded and restored; the encryption key rotated. |
| System | Backups finishing or failing, the server starting and stopping. |
| Custom | Entries flows add with Log Event. |

The log leaves out sensor readings, so a temperature device reporting every few
seconds adds nothing. It also leaves out device attributes
other than availability, Home Assistant entities not listed in the settings,
and controls excluded in the settings. Exclude a control that mirrors a busy
sensor to keep it out of the log.

Secrets never appear. A failed sign-in records the username as typed, never
the password. A change lists the names of the fields that changed, never their
values, and a changed connection secret appears only as `credentials`.

## Who did it

Every entry names its actor:

| Actor | Shown as |
| --- | --- |
| A signed-in user | Their username, with `on a dashboard` for a dashboard and `through MCP` for MCP. |
| An API key | The key's name with its owner, such as `kitchen tablet (admin)`. |
| A flow | The flow's name, with a link to the run. |
| An agent | The agent's name. |
| karakuri | Schedules, recovery, connection status, and the command line, named with the operating system user for `config rotate-key` and `backup restore`. |
| Someone | An address that has not signed in, such as one making a failed sign-in. |

## Repeats

When the same thing happens to the same subject, by the same actor from the
same address, within a minute, it folds into the subject's latest entry as a
count, shown as `×12`. A connection retrying every few seconds is one entry
with a count. A change in between is never hidden. A light turned on, off, and
on again is three entries, and the same change made by someone else, or a
failed sign-in from another address, gets its own entry.

## Activity

The page lists entries newest first. Filter by category, level, time range,
subject (pick a subject's name in any entry), or text in the message, the
subject, or the actor. New entries appear while the page is open, and older
entries load a page at a time.

Only holders of `api:events:audit` see security entries.

## Settings

Holders of `api:events:write` change these under **Settings, Event log**:

| Setting | Meaning |
| --- | --- |
| Keep entries for | Days per category, 1 to 3650. 30 by default, and 365 for security. |
| Most entries kept | 10,000 to 10,000,000, one million by default. Past it the oldest entries go first, never security entries, which only their own age removes. |
| Controls not logged | Control IDs, one per line. |
| Home Assistant entities logged | `connection-id entity_id` per line, at most 500. Each is logged when its state changes. |

Karakuri removes old entries when the server starts and every six hours.

## Dashboards

The **Activity** widget lists recent entries for the controls, devices, and
Home Assistant entities its rows name, with who did it, and updates live. Each
row is a display-only binding, so a viewer of a shared or public dashboard
reads those subjects' entries and nothing else, and never security entries.

## Flows

**Log event** (`log-event`) adds a custom entry. It takes a message, which
interpolates message properties such as `{{payload.room}}`, a level, an
optional control or device the entry is about, and optionally the payload as
details. The entry names the flow and run, and the message passes through.

**Event logged** (`event-logged`) starts a run when a new matching entry is
logged. It filters by categories, levels, a control or device, and text in the
message. Folded repeats do not start another run. Entries the flow itself
caused never start it, so a flow that writes a control and listens for control
entries does not start itself. Without categories it matches everything except
security, and naming security needs the flow's owner to hold
`api:events:audit`. The payload is the entry, with `category`, `level`,
`message`, `subject_kind`, `subject_id`, `subject_name`, `actor_kind`,
`actor_name`, `count`, `details`, and `at`.

To hear about failed sign-ins, pair Event Logged on category security and
level warning with a notification node.

## Permissions, REST, and MCP

| Permission | Allows | Held by default |
| --- | --- | --- |
| `api:events:read` | Every category except security, and reading the settings. | Admin and User roles |
| `api:events:audit` | Security entries. | Admin |
| `api:events:write` | Changing the settings. | Admin |

| Route | Permission |
| --- | --- |
| `GET /api/events` | read, or a dashboard showing the subjects asked for |
| `GET /api/events/settings` | read |
| `PUT /api/events/settings` | write |

`GET /api/events` takes these parameters and answers `{events, next_before}`:

- `category`, `level`, and `subject`, each repeatable. A subject is `kind:id`,
  such as `control:<uuid>` or `ha_entity:<connection>/<entity>`.
- `actor`, as `kind` or `kind:id`.
- `q`, for text.
- `from` and `to`, as RFC 3339 or `now-24h`.
- `before`, the `next_before` of the previous page.
- `limit`, 1 to 500.

Asking for security without audit is refused.

Karakuri publishes live entries on the WebSocket topics `events` (read),
`events:security` (audit), and `events:subject:{kind}:{id}` (read, or a
dashboard showing the subject). A new entry arrives as `event`, and a repeat
folding into one arrives as `event_update`.

MCP has `list_events`, with the same filters, and `get_event_settings`. No
tool writes the log. Flows write it with Log Event.

The log is in the database, so backups carry it, and a restore is the first
entry of the restored installation.
