# What a dashboard exposes

The people who look at a dashboard usually hold no permissions: a housemate you
shared it with, a wall panel in the hallway, anyone with the link once it is
public. They do not need `api:connections:read`, which would open the whole
Home Assistant instance, every MQTT device, and every control. What the
dashboard shows decides what they can see and press.

**The layout is the grant.** Placing a widget bound to `light.lamps` lets the
dashboard's viewers read `light.lamps`, and nothing else on that connection. A
dashboard with no widget on a connection exposes nothing on it. Everything below
follows from that rule.

Which layout counts depends on deployment:

- Once a dashboard is deployed, viewers get the deployed version's bindings.
  Draft changes expose nothing until you deploy them.
- Authors with edit access also get their draft's bindings while editing.
- A dashboard that was never deployed shows its draft. Edits to an undeployed
  public dashboard change what the public can read as soon as they are saved, so
  deploy a public dashboard before you start reworking it.
- If the deployed version is damaged and cannot be loaded, viewers see an error.
  They never fall back to the draft.

## Who can see and do what

| Viewer | Read what the dashboard shows | Operate what it shows | Anything it does not show |
|---|---|---|---|
| Anonymous visitor | Only if the dashboard is public | Only with public control on | No |
| Signed-in user who sees it only because it is public | Yes | Only with public control on | No |
| Read share (*View only* in the share dialog) | Yes | Only with public control on | No |
| View, edit, or admin share, and the owner | Yes | Yes | No |
| A render, including one opened with a render key | Yes | No | No |
| Holder of the subsystem permission | Yes | Yes | Yes, through that permission |

**Public control** is off by default and sits beside **Public dashboard** in the
dashboard's settings. Publishing a dashboard shows the house; it does not hand
out the switches. Turn public control on only when anyone with the link should
be able to press things.

Sharing with a named person at view level or above lets them operate the
dashboard, and runs they start carry their name. Use a **read** share for
someone who should watch without pressing, such as a guest or a panel in a
shared room. Signing in alone creates no share.

Viewers who may not operate see readings in place of switches, sliders, and
buttons. The server refuses their presses either way, so a modified page gains
nothing.

### Share levels

Share levels are `read`, `view`, `edit`, and `admin`. Each includes the ones
below it. A share made without a level is `view`, and any other value is
refused.

| Action | Needs |
|---|---|
| View the dashboard | `view` (or `read`), public, or ownership |
| Edit, place widgets, deploy | `edit` |
| Change sharing, delete | `admin` |
| Create a dashboard in a group | `edit` on the group |
| Move a dashboard to another group | `admin` on the dashboard and `edit` on the target group |
| Reorder within a group | `edit` on each dashboard |

A dashboard inherits its group's shares, and a group's owner holds `admin` on
every dashboard in it, so moving a dashboard between groups changes who can see
it.

`api:dashboards:write` lets a user create dashboards of their own. It does not
let them write into someone else's. `api:dashboards:manage` does, and the Admin
role's `api:*` includes it; `api:dashboard-groups:manage` does the same for
groups. A private dashboard is missing from the list of anyone without access,
and opening its address answers not found.

Viewers who are not the owner and lack `api:dashboards:read` see the owner only
as an ID and username. Publishing a dashboard does not reveal the owner's email
address or preferences.

## What each binding lets viewers read and operate

A binding is the source a widget row, tile, condition, or cue names. Viewers
reach a binding's connection through the dashboard, so a dashboard deploys only
when its deployer may use every connection its entity, device, camera,
condition, variable, and chart bindings reach. Saving a chart query or a
time-series variable query likewise needs the connection shared with you. Each
source grants this much to the dashboard's viewers:

| Source | Viewers may read | Viewers may operate, with the control policy above |
|---|---|---|
| `ha` | The bound entity's state, and where a widget shows them its history, its to-do items, a camera's snapshots and live stream, and a media player's cover art | Services of the entity's own domain, aimed at that entity only |
| `control` | The bound control | Setting the bound control |
| `mqtt` | The bound device's name, controls, buttons, and current values | Commands to the bound device |
| `flow-trigger` | Nothing | Firing the bound trigger names, each one separately |
| `nvr` | The bound camera's images, stream, and events, or every camera of the connection with `camera: "*"` | Nothing |

Some consequences:

- **Lists are filtered, not refused.** A viewer asking for the house's entities,
  controls, or devices gets only the ones the dashboard binds, so a public
  dashboard does not publish the inventory of the house.
- **Home Assistant calls stay on the bound entity.** A dashboard can call
  `light.turn_on` on `light.lamps`, but it cannot call a service without a
  target, a service aimed at an area, device, floor, or label, or a service of
  another domain. Script services are limited to `script.turn_on`,
  `script.turn_off`, and `script.toggle` on the bound script. That keeps a viewer
  from turning a lamp binding into a whole-room call, running an unrelated
  script or shell command, or purging the entity's history.
- **MQTT device details stay private.** Viewers do not see a device's state or
  payload scripts, its connection, or its topics.
- **Credentials stay on the server.** Karakuri fetches camera images,
  streams, and Home Assistant-hosted cover art with the connection's own token
  and relays them. The viewer's browser loads cover art hosted elsewhere from
  that host.
- **Each viewer's camera holds a stream.** A live Home Assistant camera keeps
  one stream open per viewing browser for as long as it is on screen. NVR
  cameras share one stream among viewers; see [frigate.md](frigate.md).
- **Flow-trigger rows need flow access to deploy.** Deploying a dashboard that
  holds a flow-trigger binding needs edit access to that flow. Deployment
  reports a flow you hold nothing on as missing. A version deployed earlier
  keeps working.
- **Raw MQTT is never exposed.** Subscribing to broker topics and publishing to
  them always need the connection permission. Bind a device instead.

The [activity widget](events.md) reads the event log of the subjects it binds,
and never security entries.

### Display only

A row marked **Display only** (`displayOnly: true`) grants no write, however the
dashboard is shared or published. The checkbox is the only part of a binding
that decides whether it can be pressed. The source decides only how the row is
drawn. Mark every row a viewer should only watch.

### Conditions, status cues, and states

Visibility conditions and status cues name sources with the same fields as
bindings. Each one lets viewers read its source and never write it, like a
display-only row. Hiding a camera tile until the garage door opens therefore
lets the dashboard's viewers read the door's state.

Time conditions and variable conditions read no source and expose nothing. A
binding's `states` read only that binding, so they expose nothing beyond it.

## Variables

A binding templated on a variable, such as `"entityId":"sensor.${person}_steps"`,
exposes what each of the variable's options substitutes into it:
`sensor.james_steps` and `sensor.sam_steps` for two options, and nothing a value
outside them could name. Viewers cannot type free values. The dashboard ignores
a link naming a value that is not an option, and the server refuses a command
naming one.

- **Every option is readable.** Viewers may read every option's bindings
  whatever value they picked. Put data only one person should see on a
  dashboard of its own.
- **Query options follow the house.** A Home Assistant query's options are what
  Home Assistant currently returns, so a new `person.*` entity becomes readable
  through a `person.${person}` binding within about 30 seconds. Write a regex or
  a narrower pattern when the set should not grow.
- **Too many combinations expose nothing.** A placement whose variables combine
  into more than 256 values grants none of its bindings, and `validate_dashboard`
  reports it.
- **Followed helpers are readable.** A variable that follows an `input_select`
  or `select` lets viewers read that entity's state.
- **Resolved options are visible.** Anyone who may view the dashboard sees
  them. Previewing an unsaved query needs edit access. Saving or previewing a new or
  changed Prometheus, InfluxDB, or SQL query variable also needs
  `api:connections:read`, because it runs text on the connection.
- **Time ranges expose nothing.** They decide when data is read, not which.

A **Variable Buttons** placement with `scope: "dashboard"` lets viewers set
that one variable's dashboard-wide value, and like any press it needs the
control policy. Editors may always set variables. A placement with
`scope: "screen"` changes only the viewer's own screen and needs nothing.
[Variables](dashboard-authoring.md#variables) covers defining them.

## Charts and feeds

The server runs a chart's queries from the saved placement. Viewers never
send query text, so they cannot run their own queries through a chart, and their
variable values are used only when each is an option.

Anyone who may view the dashboard, including public, shared, and render viewers,
reads its charts. Query text is part of the placement's config, so viewers can
read it. Point charts at a database user that may read only what the dashboard
shows. PostgreSQL queries run in read-only transactions regardless.

Saving a chart whose queries change, through the editor, MCP, or import, needs
`api:connections:read`, and so does the editor's preview. A dashboard editor
therefore cannot read a connection through a chart they could not query
directly. Moving, resizing, or restyling a chart needs only edit access. See
[timeseries.md](timeseries.md) for connections.

The news feed fetches only the URLs saved in the placement, with variable
values that are options. A viewer cannot make the server request another
address.

## What to keep out of a placement's config

Every viewer of a dashboard is served every placement's config, including
widgets that are hidden or on another page. Do not put secrets there.

- The Wi-Fi QR widget's password is readable by every viewer, as it is from the
  code itself.
- Chart query text is readable, as [Charts and feeds](#charts-and-feeds)
  explains.
- The calendar plugin stores calendar sources, including Google API keys,
  outside the config. Its panel shows whether a key is set but never returns
  it.

## Plugin widgets

A plugin widget exposes only what its plugin declares. A widget that declares
nothing, such as a clock or an image, reads nothing and exposes nothing. A
widget whose plugin is unloaded exposes nothing either. Plugin authors declare
what a widget may read in its manifest; see [plugins.md](plugins.md).

A plugin may choose a broader boundary. A viewer of a dashboard holding the
weather widget can read forecasts for other locations as well as the one shown.
The crypto and stock widgets work the same way for prices and quotes. A viewer
of a dashboard holding the calendar widget reads only that widget's events.
Its sources and refresh stay with the dashboard's editors.

## Public controls and triggers

A control or widget trigger with **Allow public** set is public wherever it
appears, including outside any dashboard. That is separate from everything
above: display only, public control, and renders do not take it away. Use
dashboard sharing for the general case, and **Allow public** only when you mean
the object itself to be public.

## Live display state and commands

Exactly the people who may view a dashboard, renders included, may read its
live state. That covers hidden placements, the current page, alerts, the dim
override, pushed content, and dashboard-wide variable values. An alert's
title and message and any pushed content are shown to all of them, anonymous
viewers of a public dashboard included.

Sending commands through REST or MCP needs edit access. A flow's Dashboard
Command node can command any dashboard, the same reach flows already have over
controls and connections, so the permission to deploy flows is what limits it.
A Karakuri dashboard notification endpoint can also address any dashboard, and
saving one needs the connection write permission. Commands never change what a
dashboard exposes. They show and hide placements, add text, and pick among
variable options the dashboard already has.

## Renders

A render opens the dashboard in a headless browser and photographs it. The
browser is not signed in as anyone, not even the owner. Each render can view
only the dashboard it photographs, and only while the job runs. It can read what
that dashboard's deployed layout shows and press nothing. Public controls and
triggers stay callable, as they are for anyone.

An editor may render the draft through `render_dashboard`. That render reads
only the dashboard's draft and the data its widgets bind, and it still presses
nothing and opens no other dashboard. Adding `view=draft` to an ordinary render
address does not show the draft.

Anyone who may view a dashboard may render its deployed layout. The server
limits how many renders run at once with `render.workers` and
`render.max_queue_size`, and refuses new requests while the queue is full.

Inspecting a render's diagnostics needs edit access for a draft and view access
for the deployed layout. Diagnostics record no request headers or bodies and
strip credentials, query strings, and fragments from URLs. Dependency
inspection and validation need edit access, and they report references you
cannot read as unchecked.

### Render keys

Render addresses (`/dashboard/{id}/render/...` and a schedule's latest-render
address) are often used by an `<img>` tag or a photo frame that cannot sign in,
so they take a credential in the `token` query parameter. A private dashboard's
render page offers a **render key** for that. A render key is an API key tied
to the dashboard that holds no permissions.

- A render key opens that dashboard's direct and schedule render addresses and
  nothing else. Everywhere else, including the API, viewer pages, plugins, and
  other dashboards, it counts as no credential.
- It works only while its owner can view the dashboard as its owner, through a
  share, or because it is public. The dashboard manage permission does not
  count, so an administrator who sees a dashboard only through manage cannot
  create one.
- Unsharing the dashboard from the key's owner or deleting the key stops every
  address that carries it. Deleting the dashboard deletes its keys.
- Anyone holding a copied address can see the render, so treat the address as
  a password for that one picture.

The render page's **Access keys** table lists the dashboard's keys with their
prefix, creation, and last use. **Add key** takes a name, so the key given to
the hallway display can be deleted without breaking the kitchen frame's.
**Use** puts a key into the copied render URL, embed snippet, and schedule
addresses, and the trash button deletes a key.

Over the API, `GET /api/api_keys?dashboard_id={id}` lists your render keys for a
dashboard, and `POST /api/api_keys` with `dashboard_id` creates one, which needs
API key write permission and view access to the dashboard.
`GET /api/api_keys/{id}/value` shows a render key's value again. It works only
for render keys, only for their owner, and only when signed in, never with
another key.

## Uploaded files

Files uploaded to a widget, such as images and videos, are served at
`/widget/{placement}/{upload}/{filename}` to whoever may view the placement's
dashboard. A public dashboard's files load for anyone. A private dashboard's
files stop loading when the dashboard is made private or unshared. A file on a
placement that is not yet deployed needs edit access, and a render sees only
its own dashboard's files. A file you may not see answers not found, so
guessing addresses cannot confirm that it exists.

## Taking access away

Unsharing a dashboard, making it private, removing a role, deleting a session,
or unloading a plugin takes effect on open pages too, not only on the next
load. Within about 30 seconds, and immediately after most changes made through
the app or MCP, open viewers stop receiving updates they may no longer read.
