# Spec Kit workflow

Karakuri uses Spec Kit 1.2.4.dev0 from the
[grmrgecko/spec-kit](https://github.com/grmrgecko/spec-kit) fork of
[GitHub Spec Kit](https://github.com/github/spec-kit). Project-local skills are
installed for Codex in `.agents/skills/` and for Claude Code in
`.claude/skills/`. Scripts and templates live in
`.specify/`; product requirements and plans live in `specs/`, and standalone
library contracts live in `automation/specs/`. The
[constitution](../.specify/memory/constitution.md) records the engineering rules.

## Existing specifications

[specs/README.md](README.md) indexes the product, and
[automation/specs/README.md](../automation/specs/README.md) indexes the library. Each
`spec.md` states user scenarios, failure cases, numbered requirements, and
measurable outcomes. Each `plan.md` describes architecture, persistent entities,
interfaces, and verification supporting those requirements.

These documents describe the implemented product at commit `5158025`. They
were written from the current guides, code, and tests in the requirements form
of a project built from specs. They do not claim an earlier design history or
certify that every scenario was exercised during this documentation change.
The initial [source audit](code-audit.md) records selected corrections.
The [server and agent audit](server-agent-audit.md) maps every requirement
in those features to implementation and verification evidence, including known
limits and checks that still need live execution. The standalone `automation/`
module has six feature pairs
under `automation/specs/`; the CLI and resident lifecycle remain in root feature
`010-desktop-automation`. Both use this repository's Spec Kit installation and
constitution. The module index maps the former root module feature's requirements.
[Agent GUI nodes](013-agent-gui-nodes/spec.md) extend the agent catalog
with shared desktop inspection, waits, native actions, and fresh captures.
[Desktop interaction and reference images](014-desktop-interaction-vision/spec.md)
add serialized drag, observed window placement, and bounded percentage matching.
[Server utility nodes](016-server-utility-nodes/spec.md) add local time
windows, integration status triggers and numeric transforms to the server catalog.
[Isolated SQLite queries](017-isolated-sqlite/spec.md) add private flow
and shared global SQL storage with a memory filesystem and bounded snapshots.
[Configured notification services](018-notification-services/spec.md)
add the full notify catalog with encrypted settings and allowed endpoint IDs.
[Network socket nodes](019-network-socket-nodes/spec.md) add shared
TCP, UDP and WebSocket connections and seven server nodes.
[Agent action options](020-agent-action-options/spec.md) extend process,
network, service, file and image nodes and add HTTP requests from agent hosts.
[Graceful process termination](021-graceful-process-termination/spec.md)
adds opt-in cooperative shutdown and bounded escalation to agent commands/scripts.
[Agent browser prompts](022-agent-browser-prompts/spec.md) add human
questions launched through agent browsers, portable Markdown attachments, and
author-defined response outputs.
[Dashboard display control](023-dashboard-display-control/spec.md)
adds commands to live dashboards, lock-based visibility and conditions, pages,
alerts, dimming, a region layout, animations, and three display widgets.
[Home media and list widgets](024-home-media-widgets/spec.md) relays
Home Assistant cameras, media art, history, and to-do lists to dashboards and
adds spoken alerts and six widgets.
[Script node SDK](026-script-node-sdk/spec.md) adds engine-mediated node
functions to JavaScript, Python, and Bash flow scripts, invocation history and
validation, and catalog-driven completion, hover, signature help, and editor diagnostics.
[Time-series connections](027-timeseries-connections/spec.md) add
Prometheus, InfluxDB, and PostgreSQL connections, Home Assistant history
queries, query and write nodes, and buffered history forwarding.
[Dashboard charts](028-dashboard-charts/spec.md) add a
chart widget whose queries the server runs by placement.
[Durable waits and device state](029-durable-waits/spec.md)
keeps pending timers and fan-in groups across restarts and redeploys and
restores MQTT device state marked stale.
[Calendar connections](030-calendar-connections/spec.md) moves
calendars into encrypted ICS, CalDAV, and Google connections and fires flows at
event start and end without repeating across restarts.
[Location control kind](031-location-control/spec.md) adds a
`location` kind for people and trackers, drawn for Home Assistant persons and
fed by MQTT trackers.
[Zigbee2MQTT discovery and device availability](032-zigbee2mqtt-discovery/spec.md)
adds Zigbee devices from Zigbee2MQTT as ready-made MQTT devices and
gives every MQTT device an online, offline, or unknown availability.
[ESPHome native API](033-esphome-native-api/spec.md) connects
to ESPHome nodes directly and adds their entities as devices.
[Frigate NVR integration](034-frigate-nvr/spec.md) shows
Frigate cameras and events on dashboards and starts flows on its detections,
without karakuri recording or detecting anything itself.
[Energy tracking](035-energy-tracking/spec.md) totals usage
and cost per hour, day, week, and month from energy and power readings, under
time-of-use tariffs, with solar and grid, flow nodes, and a dashboard preset.
[Backup and restore](036-backup-restore/spec.md) writes the whole
installation, its keys included, to one passphrase-encrypted archive on demand
or on a schedule, and restores it onto the same or a new machine.
[Event log](037-event-log/spec.md) records who or what changed
controls and devices, failures, and security events, and shows them on an
Activity page, on dashboards, and to flows.
[Media control kind](038-media-control-kind/spec.md) draws and
commands Home Assistant, ESPHome, MQTT, and karakuri media players as one
kind on cards, in Now Playing, and in flows.
[Home Assistant areas and devices](039-ha-registries/spec.md) reads
Home Assistant's area, device, and entity registries so entities are found by
room, flows target and watch areas, and Home Assistant entities become devices.
[openHAB connection](040-openhab/spec.md) follows an openHAB server's
items, adds them as devices, and gives flows item triggers and commands.
[Weather station overrides](041-weather-station-overrides/spec.md)
replace a location's current weather with local station readings from a flow
until each reading reaches its maximum age.
[Faceplates](042-faceplates/spec.md) give a flow named tabs of
controls that feed its nodes, for users who should not edit the graph.
[Flow sharing](043-flow-sharing/spec.md) enforces flow ownership and
shares a flow with users and groups at faceplate, view, edit, or admin level.
[Tile appearance](044-tile-appearance/spec.md) gives bindings state
looks and value formats, adds a Button widget, and lets any placement override
its tile style and draw animated status borders.
[Home and overview](045-home-overview/spec.md) makes `/` a per-user
start page, moves the flow list to `/flows`, and adds an overview of what
needs attention, what runs next, and unfinished work.
[Help center](046-help-center/spec.md) adds a Help menu with an index of
every guide shipped in the server and a viewer that moves between them.
A proposal becomes a requirement through a scoped specification change.

Read the spec, plan, and user guide before changing behavior. Update their
requirements and acceptance scenarios with the implementation. Preserve IDs so
reviews and tests can refer to them. Resolve disagreements against code, then
correct the documents explicitly; do not transcribe an accidental bug into a
requirement. User guides in `docs/` explain how to use the software; they never
cite specs or describe internals, which belong in plans.

## Using the skills

Open a fresh Codex or Claude Code session in this directory to discover the
installed skills. Codex invokes them as `$speckit-*` and Claude Code as
`/speckit-*`; the examples below use the Codex form.
Use `$speckit-specify`, `$speckit-plan`, `$speckit-tasks`,
`$speckit-implement`, and `$speckit-converge` for new features. Use
`$speckit-clarify`, `$speckit-checklist`, and `$speckit-analyze` when they help
resolve requirements or check consistency. `$speckit-constitution` maintains
the principles. These are agent prompts, not shell commands.

A change to behavior that a spec already states updates that spec in place.
Do not open a new feature for it. The fork's `speckit-specify` skill checks
for an owning spec before creating one, and the local patch extends that check
to Karakuri: it first reads both spec indexes and the affected specs. It
creates a numbered feature only for a capability no spec owns. Even then, it
rewrites the prior requirements the capability changes instead of overriding
them in the new spec. Updates keep IDs stable: changed
items are rewritten, new items take the next unused number, and removed IDs are
not reused. The planning skill revises an existing `plan.md` the same way.

Success criteria, quickstart scenarios, and test tasks use the shortest run,
smallest data set, and lightest load that would expose a failure. Soak and
multi-hour runs appear only when a requirement names that duration.

To select an existing feature by hand before planning:

```sh
export SPECIFY_FEATURE_DIRECTORY=specs/002-flow-authoring
.specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

Library features use their full module path with the same scripts:

```sh
export SPECIFY_FEATURE_DIRECTORY=automation/specs/005-session-access-and-helpers
.specify/scripts/bash/check-prerequisites.sh --paths-only --json
```

Relative feature paths resolve from the shared repository root, even when a
command runs inside `automation/`. Keep `SPECIFY_INIT_DIR` at the repository
root when using that override; the module has no separate `.specify/` directory.

For new capabilities, the specify skill creates a numbered feature directory
and records its local selection. It chooses the specification root by contract
ownership. Server, frontend, agent, and automation CLI features use root
`specs/`; independently importable automation library features use
`automation/specs/`. Explicit feature selection takes precedence. The shell
helper accepts the same scopes. Preview their next names without writing
anything:

```sh
.specify/scripts/bash/create-new-feature.sh --dry-run --json \
  --short-name example-feature 'Describe the proposed behavior'

.specify/scripts/bash/create-new-feature.sh --scope automation --dry-run --json \
  --short-name example-feature 'Describe the proposed library behavior'
```

`--scope root` is the default. Numbering and numeric-prefix conflicts are local
to the chosen folder. Without `--dry-run`, the script creates that feature and
writes its full repository-relative path to the shared `.specify/feature.json`.
The pointer is ignored by Git and belongs to this checkout.
`SPECIFY_FEATURE_DIRECTORY` overrides it. Set `SPECIFY_FEATURE_NO_PERSIST=1` for
checks that must not update local selection. No baseline feature is selected globally.

`setup-plan.sh` preserves an existing `plan.md`; the planning skill can edit it.
Preserve the existing design when extending a baseline. Generate `tasks.md` for
actual new work. The baseline has no invented completed implementation tasks.
`check-prerequisites.sh --require-tasks` is for use after tasks exist and
correctly refuses a baseline with no task list.

## Repository rules

Spec Kit follows `AGENTS.md`. Build with `make dev`; test with `make test`
and relevant targets. Restart `karakuri-dev.service` after a build, then verify
the unit and HTTP listener. Use MCP for functional checks and the browser for
changed canvas derivations and UI. Commit coherent verified changes on the
current branch. Branch creation, pushes, issues, and publishing require a request.

No Git extension is installed. The bundled `$speckit-taskstoissues` skill is
deprecated upstream and has no use here because the repository has no remote. Do not invoke it or install
GitHub extensions during ordinary feature work. Keep private agent state,
credentials, generated application configs, and databases out of commits.

## CLI installation and upgrades

The checked-in scripts work with Bash and the usual Unix utilities. The Specify
CLI is needed only to manage or upgrade the toolkit. With Python 3.11 or later
and `uv` installed, install the pinned fork commit:

```sh
uv tool install specify-cli \
  --from git+https://github.com/grmrgecko/spec-kit.git@41a93d9a98c85bbc28b4694598119bd77beaa36d
specify version
specify integration status
```

`specify integration status` is read-only. It reports both integrations and
any managed file whose content no longer matches its manifest hash.

Reproduce the scaffold in a temporary directory using the command in
[.specify/UPSTREAM.md](../.specify/UPSTREAM.md), then review its diff against the
installed assets. Preserve the constitution, template overrides, and documented
local scope changes. The exact source version and license are recorded there.
