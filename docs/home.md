# Home and overview

`/` is each user's start page. The Karakuri name in the navigation goes
there, and so does sign-in. The flow list is at `/flows`.

## Start page

Each user picks a start page in **Settings, Profile**:

| Choice | `/` opens |
| --- | --- |
| Default | The overview for anyone who can read flows. Someone who cannot gets their only dashboard, or the dashboard list when they can see several. |
| Overview | The overview, whatever the user can read. |
| A dashboard | That dashboard. |

A chosen dashboard that was deleted, or is no longer shared with the user,
shows in the profile as "Unavailable dashboard", without its name, and `/`
falls back to the default. An API client sets the choice as the `start_page`
preference through `PUT /api/auth/preferences`. The value is absent,
`overview`, or a dashboard ID.

## Overview

The overview shows whether anything is broken and what runs next. It updates
as runs start and finish, as flows and dashboards change, and as warnings and
errors are logged, at most once every two seconds. It also refreshes every 30
seconds while the tab is visible, and again when the window regains focus. A user sees a section only when they can read what it
lists:

| Section | Shows | Needs |
| --- | --- | --- |
| Needs attention | The items below, or one all-clear line naming what was checked. | Any of the source permissions below. |
| Coming up | Schedule triggers firing and wait deadlines in the next 12 hours, soonest first, up to 20. A wait can be cancelled from here. | `api:flows:read`. Waits also need `api:runs:read`, and cancelling needs `api:runs:cancel` and edit on the flow. |
| Running now | Active runs, with cancel. Hidden when nothing runs. | `api:runs:read`. |
| Unfinished work | Up to 10 flows and 10 dashboards with undeployed draft changes or never deployed, newest change first. Hidden when empty. | Edit on each item, and `api:flows:write` or `api:dashboards:write`. |
| Quick launch | Pinned dashboards as tiles, in pin order. | `api:dashboards:read`. |
| Recent problems | The newest 8 warnings and errors of the last 24 hours, linked to Activity filtered to those levels. | `api:events:read`. |

The overview leaves out flows, waits, runs, and failures of a flow the user
holds no share on, as every other page does.

Pin a dashboard with the pin button in the dashboard list, or unpin it there
or from its tile. Tiles reorder with their arrow buttons. Pins are private to
each user; over the API they are the `pinned_dashboards` preference, an ordered
list of dashboard IDs.

## What needs attention

| Source | Item | Severity | Needs |
| --- | --- | --- | --- |
| Connections | An enabled connection in the error state, with its error and how long it has been down. | error | `api:connections:read` |
| | An enabled connection still connecting after two minutes. | warning | |
| | A connected connection that went down 3 or more times in the last hour, counted from the event log's connection failures. | warning | |
| Agents | An enabled agent without a live session, with when it was last seen. | error | `api:agents:read` |
| Flows | Failed runs of the last 24 hours, one item per flow and distinct error, with the count and a link to the flow. A flow whose newest finished run succeeded shows its failures as warnings. A run whose failure an error branch handled succeeded and is not counted. | error or warning | `api:runs:read` and view on the flow |
| Backups | The newest finished backup failed, or its archive failed verification. | error | `api:backups:read` |
| | The schedule is enabled and its first time after the newest successful backup passed more than an hour ago. | warning | |
| History | A writable time-series connection whose history writer is failing to deliver. | warning | `api:connections:read` |
| Updates | A newer Karakuri release this server has not installed, such as in a container, linked to how to update. It goes away once the running release is that new. | warning | `api:plugins:write` |

Errors come first, then the newest. A disabled connection or agent is not an
item, because disabling it was a choice. A device that went offline is not an
item either. Battery devices report offline routinely and would bury real
failures, so device availability stays in the event log and reaches recent
problems. Having no backup schedule is not an item.

When a source cannot be read, for example because the agent hub is not
running, the section says so by name instead of claiming all clear. The
unstable-connection check reads the event log, so it finds nothing when an
administrator stopped recording the connection category.

## REST and MCP

| Route | MCP tool | Returns |
| --- | --- | --- |
| `GET /api/home/attention` | `get_attention` | `items`, `checked`, `unchecked`, and `summary` with the all-clear figures. |
| `GET /api/home/upcoming?hours=12` | `list_upcoming` | `items` with `kind` (`schedule` or `wait`), `at`, the flow, the node, and for waits the `key` and `can_cancel`. `hours` is 1 to 24. |
| `GET /api/home/unfinished` | `list_unfinished_work` | `flows` and `dashboards`, each with `id`, `name`, `updated_at`, and `deployed`. |

The routes need a signed-in user or API key. Each source inside checks its own
permission, so a limited key sees only the sources its permissions cover.
Cancel a wait from `list_upcoming` with `cancel_flow_wait`, using its
`node_id` and `key`.
